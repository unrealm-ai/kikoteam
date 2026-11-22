package integration

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

// MCPProber performs only initialize and tools/list, never tools/call.
// Private CIDRs are an operator-controlled allowlist, not client input.
type MCPProber struct {
	PrivateCIDRs []netip.Prefix
	Client       *http.Client
}

func mcpAddressAllowed(ip net.IP, allow []netip.Prefix) bool {
	if ip == nil || ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return false
	}
	a, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	a = a.Unmap()
	// Deny shared address space and transition ranges which can reach metadata/local hosts.
	for _, cidr := range []string{"100.64.0.0/10", "0.0.0.0/8", "192.0.0.0/24", "198.18.0.0/15", "240.0.0.0/4", "64:ff9b::/96", "2002::/16"} {
		if netip.MustParsePrefix(cidr).Contains(a) {
			return false
		}
	}
	if !ip.IsPrivate() {
		return ip.IsGlobalUnicast()
	}
	for _, p := range allow {
		if p.Contains(a) {
			return true
		}
	}
	return false
}
func (p *MCPProber) client() *http.Client {
	if p.Client != nil {
		c := *p.Client
		c.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("MCP redirects disabled") }
		return &c
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	tr := &http.Transport{TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 10 * time.Second}
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(address)
		if e != nil {
			return nil, e
		}
		ips, e := net.DefaultResolver.LookupIPAddr(ctx, host)
		if e != nil || len(ips) == 0 {
			return nil, errors.New("MCP DNS resolution failed")
		}
		for _, ip := range ips {
			if !mcpAddressAllowed(ip.IP, p.PrivateCIDRs) {
				return nil, errors.New("MCP target denied")
			}
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}
	return &http.Client{Transport: tr, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("MCP redirects disabled") }}
}

type mcpWireTool struct {
	Annotations struct {
		Destructive bool `json:"destructiveHint"`
	} `json:"annotations"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}
type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   json.RawMessage `json:"error"`
}
type mcpConversation struct {
	client                      *http.Client
	endpoint, session, protocol string
	headers                     http.Header
	next                        int
}

func (c *mcpConversation) call(ctx context.Context, method string, params any, notify bool) (json.RawMessage, error) {
	c.next++
	payload := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		payload["params"] = params
	}
	if !notify {
		payload["id"] = c.next
	}
	data, e := json.Marshal(payload)
	if e != nil {
		return nil, e
	}
	req, e := http.NewRequestWithContext(ctx, "POST", c.endpoint, bytes.NewReader(data))
	if e != nil {
		return nil, e
	}
	req.Header.Set("User-Agent", "KikoTeam/1.0")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for key := range c.headers {
		req.Header.Set(key, c.headers.Get(key))
	}
	if c.session != "" {
		req.Header.Set("Mcp-Session-Id", c.session)
	}
	if c.protocol != "" {
		req.Header.Set("MCP-Protocol-Version", c.protocol)
	}
	res, e := c.client.Do(req)
	if e != nil {
		return nil, service.Fail("mcp_unavailable", 502, "MCP 连接失败、超时或目标被策略拒绝")
	}
	defer res.Body.Close() //nolint:errcheck // Read/transport failures are reported separately.
	if res.StatusCode == 401 || res.StatusCode == 403 {
		return nil, service.Fail("mcp_auth_failed", 422, "MCP 账号认证失败或没有权限")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, service.Fail("mcp_unavailable", 502, "MCP 服务返回非成功状态")
	}
	if notify {
		return nil, nil
	}
	if method == "initialize" {
		c.session = res.Header.Get("Mcp-Session-Id")
		if len(c.session) > 1024 || strings.ContainsAny(c.session, "\r\n") {
			return nil, mcpProtocolError()
		}
	}
	return readMCPResponse(res, c.next)
}

func decodeMCPResponse(data []byte, id int) (json.RawMessage, bool, error) {
	var msg mcpResponse
	if json.Unmarshal(data, &msg) != nil {
		return nil, false, mcpProtocolError()
	}
	if string(msg.ID) != fmt.Sprint(id) {
		return nil, false, nil
	}
	if msg.JSONRPC != "2.0" || len(msg.Error) > 0 && string(msg.Error) != "null" || len(msg.Result) == 0 {
		return nil, true, mcpProtocolError()
	}
	return msg.Result, true, nil
}
func readMCPResponse(res *http.Response, id int) (json.RawMessage, error) {
	const limit = 2 * 1024 * 1024
	reader := io.LimitReader(res.Body, limit+1)
	if !strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
		body, e := io.ReadAll(reader)
		if e != nil || len(body) > limit {
			return nil, mcpProtocolError()
		}
		v, found, e := decodeMCPResponse(body, id)
		if !found && e == nil {
			e = mcpProtocolError()
		}
		return v, e
	}
	scan := bufio.NewScanner(reader)
	scan.Buffer(make([]byte, 4096), limit)
	var event []byte
	total := 0
	for scan.Scan() {
		line := scan.Text()
		total += len(line) + 1
		if total > limit {
			return nil, mcpProtocolError()
		}
		if line == "" {
			if len(event) > 0 {
				v, found, e := decodeMCPResponse(event, id)
				if e != nil {
					return nil, e
				}
				if found {
					return v, nil
				}
				event = nil
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			event = append(event, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " ")...)
			event = append(event, '\n')
		}
	}
	if scan.Err() != nil {
		return nil, mcpProtocolError()
	}
	if len(event) > 0 {
		v, found, e := decodeMCPResponse(event, id)
		if e != nil {
			return nil, e
		}
		if found {
			return v, nil
		}
	}
	return nil, mcpProtocolError()
}

func mcpProtocolError() error {
	return service.Fail("mcp_protocol_error", 502, "MCP 协议响应无效或超过限制")
}
func (p *MCPProber) Probe(ctx context.Context, plugin service.Plugin, secret string) ([]service.MCPTool, error) {
	if e := service.ValidatePlugin(&plugin); e != nil {
		return nil, e
	}
	u, parseErr := url.Parse(plugin.Endpoint)
	if parseErr != nil {
		return nil, mcpProtocolError()
	}
	if u.Hostname() == "localhost" {
		return nil, service.Fail("invalid_plugin_endpoint", 400, "不允许本机 MCP 地址")
	}
	ctx, cancel := context.WithTimeout(ctx, 18*time.Second)
	defer cancel()
	client := p.client()
	defer client.CloseIdleConnections()
	c, e := p.conversation(ctx, client, plugin.ID, plugin.Endpoint, connectionHeaders(plugin, secret))
	if e != nil {
		return nil, e
	}
	hasTools, e := c.initialize(ctx)
	if e != nil {
		return nil, e
	}
	if !hasTools {
		return []service.MCPTool{}, nil
	}
	tools := []service.MCPTool{}
	names := map[string]bool{}
	cursors := map[string]bool{}
	cursor := ""
	for page := 0; page < 20; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, e := c.call(ctx, "tools/list", params, false)
		if e != nil {
			return nil, e
		}
		var list struct {
			Tools []mcpWireTool `json:"tools"`
			Next  string        `json:"nextCursor"`
		}
		if json.Unmarshal(raw, &list) != nil {
			return nil, mcpProtocolError()
		}
		for _, t := range list.Tools {
			if t.Name == "" || len(t.Name) > 256 || len(t.Description) > 16000 || names[t.Name] || len(tools) >= 500 {
				return nil, mcpProtocolError()
			}
			if plugin.ID == service.CatalogID("plugin", "fxiaoke") && (plugin.OAuth == nil || !slices.Contains(plugin.OAuth.ReadTools, t.Name) || t.Annotations.Destructive) {
				continue
			}
			var schema map[string]any
			if json.Unmarshal(t.InputSchema, &schema) != nil || schema["type"] != "object" {
				return nil, mcpProtocolError()
			}
			canonical, e := json.Marshal(schema)
			if e != nil {
				return nil, mcpProtocolError()
			}
			sum := sha256.Sum256(canonical)
			tools = append(tools, service.MCPTool{Name: t.Name, Description: t.Description, InputSchema: canonical, SchemaDigest: hex.EncodeToString(sum[:])})
			names[t.Name] = true
		}
		if list.Next == "" {
			return tools, nil
		}
		if cursors[list.Next] || len(list.Next) > 4096 {
			return nil, mcpProtocolError()
		}
		cursors[list.Next] = true
		cursor = list.Next
	}
	return nil, mcpProtocolError()
}
func (c *mcpConversation) initialize(ctx context.Context) (bool, error) {
	raw, e := c.call(ctx, "initialize", map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "kiko", "version": "1"}}, false)
	if e != nil {
		return false, e
	}
	var init struct {
		ProtocolVersion string                     `json:"protocolVersion"`
		Capabilities    map[string]json.RawMessage `json:"capabilities"`
	}
	if json.Unmarshal(raw, &init) != nil {
		return false, mcpProtocolError()
	}
	switch init.ProtocolVersion {
	case "2024-11-05", "2025-03-26", "2025-06-18":
	default:
		return false, mcpProtocolError()
	}
	c.protocol = init.ProtocolVersion
	if _, e = c.call(ctx, "notifications/initialized", nil, true); e != nil {
		return false, e
	}
	_, ok := init.Capabilities["tools"]
	return ok, nil
}
func (p *MCPProber) CallTool(ctx context.Context, plugin service.RuntimePlugin, name string, args json.RawMessage) (json.RawMessage, error) {
	client := p.client()
	defer client.CloseIdleConnections()
	c, e := p.conversation(ctx, client, plugin.PluginID, plugin.Endpoint, plugin.Headers)
	if e != nil {
		return nil, e
	}
	tools, e := c.initialize(ctx)
	if e != nil {
		return nil, e
	}
	if !tools {
		return nil, mcpProtocolError()
	}
	return c.call(ctx, "tools/call", map[string]any{"name": name, "arguments": args}, false)
}
