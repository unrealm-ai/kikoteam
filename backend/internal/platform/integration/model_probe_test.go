package integration

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestModelProtocolsParallel(t *testing.T) {
	for _, protocol := range []string{"chat_completions", "openai_responses", "anthropic_messages"} {
		t.Run(protocol, func(t *testing.T) {
			var calls atomic.Int32
			both := make(chan struct{})
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 2 {
					close(both)
				}
				select {
				case <-both:
				case <-time.After(time.Second):
					t.Error("not parallel")
					return
				}
				raw, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				image := strings.Contains(string(raw), "base64,") || strings.Contains(string(raw), `"type":"image"`)
				path := "/v1/chat/completions"
				if protocol == "openai_responses" {
					path = "/v1/responses"
				}
				if protocol == "anthropic_messages" {
					path = "/v1/messages"
				}
				if r.URL.Path != path {
					t.Errorf("path %s", r.URL.Path)
				}
				if image {
					w.WriteHeader(400)
					if _, err := io.WriteString(w, `{"error":{"code":"vision_not_supported"}}`); err != nil {
						t.Error(err)
					}
					return
				}
				reply := `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"OK"}}]}`
				if protocol == "openai_responses" {
					reply = `{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"OK"}]}]}`
				}
				if protocol == "anthropic_messages" {
					reply = `{"stop_reason":"end_turn","content":[{"type":"text","text":"OK"}]}`
				}
				if _, err := io.WriteString(w, reply); err != nil {
					t.Error(err)
				}
			}))
			defer s.Close()
			p := ModelProber{AllowLoopbackHTTP: true}
			v, e := p.Probe(context.Background(), service.ProbeInput{Protocol: protocol, Endpoint: s.URL, ModelID: "fixture", APIKey: "synthetic-secret"})
			if e != nil || v.Text.Status != "passed" || v.Image.Status != "unsupported" || v.Text.Output != "OK" || calls.Load() != 2 {
				t.Fatalf("%+v %v", v, e)
			}
		})
	}
}
func TestModelProbeTimeout(t *testing.T) {
	if ModelTextTimeout != 20*time.Second || ModelImageTimeout != 40*time.Second {
		t.Fatal("changed timeout")
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(100 * time.Millisecond):
		}
	}))
	defer s.Close()
	p := ModelProber{AllowLoopbackHTTP: true, textTimeout: 10 * time.Millisecond, timeImageTimeout: 20 * time.Millisecond}
	in := service.ProbeInput{Protocol: "chat_completions", Endpoint: s.URL, APIKey: "fixture"}
	v, e := p.Probe(context.Background(), in)
	if e != nil || v.Text.Issue != "timeout" || v.Image.Issue != "timeout" || v.Image.Status != "unknown" {
		t.Fatalf("%+v %v", v, e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	v, e = p.Probe(ctx, in)
	if e != nil || v.Text.Issue != "cancelled" || v.Image.Issue != "cancelled" {
		t.Fatalf("%+v %v", v, e)
	}
}
func TestModelProbeErrors(t *testing.T) {
	for _, tc := range []struct {
		status         int
		body, expected string
	}{{400, `{"error":{"code":"vision_not_supported"}}`, "unsupported"}, {422, `{"error":{"message":"model does not support image input"}}`, "unsupported"}, {401, `{"error":{"message":"image unsupported"}}`, "unknown"}, {400, `{"error":{"message":"bad request"}}`, "unknown"}, {200, `null`, "unknown"}, {200, strings.Repeat("x", 65537), "unknown"}} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			if _, err := io.WriteString(w, tc.body); err != nil {
				t.Error(err)
			}
		}))
		p := ModelProber{AllowLoopbackHTTP: true}
		v, e := p.Probe(context.Background(), service.ProbeInput{Protocol: "chat_completions", Endpoint: s.URL, APIKey: "fixture"})
		s.Close()
		if e != nil || v.Text.Status != "failed" || v.Image.Status != tc.expected {
			t.Fatalf("%+v %v", v, e)
		}
	}
}
func TestModelProbeRedirect(t *testing.T) {
	var called atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called.Add(1) }))
	defer target.Close()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer s.Close()
	p := ModelProber{AllowLoopbackHTTP: true}
	v, e := p.Probe(context.Background(), service.ProbeInput{Protocol: "chat_completions", Endpoint: s.URL, APIKey: "fixture"})
	if e != nil || called.Load() != 0 || v.Text.HTTPStatus != 302 {
		t.Fatalf("%+v %v calls=%d", v, e, called.Load())
	}
}
func TestModelFixtureAndRedaction(t *testing.T) {
	f, e := newModelImageFixture()
	if e != nil || len(f.Data) == 0 || len(f.Code) != 4 {
		t.Fatal(e)
	}
	b, e := json.Marshal(map[string]string{"shape": "square", "color": f.Color, "code": f.Code})
	if e != nil {
		t.Fatal(e)
	}
	if !imageAnswer(string(b), f) || imageAnswer(`{"shape":"square","color":"purple","code":"none"}`, f) {
		t.Fatal("image validation")
	}
	v := safeModelOutput("my-private-key Bearer leaked sk-somethingsecret123", "my-private-key")
	if strings.Contains(v, "private") || strings.Contains(v, "leaked") || strings.Contains(v, "something") {
		t.Fatal(v)
	}
	for _, protocol := range []string{"chat_completions", "openai_responses", "anthropic_messages"} {
		wire, e := json.Marshal(modelBody(protocol, "model", imagePrompt, f.Data))
		if e != nil || !strings.Contains(string(wire), f.Data) {
			t.Fatalf("missing image %s %v", protocol, e)
		}
	}
}
func TestNormalizeModelEndpoint(t *testing.T) {
	for _, u := range []string{"https://example.com", "https://example.com/", "https://example.com/v1/"} {
		got, e := NormalizeModelEndpoint("chat_completions", u)
		if e != nil || got != "https://example.com/v1" {
			t.Fatalf("%s %v", got, e)
		}
	}
	for _, u := range []string{"https://user:pass@example.com", "https://example.com?a=b", "https://example.com?", "https://example.com#x", "file:///tmp/x"} {
		if _, e := NormalizeModelEndpoint("chat_completions", u); e == nil {
			t.Fatalf("accepted %s", u)
		}
	}
	p := ModelProber{}
	if _, e := p.Probe(context.Background(), service.ProbeInput{Protocol: "chat_completions", Endpoint: "http://127.0.0.1:1", APIKey: "x"}); e == nil {
		t.Fatal("loopback default")
	}
}

func TestModelImagePixelsRoundTrip(t *testing.T) {
	f, err := newModelImageFixture()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(f.Data)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, _ := img.At(40, 20).RGBA()
	c := "yellow"
	if r > 50000 && g < 10000 {
		c = "red"
	} else if b > 50000 {
		c = "blue"
	} else if g > 30000 && r < 10000 {
		c = "green"
	}
	glyphs := []string{"111101101101111", "010110010010111", "111001111100111", "111001111001111", "101101111001001", "111100111001111", "111100111101111", "111001001001001", "111101111101111", "111101111001111"}
	code := ""
	for i := range 4 {
		var bits strings.Builder
		for y := range 5 {
			for x := range 3 {
				r, _, _, _ := img.At(25+i*12+x*3, 61+y*3).RGBA()
				if r == 0 {
					bits.WriteByte('1')
				} else {
					bits.WriteByte('0')
				}
			}
		}
		for d, glyph := range glyphs {
			if bits.String() == glyph {
				code += string(rune('0' + d))
			}
		}
	}
	answer, err := json.Marshal(map[string]string{"shape": "square", "color": c, "code": code})
	if err != nil {
		t.Fatal(err)
	}
	if !imageAnswer(string(answer), f) {
		t.Fatal("PNG pixels did not round trip")
	}
	for _, protocol := range []string{"chat_completions", "openai_responses", "anthropic_messages"} {
		output := string(answer)
		quoted, err := json.Marshal(output)
		if err != nil {
			t.Fatal(err)
		}
		body := `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":` + string(quoted) + `}}]}`
		if protocol == "openai_responses" {
			body = `{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":` + string(quoted) + `}]}]}`
		}
		if protocol == "anthropic_messages" {
			body = `{"stop_reason":"end_turn","content":[{"type":"text","text":` + string(quoted) + `}]}`
		}
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, err := io.WriteString(w, body); err != nil {
				t.Error(err)
			}
		}))
		p := ModelProber{AllowLoopbackHTTP: true}
		got := p.run(context.Background(), service.ProbeInput{Protocol: protocol, Endpoint: s.URL, ModelID: "fixture", APIKey: "synthetic"}, imagePrompt, f.Data, ModelImageTimeout, &f)
		s.Close()
		if got.Status != "passed" {
			t.Fatalf("%s %+v", protocol, got)
		}
	}
}

func TestModelProbeNoAuthentication(t *testing.T) {
	for _, protocol := range []string{"chat_completions", "openai_responses", "anthropic_messages"} {
		t.Run(protocol, func(t *testing.T) {
			var calls atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if _, ok := r.Header["Authorization"]; ok {
					t.Error("empty authorization was sent")
				}
				if _, ok := r.Header["X-Api-Key"]; ok {
					t.Error("empty API key was sent")
				}
				if protocol == "anthropic_messages" && r.Header.Get("Anthropic-Version") != "2023-06-01" {
					t.Error("missing protocol version")
				}
				w.WriteHeader(http.StatusBadRequest)
			}))
			defer s.Close()
			p := ModelProber{AllowLoopbackHTTP: true}
			_, err := p.Probe(context.Background(), service.ProbeInput{Protocol: protocol, Endpoint: s.URL, ModelID: "fixture"})
			if err != nil || calls.Load() != 2 {
				t.Fatalf("no-auth request failed: %v calls=%d", err, calls.Load())
			}
		})
	}
}
func TestModelOutputLimit(t *testing.T) {
	output := safeModelOutput(strings.Repeat("字", 5000), "")
	if len([]rune(output)) != 4096 || !strings.HasSuffix(output, "…") {
		t.Fatal("output exceeded contract bound")
	}
}
