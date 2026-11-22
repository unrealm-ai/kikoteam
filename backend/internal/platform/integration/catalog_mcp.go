package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func connectionHeaders(p service.Plugin, secret string) map[string]string {
	switch p.AuthType {
	case "bearer", "oauth":
		return map[string]string{"Authorization": "Bearer " + secret}
	case "api_key":
		return map[string]string{p.HeaderName: secret}
	}
	return nil
}

// The snapshot endpoint can return an HTTP 200 MCP result with an embedded
// provider error. Confirm the requested instrument and an actual quote.
func validHiThinkQuote(text string) bool {
	type quote struct {
		Code string   `json:"thscode"`
		Last *float64 `json:"last_price"`
	}
	var result struct {
		Items []quote `json:"item"`
		Data  struct {
			Items []quote `json:"item"`
		} `json:"data"`
		Code *int `json:"code"`
	}
	if json.Unmarshal([]byte(text), &result) != nil || result.Code != nil && *result.Code != 0 && *result.Code != 200 {
		return false
	}
	for _, q := range append(result.Items, result.Data.Items...) {
		if q.Code == "600519.SH" && q.Last != nil && *q.Last > 0 {
			return true
		}
	}
	return false
}

func (p *MCPProber) conversation(ctx context.Context, client *http.Client, id, endpoint string, input map[string]string) (*mcpConversation, error) {
	c := &mcpConversation{endpoint: endpoint, client: client, headers: http.Header{}}
	for k, v := range input {
		c.headers.Set(k, v)
	}
	switch id {
	case service.CatalogID("plugin", "amap"):
		u, err := url.Parse(endpoint)
		if err != nil || u.Host != "mcp.amap.com" || u.Scheme != "https" {
			return nil, mcpProtocolError()
		}
		q := u.Query()
		q.Set("key", c.headers.Get("X-Kiko-Connection"))
		u.RawQuery = q.Encode()
		c.endpoint = u.String()
		c.headers.Del("X-Kiko-Connection")
	case service.CatalogID("plugin", "dingtalk"):
		u, err := url.Parse(c.headers.Get("X-Kiko-Connection"))
		if err != nil || u.Scheme != "https" || u.Host != "mcp-gw.dingtalk.com" || u.User != nil || u.Fragment != "" || !strings.HasPrefix(u.Path, "/mserver/") && !strings.HasPrefix(u.Path, "/server/") {
			return nil, service.Fail("invalid_plugin_credential", 422, "请填写从钉钉复制的完整待办 MCP 地址")
		}
		c.endpoint = u.String()
		c.headers.Del("X-Kiko-Connection")
	case service.CatalogID("plugin", "tencent_meeting"):
		if endpoint != "https://mcp.meeting.tencent.com/mcp/wemeet-open/v1" {
			return nil, mcpProtocolError()
		}
		c.headers.Set("X-Skill-Version", "v1.0.15")
	case service.CatalogID("plugin", "feishu"):
		if endpoint != "https://mcp.feishu.cn/mcp" {
			return nil, mcpProtocolError()
		}
		secret := c.headers.Get("X-Lark-MCP-TAT")
		token, err := tenantToken(ctx, client, secret)
		if err != nil {
			return nil, err
		}
		c.headers.Set("X-Lark-MCP-TAT", token)
		c.headers.Set("X-Lark-MCP-Allowed-Tools", "search-doc,fetch-doc,list-docs")
	}
	return c, nil
}

// Each MCP session obtains a current tenant token using the encrypted app
// credential. Sessions never share a cached token across connection owners.
func tenantToken(ctx context.Context, client *http.Client, secret string) (string, error) {
	var app struct {
		ID     string `json:"app_id"`
		Secret string `json:"app_secret"`
	}
	if json.Unmarshal([]byte(secret), &app) != nil || app.ID == "" || app.Secret == "" {
		return "", service.Fail("invalid_plugin_credential", 422, "需要飞书应用的 App ID 和 App Secret")
	}
	body, err := json.Marshal(app)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return "", service.Fail("mcp_unavailable", 502, "飞书令牌服务暂不可达")
	}
	defer res.Body.Close() //nolint:errcheck // The token response is read below.
	var reply struct {
		Code  int
		Token string `json:"tenant_access_token"`
	}
	if res.StatusCode != 200 || json.NewDecoder(io.LimitReader(res.Body, 32768)).Decode(&reply) != nil || reply.Code != 0 || reply.Token == "" {
		return "", service.Fail("mcp_auth_failed", 422, "飞书应用认证失败")
	}
	return reply.Token, nil
}

// Some providers serve tools/list anonymously. Verify the configured account
// with a bounded read only on an explicit connection test or save.
func (p *MCPProber) VerifyConnection(ctx context.Context, plugin service.Plugin, secret string) error {
	var tool, args string
	switch plugin.ID {
	case service.CatalogID("plugin", "tavily"):
		if plugin.AuthType == "bearer" && plugin.Endpoint == "https://mcp.tavily.com/mcp" {
			return p.verifyTavilyKey(ctx, secret)
		}
		return nil
	case service.CatalogID("plugin", "github"):
		tool, args = "get_me", "{}"
	case service.CatalogID("plugin", "zhipu"):
		tool, args = "webSearchPrime", `{"search_query":"智谱开放平台","count":1}`
	case service.CatalogID("plugin", "hithink"):
		tool, args = "get_a_share_prices_snapshot", `{"thscodes":"600519.SH"}`
	default:
		return nil
	}
	data, err := p.CallTool(ctx, service.RuntimePlugin{PluginID: plugin.ID, Endpoint: plugin.Endpoint, Headers: connectionHeaders(plugin, secret)}, tool, json.RawMessage(args))
	if err != nil {
		return err
	}
	var result struct {
		Error   bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	invalid := service.Fail("mcp_auth_failed", 422, "连接未能完成服务商的只读业务查询，请检查凭据和套餐权限")
	if json.Unmarshal(data, &result) != nil || result.Error || len(result.Content) == 0 || result.Content[0].Text == "" {
		return invalid
	}
	if plugin.ID == service.CatalogID("plugin", "hithink") && !validHiThinkQuote(result.Content[0].Text) {
		return invalid
	}
	return nil
}

// Tavily accepts an MCP handshake with an invalid key. The usage endpoint
// authenticates the key without running a search.
func (p *MCPProber) verifyTavilyKey(ctx context.Context, key string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.tavily.com/usage", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	client := p.client()
	defer client.CloseIdleConnections()
	res, err := client.Do(req)
	if err != nil {
		return service.Fail("mcp_unavailable", 502, "无法连接 Tavily 校验 API Key")
	}
	defer res.Body.Close() //nolint:errcheck // The response is read below.
	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
		return service.Fail("mcp_auth_failed", 422, "Tavily API Key 无效或没有访问权限")
	}
	if res.StatusCode != http.StatusOK {
		return service.Fail("mcp_unavailable", 502, "Tavily 暂时无法校验 API Key")
	}
	var usage struct {
		Key struct{ Usage *int }
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&usage); err != nil || usage.Key.Usage == nil {
		return mcpProtocolError()
	}
	return nil
}
