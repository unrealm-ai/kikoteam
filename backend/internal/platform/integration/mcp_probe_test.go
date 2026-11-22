package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestMCPProbeHTTPSAndStream(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer test-only" {
					w.WriteHeader(401)
					return
				}
				var q struct {
					ID     int    `json:"id"`
					Method string `json:"method"`
				}
				if e := json.NewDecoder(r.Body).Decode(&q); e != nil {
					t.Error(e)
					return
				}
				var result any
				switch q.Method {
				case "initialize":
					w.Header().Set("Mcp-Session-Id", "session-test")
					result = map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{"tools": map[string]any{}}}
				case "notifications/initialized":
					w.WriteHeader(202)
					return
				case "tools/list":
					if r.Header.Get("Mcp-Session-Id") != "session-test" {
						t.Error("session missing")
					}
					result = map[string]any{"tools": []any{map[string]any{"name": "read", "description": "Read", "inputSchema": map[string]any{"type": "object"}}}}
				default:
					t.Error(q.Method)
					return
				}
				b, e := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": q.ID, "result": result})
				if e != nil {
					t.Error(e)
					return
				}
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					if _, e := fmt.Fprintf(w, "data: %s\n\n", b); e != nil {
						t.Error(e)
						return
					}
					w.(http.Flusher).Flush()
					<-r.Context().Done()
				} else {
					w.Header().Set("Content-Type", "application/json")
					if _, e := w.Write(b); e != nil {
						t.Error(e)
					}
				}
			}))
			defer ts.Close()
			p := &MCPProber{Client: ts.Client()}
			tools, e := p.Probe(context.Background(), service.Plugin{Name: "test", Endpoint: ts.URL, AuthType: "bearer"}, "test-only")
			if e != nil || len(tools) != 1 || tools[0].SchemaDigest == "" {
				t.Fatal(tools, e)
			}
		})
	}
}
func TestMCPDeniedAddresses(t *testing.T) {
	allow := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	for _, s := range []string{"127.0.0.1", "169.254.169.254", "::1", "0.0.0.0", "fe80::1", "224.0.0.1", "192.168.1.1", "100.100.100.200", "64:ff9b::a9fe:a9fe"} {
		if mcpAddressAllowed(net.ParseIP(s), allow) {
			t.Fatal(s)
		}
	}
	if !mcpAddressAllowed(net.ParseIP("10.1.1.1"), allow) {
		t.Fatal("allowlist ignored")
	}
}
func TestMCPAuthAndRedirect(t *testing.T) {
	for _, status := range []int{401, 302} {
		ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", "https://example.com")
			w.WriteHeader(status)
		}))
		p := &MCPProber{Client: ts.Client()}
		_, e := p.Probe(context.Background(), service.Plugin{Name: "test", Endpoint: ts.URL, AuthType: "none"}, "")
		ts.Close()
		if e == nil {
			t.Fatal(status)
		}
	}
}
func TestMCPResponseRejectsProtocolError(t *testing.T) {
	for _, body := range []string{`{"jsonrpc":"2.0","id":1,"error":{"message":"secret upstream"}}`, `{"jsonrpc":"2.0","id":2,"result":{}}`} {
		r := &http.Response{Header: http.Header{}, Body: http.NoBody}
		r.Body = ioNop(body)
		if _, e := readMCPResponse(r, 1); e == nil {
			t.Fatal(body)
		}
	}
}

type stringCloser struct{ *strings.Reader }

func (s stringCloser) Close() error { return nil }
func ioNop(s string) stringCloser   { return stringCloser{strings.NewReader(s)} }
