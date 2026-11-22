package wecomcontacts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func wire(t *testing.T, v any) string {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}
func envelope(t *testing.T, v any) string {
	return wire(t, map[string]any{"errcode": 0, "results_json": wire(t, map[string]any{"result": wire(t, v)})})
}
func fixture(t *testing.T, change func(string, string) string) Client {
	t.Helper()
	return Client{HTTPClient: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.URL.Host != "qyapi.weixin.qq.com" || r.Method != "POST" {
			t.Fatal("unsafe destination")
		}
		var data map[string]any
		if e := json.NewDecoder(r.Body).Decode(&data); e != nil {
			t.Fatal(e)
		}
		var body string
		switch r.URL.Path {
		case "/cgi-bin/aibot/cli/get_cli_config":
			sum := sha256.Sum256([]byte("secretbot" + fmt.Sprint(int64(data["time"].(float64))) + fmt.Sprint(data["nonce"])))
			if data["signature"] != hex.EncodeToString(sum[:]) || data["bot_id"] != "bot" || data["bind_source"] != float64(1) {
				t.Fatal("bad auth")
			}
			body = `{"errcode":0,"token":"test-token"}`
		case "/cli/service/discovery":
			if data["payload"] != `{"service":"contact"}` {
				t.Fatal(data)
			}
			body = envelope(t, map[string]any{"resources": map[string]any{"users": map[string]any{"methods": map[string]any{"search": map[string]any{"http_method": "POST", "path": "/contact/users/search"}}}}})
		case "/cli/contact/users/search":
			if r.Header.Get("Authorization") != "Bearer test-token" || data["payload"] != `{"keywords":["Alice"]}` {
				t.Fatal("bad scoped search")
			}
			body = envelope(t, map[string]any{"users": []any{map[string]any{"userid": "search-id", "name": "Alice", "departments": []string{"R&D"}}}, "hint": "limited"})
		default:
			t.Fatal("unexpected API", r.URL.Path)
		}
		if change != nil {
			body = change(r.URL.Path, body)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}}
}
func TestScopedSearch(t *testing.T) {
	v, e := fixture(t, nil).Search(context.Background(), "bot", "secret", "Alice")
	if e != nil || len(v.Items) != 1 || !v.Limited || v.Items[0].SearchUserID != "search-id" || v.Items[0].CallbackUserID != "" {
		t.Fatal(v, e)
	}
}
func TestRejectProtocol(t *testing.T) {
	for _, mode := range []string{"origin", "method", "write", "missing", "nested", "oversize", "count"} {
		t.Run(mode, func(t *testing.T) {
			c := fixture(t, func(path, s string) string {
				if path == "/cli/service/discovery" {
					switch mode {
					case "origin":
						return envelope(t, map[string]any{"base_url": "https://evil.invalid/cli", "resources": map[string]any{"users": map[string]any{"methods": map[string]any{"search": map[string]any{"http_method": "POST", "path": "/contact/search"}}}}})
					case "method":
						return strings.ReplaceAll(s, "POST", "GET")
					case "write":
						return strings.ReplaceAll(s, "/contact/users/search", "/contact/users/delete")
					case "missing":
						return envelope(t, map[string]any{})
					case "nested":
						return `{"errcode":0,"results_json":"[]"}`
					case "oversize":
						return strings.Repeat("x", 256*1024+1)
					}
				}
				if mode == "count" && path == "/cli/contact/users/search" {
					return envelope(t, map[string]any{"users_count": 1})
				}
				return s
			})
			if _, e := c.Search(context.Background(), "bot", "secret", "Alice"); e == nil {
				t.Fatal("unsafe accepted")
			}
		})
	}
}
func TestAuthRefreshOnlyOnce(t *testing.T) {
	calls := 0
	c := fixture(t, func(path, s string) string {
		if path == "/cli/contact/users/search" {
			calls++
			return `{"errcode":853004}`
		}
		return s
	})
	if _, e := c.Search(context.Background(), "bot", "secret", "Alice"); e == nil || calls != 2 {
		t.Fatal(calls, e)
	}
}
func TestRedirectAndErrorRedaction(t *testing.T) {
	calls := 0
	c := Client{HTTPClient: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://evil.invalid/secret"}}, Body: io.NopCloser(strings.NewReader("secret"))}, nil
	})}}
	_, e := c.Search(context.Background(), "bot", "secret", "Alice")
	if e == nil || calls != 1 || strings.Contains(e.Error(), "secret") {
		t.Fatal(calls, e)
	}
}

func TestContactDiscoveryInvalidIsNotUnsupported(t *testing.T) {
	for _, tc := range []struct {
		name     string
		value    any
		wantCode string
	}{
		{"malformed", []string{"users"}, "wecom_contacts_invalid_response"},
		{"null", nil, "wecom_contacts_invalid_response"},
		{"no search advertised", map[string]any{}, "wecom_contacts_unsupported"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := fixture(t, func(path, body string) string {
				if path == "/cli/service/discovery" {
					return envelope(t, map[string]any{"resources": tc.value})
				}
				return body
			})
			_, err := c.Search(context.Background(), "bot", "secret", "Alice")
			var de *service.Error
			if !errors.As(err, &de) || de.Code != tc.wantCode {
				t.Fatalf("expected %s, got %v", tc.wantCode, err)
			}
		})
	}
}
