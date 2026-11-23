package channel

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"
	"time"
)

func TestWeComInboundFileUsesDownloadContentDisposition(t *testing.T) {
	key := bytes.Repeat([]byte("k"), 32)
	encoded := base64.RawStdEncoding.EncodeToString(key)
	raw, err := json.Marshal(map[string]any{
		"msgtype": "file",
		"file":    map[string]string{"url": "https://attachments.example/synthetic", "aeskey": encoded},
	})
	if err != nil {
		t.Fatal(err)
	}
	refs := wecomMedia("file", raw)
	if len(refs) != 1 || refs[0].Name != "" { // The real callback need not carry a filename.
		t.Fatal("WeCom callback no longer accepts the official shape", refs)
	}
	var loc mediaLocator
	if err := json.Unmarshal([]byte(refs[0].Locator), &loc); err != nil {
		t.Fatal(err)
	}
	plain := []byte("fixture document bytes")
	pad := 32 - len(plain)%32
	encrypted := append(append([]byte{}, plain...), bytes.Repeat([]byte{byte(pad)}, pad)...)
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	cipher.NewCBCEncrypter(block, key[:16]).CryptBlocks(encrypted, encrypted)
	const destination = "https://download.example/synthetic"
	calls := 0
	got, err := fetchMediaURLWithTransport(t.Context(), "wecom", loc.Key, loc.AESKey, mediaTestTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		switch req.URL.String() {
		case loc.Key:
			return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {destination}}, Body: http.NoBody}, nil
		case destination:
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Disposition": {`attachment; filename="dummy.docx"; filename*=UTF-8''%E5%AF%B9%E8%B4%A6%20%E5%8D%95.pdf`}},
				Body:       io.NopCloser(bytes.NewReader(encrypted)),
			}, nil
		default:
			t.Fatal("unexpected attachment URL", req.URL)
			return nil, nil
		}
	}))
	if err != nil || calls != 2 || !bytes.Equal(got.data, plain) || got.name != "对账 单.pdf" {
		t.Fatal("incorrect decrypted attachment or recovered name", got.name, err)
	}
}

func TestInboundMediaFilenameSafetyAndFailureStages(t *testing.T) {
	for value, expected := range map[string]string{
		`attachment; filename="../report.pdf"`:              "report.pdf",
		`inline; filename="note.txt"`:                       "note.txt",
		`attachment; filename*=UTF-8''..%2F..`:              "",
		`attachment; filename*=UTF-8''safe%0D%0A.pdf`:       "safe.pdf",
		"ignored; filename=ignored.pdf":                     "",
		"attachment; filename*=invalid":                     "",
		"attachment; filename=" + strings.Repeat("a", 4096): "",
	} {
		if got := mediaDispositionName(value); got != expected {
			t.Errorf("unexpected disposition basename %q, want %q", got, expected)
		}
	}
	for _, tc := range []struct {
		provider string
		response *http.Response
		expected inboundMediaFailure
	}{
		{"dingtalk", &http.Response{StatusCode: 403}, mediaDownloadHTTP},
		{"wecom", &http.Response{StatusCode: 200, ContentLength: inboundMediaLimit + 33}, mediaTooLarge},
		{"dingtalk", &http.Response{StatusCode: 200, ContentLength: inboundMediaLimit + 1}, mediaTooLarge},
		{"wecom", &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("not ciphertext"))}, mediaDecryptFailed},
	} {
		if _, err := decodeMediaDownload(tc.response, tc.provider, ""); !errors.Is(err, tc.expected) {
			t.Fatalf("incorrect normalized failure stage %v, want %v", err, tc.expected)
		}
	}
	if _, err := fetchMediaURL(t.Context(), "wecom", "http://example.qq.com", "", true); !errors.Is(err, mediaDownloadURLInvalid) {
		t.Fatal("HTTPS gate failed", err)
	}
	if got := mediaFailureCode(context.DeadlineExceeded, mediaDownloadFailed); got != mediaDownloadTimeout {
		t.Fatal("download timeout not classified", got)
	}
	if _, err := readMedia(strings.NewReader("123"), 2); !errors.Is(err, mediaTooLarge) {
		t.Fatal("length safety regressed", err)
	}
}

func TestInboundMedia100MiBWireBudgetDoesNotRaiseOutbound(t *testing.T) {
	const wanted = 100 << 20
	for _, tc := range []struct {
		provider string
		maxWire  int64
	}{
		{"dingtalk", wanted},
		{"wecom", wanted + 32},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			// At the boundary the body is opened; one byte beyond the declared
			// bound must fail before any read. The fixture needs no large body.
			body := io.NopCloser(iotest.ErrReader(errors.New("fixture body read")))
			resp := &http.Response{StatusCode: http.StatusOK, ContentLength: tc.maxWire, Body: body}
			if _, err := decodeMediaDownload(resp, tc.provider, ""); !errors.Is(err, mediaDownloadFailed) {
				t.Fatalf("exact wire cap was not admitted to read: %v", err)
			}
			resp.ContentLength++
			if _, err := decodeMediaDownload(resp, tc.provider, ""); !errors.Is(err, mediaTooLarge) {
				t.Fatalf("wire cap + 1 was not rejected: %v", err)
			}
		})
	}
	if got := responseLimit("/api/internal/v1/channel-deliveries/fake/content"); got != (32<<20)*4/3+65536 || outboundMediaLimit != 32<<20 {
		t.Fatal("raising inbound attachment size changed the outbound artifact limits")
	}
}

func TestFeishuInboundMedia100MiBWireBudget(t *testing.T) {
	const wanted = 100 << 20
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		length := wanted
		if r.URL.Path == "/oversize" {
			length++
		}
		w.Header().Set("Content-Length", strconv.Itoa(length))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	for _, tc := range []struct {
		path string
		want error
	}{{"/at-cap", nil}, {"/oversize", mediaTooLarge}} {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+tc.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := (boundedLarkClient{}).Do(req)
		if !errors.Is(err, tc.want) {
			t.Fatalf("incorrect Feishu header boundary: %v, want %v", err, tc.want)
		}
		if resp != nil {
			if err := resp.Body.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestWeComInboundMedia100MiBPlaintextAfterPadding(t *testing.T) {
	const wanted = 100 << 20
	key := bytes.Repeat([]byte("k"), 32)
	encoded := base64.RawStdEncoding.EncodeToString(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, wanted+32)
	for _, pad := range []int{32, 31} {
		// Same ciphertext length: valid full padding reveals exactly 100 MiB,
		// while a valid 31-byte pad reveals one byte beyond the plaintext cap.
		copy(buffer[len(buffer)-pad:], bytes.Repeat([]byte{byte(pad)}, pad))
		cipher.NewCBCEncrypter(block, key[:16]).CryptBlocks(buffer, buffer)
		plain, err := decryptWecomFile(buffer, encoded)
		if pad == 31 {
			if !errors.Is(err, mediaTooLarge) || plain != nil {
				t.Fatalf("oversized plaintext passed with valid CBC padding: %v", err)
			}
		} else if err != nil || len(plain) != wanted {
			t.Fatalf("plaintext at 100 MiB did not pass after removing padding: %v", err)
		}
	}
}

func TestDingTalkInboundPictureFallbackDownloadCode(t *testing.T) {
	for _, tc := range []struct{ kind, body string }{
		{"picture", `{"pictureDownloadCode":"only-picture"}`},
		{"richText", `{"richText":[{"type":"picture","pictureDownloadCode":"only-picture"}]}`},
		{"picture", `{"downloadCode":"only-picture","pictureDownloadCode":"alternative"}`},
	} {
		refs := dingMedia(tc.kind, json.RawMessage(tc.body))
		var loc mediaLocator
		if len(refs) != 1 || json.Unmarshal([]byte(refs[0].Locator), &loc) != nil || loc.Key != "only-picture" {
			t.Fatal("missing supported picture locator", tc.kind)
		}
	}
}

func TestInboundMediaWorkerPoolIsBoundedAndCancels(t *testing.T) {
	started := make(chan struct{}, inboundMediaWorkers+1)
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-r.Context().Done()
	}))
	defer h.Close()
	backend, err := NewBackend(h.URL, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	s := &Supervisor{Backend: backend}
	done := make(chan struct{})
	go func() { defer close(done); s.runInboundMedia(ctx) }()
	for range inboundMediaWorkers {
		select {
		case <-ctx.Done():
			t.Fatal("did not claim in bounded parallel workers", ctx.Err())
		case <-started:
		}
	}
	select {
	case <-started:
		t.Fatal("too many parallel claims")
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("elected term did not cancel media workers")
	}
}

type mediaTestTransport func(*http.Request) (*http.Response, error)

func (m mediaTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return m(req)
}

func TestInboundMediaRedirectDoesNotForwardSignedReferer(t *testing.T) {
	for _, tc := range []struct {
		policy  mediaURLPolicy
		final   string
		blocked bool
	}{
		{resolvedDingMediaPolicy, "https://files.alicdn.com/final", false},
		{resolvedDingMediaPolicy, "https://cdn.example:8080/final", true},
		{"wecom", "http://cdn.example/final", true},
	} {
		hops := 0
		client := http.Client{
			CheckRedirect: tc.policy.checkRedirect,
			Transport: mediaTestTransport(func(req *http.Request) (*http.Response, error) {
				hops++
				if req.Referer() != "" {
					t.Fatalf("forwarded Referer on hop %d", hops)
				}
				resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: http.NoBody}
				switch hops {
				case 1:
					resp.StatusCode = http.StatusFound
					resp.Header.Set("Location", "https://files.dingtalk.com/next?signed=second")
				case 2:
					resp.StatusCode = http.StatusFound
					resp.Header.Set("Location", tc.final)
				}
				return resp, nil
			}),
		}
		resp, err := client.Get("https://files.dingtalk.com/start?signed=first")
		if tc.blocked {
			if !errors.Is(err, mediaDownloadURLInvalid) || hops != 2 {
				t.Fatal("invalid redirect reached transport", hops, err)
			}
		} else if err != nil || hops != 3 || resp.StatusCode != http.StatusOK {
			t.Fatal("approved redirect did not complete", hops, err)
		}
		if resp != nil {
			if err := resp.Body.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestInboundMediaRedirectLimit(t *testing.T) {
	for _, policy := range []mediaURLPolicy{resolvedDingMediaPolicy, "wecom"} {
		for _, redirects := range []int{5, 6} {
			t.Run(string(policy)+"/"+strconv.Itoa(redirects), func(t *testing.T) {
				requests := 0
				client := http.Client{
					CheckRedirect: policy.checkRedirect,
					Transport: mediaTestTransport(func(*http.Request) (*http.Response, error) {
						requests++
						if requests <= redirects {
							return &http.Response{
								StatusCode: http.StatusFound,
								Header:     http.Header{"Location": {"/hop/" + strconv.Itoa(requests)}},
								Body:       http.NoBody,
							}, nil
						}
						return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
					}),
				}
				resp, err := client.Get("https://attachments.example/start")
				if resp != nil {
					if closeErr := resp.Body.Close(); closeErr != nil {
						t.Fatal(closeErr)
					}
				}
				if redirects == 5 && (err != nil || requests != 6) {
					t.Fatalf("fifth redirect did not complete: requests=%d error=%v", requests, err)
				}
				if redirects == 6 && (!errors.Is(err, mediaDownloadURLInvalid) || requests != 6) {
					t.Fatalf("sixth redirect was not blocked before sending: requests=%d error=%v", requests, err)
				}
			})
		}
	}
}
