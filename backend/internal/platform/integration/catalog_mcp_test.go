package integration

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"io"
	"net/http"
	"strings"
	"testing"
)

type catalogTransport func(*http.Request) (*http.Response, error)

func (f catalogTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func fixtureResponse(text string) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(text))}
}
func TestHiThinkQuoteValidation(t *testing.T) {
	for _, tc := range []struct {
		text  string
		valid bool
	}{
		{`{"item":[{"thscode":"600519.SH","last_price":1277.8}]}`, true},
		{`{"code":2003,"message":"Missing X-api-key for 600519.SH"}`, false},
		{`{"item":[{"thscode":"600519.SH","last_price":null}]}`, false},
	} {
		if validHiThinkQuote(tc.text) != tc.valid {
			t.Fatal("unexpected quote verification")
		}
	}
}
func TestCatalogMCPAdapters(t *testing.T) {
	catalog, e := service.BuiltinCatalog()
	if e != nil {
		t.Fatal(e)
	}
	for _, resource := range catalog.Plugins {
		if resource.Auth != "api_key" && resource.Key != "tavily" {
			continue
		}
		t.Run(resource.Key, func(t *testing.T) {
			ctx := context.Background()
			plugin := resource.Definition()
			key := "fixture-key"
			if resource.Key == "feishu" {
				key = `{"app_id":"app","app_secret":"test"}`
			}
			if resource.Key == "dingtalk" {
				key = "https://mcp-gw.dingtalk.com/mserver/todo?key=fixture"
			}
			api := &MCPProber{Client: &http.Client{Transport: catalogTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "open.feishu.cn" {
					return fixtureResponse(`{"code":0,"tenant_access_token":"new-tenant-token"}`), nil
				}
				if r.Header.Get("X-Kiko-Connection") != "" {
					t.Fatal("internal credential header escaped")
				}
				switch resource.Key {
				case "tavily":
					if r.URL.String() != "https://mcp.tavily.com/mcp" || r.Header.Get("Authorization") != "Bearer "+key || plugin.AuthType != "bearer" || plugin.OAuth != nil {
						t.Fatal("expected the Tavily API key only in the MCP bearer header")
					}
				case "amap":
					if r.URL.Query().Get("key") != key {
						t.Fatal("missing query key")
					}
				case "dingtalk":
					if r.URL.String() != key {
						t.Fatal("signed target lost")
					}
				case "feishu":
					if r.Header.Get("X-Lark-MCP-TAT") != "new-tenant-token" || r.Header.Get("X-Lark-MCP-Allowed-Tools") != "search-doc,fetch-doc,list-docs" {
						t.Fatal("missing tenant authorization")
					}
				case "tencent_meeting":
					if r.Header.Get("X-Skill-Version") != "v1.0.15" || r.Header.Get("X-Tencent-Meeting-Token") != key {
						t.Fatal("missing version or token")
					}
				case "hithink":
					if r.Header.Get("X-api-key") != key {
						t.Fatal("missing API key")
					}
				}
				var req struct {
					ID     int
					Method string
				}
				if e := json.NewDecoder(r.Body).Decode(&req); e != nil {
					t.Fatal(e)
				}
				var result any = map[string]any{}
				switch req.Method {
				case "initialize":
					result = map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{"tools": map[string]any{}}}
				case "tools/list":
					result = map[string]any{"tools": []any{map[string]any{"name": resource.Tools[0], "inputSchema": map[string]any{"type": "object"}}}}
				case "tools/call":
					result = map[string]any{"content": []any{map[string]any{"type": "text", "text": `{"code":2003,"message":"Missing X-api-key"}`}}}
				}
				data, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
				if err != nil {
					t.Fatal(err)
				}
				return fixtureResponse(string(data)), nil
			})}}
			if tools, e := api.Probe(ctx, plugin, key); e != nil || len(tools) != 1 {
				t.Fatal("probe", e)
			}
			if _, e := api.CallTool(ctx, service.RuntimePlugin{PluginID: plugin.ID, Endpoint: plugin.Endpoint, Headers: connectionHeaders(plugin, key)}, resource.Tools[0], json.RawMessage("{}")); e != nil {
				t.Fatal(e)
			}
			if resource.Key == "hithink" && api.VerifyConnection(ctx, plugin, key) == nil {
				t.Fatal("business rejection accepted as a valid account")
			}
			if resource.Key == "dingtalk" {
				if _, e := api.Probe(ctx, plugin, "https://attacker.invalid/mserver/todo?key=x"); e == nil {
					t.Fatal("untrusted signed URL accepted")
				}
			}
		})
	}
}

func TestTavilyKeyVerification(t *testing.T) {
	plugin := service.Plugin{ID: service.CatalogID("plugin", "tavily"), Endpoint: "https://mcp.tavily.com/mcp", AuthType: "bearer"}
	for _, tc := range []struct {
		status int
		body   string
		code   string
	}{
		{200, `{"key":{"usage":0}}`, ""},
		{200, `{"detail":{"error":"invalid key"}}`, "mcp_protocol_error"},
		{401, "", "mcp_auth_failed"},
		{403, "", "mcp_auth_failed"},
		{429, "", "mcp_unavailable"},
	} {
		requests := 0
		api := &MCPProber{Client: &http.Client{Transport: catalogTransport(func(r *http.Request) (*http.Response, error) {
			requests++
			if r.Method != http.MethodGet || r.URL.String() != "https://api.tavily.com/usage" || r.Header.Get("Authorization") != "Bearer fixture-key" {
				t.Fatal("unexpected Tavily credential check")
			}
			res := fixtureResponse(tc.body)
			res.StatusCode = tc.status
			return res, nil
		})}}
		err := api.VerifyConnection(t.Context(), plugin, "fixture-key")
		var failure *service.Error
		if requests != 1 || (tc.code == "" && err != nil) || (tc.code != "" && (!errors.As(err, &failure) || failure.Code != tc.code)) {
			t.Errorf("Tavily usage returned %d, got %v", tc.status, err)
		}
	}
}
