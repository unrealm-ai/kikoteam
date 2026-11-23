package channel

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDingMediaURLRejectionIsSafeAndClassified(t *testing.T) {
	for _, tc := range []struct{ address, reason, hostname string }{
		{"https://Files.DingTalk.Example:84/private/path?q=token", "unexpected_port", "files.dingtalk.example"},
		{"ftp://files.dingtalk.com/private/path?q=token", "unexpected_scheme", "files.dingtalk.com"},
		{"https://privateUser:secret@files.dingtalk.com/private/path?q=token", "userinfo_present", "files.dingtalk.com"},
		{"https://files.dingtalk.com:8443/private/path?q=token", "unexpected_port", "files.dingtalk.com"},
		{"https:/private/path?q=token", "missing_hostname", ""},
		{"https://bad%xx.dingtalk.com/private/path?q=token", "malformed_url", ""},
		{"https://bad_label.example/private/path?q=token", "invalid_dns_hostname", ""},
		{"https://127.0.0.1/private/path?q=token", "invalid_dns_hostname", ""},
		{"https://" + strings.Repeat("a", 64) + ".example/private/path?q=token", "invalid_dns_hostname", ""},
	} {
		_, err := fetchMediaURL(t.Context(), "dingtalk", tc.address, "")
		var got *mediaURLRejection
		if !errors.Is(err, mediaDownloadURLInvalid) || !errors.As(err, &got) || got.stage != "initial" ||
			got.reason != tc.reason || got.hostname != tc.hostname || got.redirectHop != 0 ||
			err.Error() != "download_url_invalid" {
			t.Errorf("incorrect safe rejection for %s: %+v %v", tc.reason, got, err)
		}
	}
	_, err := fetchMediaURL(t.Context(), "wecom", "https://outside.example/private?q=token", "")
	var got *mediaURLRejection
	if !errors.Is(err, mediaDownloadURLInvalid) || errors.As(err, &got) {
		t.Fatal("unapproved provider acquired extra diagnostics")
	}
}

func TestDingMediaRedirectRejectionAndTrace(t *testing.T) {
	for _, tc := range []struct {
		rejectHop          int
		dest, reason, host string
	}{
		{2, "https://privateUser:privatePass@unsafe.example/privatePath?privateQuery=privateToken", "userinfo_present", "unsafe.example"},
		{4, "https://files.dingtalk.com/privatePath?privateQuery=privateToken", "redirect_limit", "files.dingtalk.com"},
	} {
		hops := 0
		client := http.Client{
			CheckRedirect: resolvedDingMediaPolicy.checkRedirect,
			Transport: mediaTestTransport(func(req *http.Request) (*http.Response, error) {
				if req.Referer() != "" {
					t.Fatal("redirect forwarded original capability")
				}
				hops++
				location := "https://files.alicdn.com/other?secret=placeholder"
				if hops == tc.rejectHop {
					location = tc.dest
				}
				return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {location}}, Body: http.NoBody}, nil
			}),
		}
		resp, err := client.Get("https://files.dingtalk.com/start?signed=privateToken")
		if resp != nil {
			if e := resp.Body.Close(); e != nil {
				t.Fatal(e)
			}
		}
		if !errors.Is(err, mediaDownloadURLInvalid) || hops != tc.rejectHop {
			t.Fatal("rejected redirect reached the transport", hops)
		}
		recordDingMediaURLRejection(t, err, tc.reason, tc.host, tc.rejectHop)
	}
}

func recordDingMediaURLRejection(t *testing.T, providerErr error, reason, host string, hop int) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/failed") || r.Header.Get("X-Kiko-Media-Failure") != "download_url_invalid" {
			t.Error("changed Manager failure classification")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	var logs bytes.Buffer
	backend, err := NewBackend(server.URL, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	backend.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
	job := MediaJob{InboxID: "example-inbox", BindingID: "example-binding", Provider: "dingtalk", Ordinal: 3}
	if err := backend.completeMedia(t.Context(), job, fetchedMedia{}, providerErr); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"privateUser", "privatePass", "privatePath", "privateQuery", "privateToken", "https://", "http://", "placeholder"} {
		if strings.Contains(logs.String(), forbidden) {
			t.Fatal("log leaked a URL component")
		}
	}
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatal(err)
		}
		if m["event"] != "channel.media_download_url_rejected" {
			continue
		}
		if m["inbox_id"] != job.InboxID || m["binding_id"] != job.BindingID || m["ordinal"] != float64(job.Ordinal) ||
			m["stage"] != "redirect" || m["reason"] != reason || m["hostname"] != host || m["redirect_hop"] != float64(hop) {
			t.Fatal("missing correlated redacted diagnostics", m)
		}
		return
	}
	t.Fatal("no correlated redacted rejection event")
}
