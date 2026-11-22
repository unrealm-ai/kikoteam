package integration

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func validOAuthURL(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.Fragment == "" && len(raw) <= 2048
}
func wellKnown(base, name string) string {
	u, e := url.Parse(base)
	if e != nil {
		return ""
	}
	u.Path = "/.well-known/" + name + strings.TrimSuffix(u.Path, "/")
	u.RawPath = ""
	u.RawQuery = ""
	return u.String()
}
func (p *MCPProber) metadata(ctx context.Context, target string, to any) error {
	if !validOAuthURL(target) {
		return oauthError()
	}
	req, e := http.NewRequestWithContext(ctx, "GET", target, nil)
	if e != nil {
		return oauthError()
	}
	c := p.client()
	defer c.CloseIdleConnections()
	r, e := c.Do(req)
	if e != nil {
		return oauthError()
	}
	defer r.Body.Close() //nolint:errcheck // Read-only response.
	if r.StatusCode != 200 {
		return oauthError()
	}
	return readOAuthJSON(r, to)
}
func readOAuthJSON(r *http.Response, to any) error {
	data, e := io.ReadAll(io.LimitReader(r.Body, 1<<20+1))
	if e != nil || len(data) > 1<<20 || json.Unmarshal(data, to) != nil {
		return oauthError()
	}
	return nil
}
func oauthError() error {
	return service.Fail("oauth_protocol_error", 502, "OAuth 服务未返回有效响应")
}
func (p *MCPProber) Discover(ctx context.Context, plugin service.Plugin) (service.OAuthMetadata, error) {
	var result service.OAuthMetadata
	o := plugin.OAuth
	if o == nil || !validOAuthURL(o.Resource) || !validOAuthURL(o.Issuer) {
		return result, oauthError()
	}
	uri := wellKnown(o.Resource, "oauth-protected-resource")
	var protected struct {
		Resource string
		Servers  []string `json:"authorization_servers"`
	}
	if e := p.metadata(ctx, uri, &protected); e != nil {
		// Servers may advertise a non-default metadata URI in the Bearer challenge.
		req, e := http.NewRequestWithContext(ctx, "POST", plugin.Endpoint, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","clientInfo":{"name":"kiko","version":"1"},"capabilities":{}}}`))
		if e != nil {
			return result, oauthError()
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		c := p.client()
		defer c.CloseIdleConnections()
		r, e := c.Do(req)
		if e != nil {
			return result, oauthError()
		}
		if e := r.Body.Close(); e != nil {
			return result, oauthError()
		}
		challenge := r.Header.Get("WWW-Authenticate")
		pos := strings.Index(challenge, `resource_metadata="`)
		if pos < 0 || r.StatusCode != 401 {
			return result, oauthError()
		}
		uri, _, ok := strings.Cut(challenge[pos+len(`resource_metadata="`):], `"`)
		if !ok || p.metadata(ctx, uri, &protected) != nil {
			return result, oauthError()
		}
	}
	if protected.Resource != o.Resource || !slices.Contains(protected.Servers, o.Issuer) {
		return result, oauthError()
	}
	var m struct {
		Issuer         string
		Auth           string   `json:"authorization_endpoint"`
		Token          string   `json:"token_endpoint"`
		Revoke         string   `json:"revocation_endpoint"`
		PKCE           []string `json:"code_challenge_methods_supported"`
		ResponseIssuer bool     `json:"authorization_response_iss_parameter_supported"`
		Methods        []string `json:"token_endpoint_auth_methods_supported"`
	}
	if e := p.metadata(ctx, wellKnown(o.Issuer, "oauth-authorization-server"), &m); e != nil {
		if e = p.metadata(ctx, strings.TrimRight(o.Issuer, "/")+"/.well-known/openid-configuration", &m); e != nil {
			return result, e
		}
	}
	if m.Issuer != o.Issuer || !validOAuthURL(m.Auth) || !validOAuthURL(m.Token) || !slices.Contains(m.PKCE, "S256") || m.Revoke != "" && !validOAuthURL(m.Revoke) {
		return result, oauthError()
	}
	// GitHub documents form credentials but omits the methods field in discovery.
	if m.Issuer == "https://github.com/login/oauth" && m.Token == "https://github.com/login/oauth/access_token" && len(m.Methods) == 0 {
		m.Methods = []string{"client_secret_post"}
	}
	return service.OAuthMetadata{Issuer: m.Issuer, Authorization: m.Auth, Token: m.Token, Revocation: m.Revoke, ResponseIssuer: m.ResponseIssuer, Methods: m.Methods}, nil
}
func (p *MCPProber) oauthPost(ctx context.Context, target string, m service.OAuthMetadata, o service.PluginOAuth, secret string, f url.Values) (*http.Response, *http.Client, error) {
	basic := secret != "" && (len(m.Methods) == 0 || slices.Contains(m.Methods, "client_secret_basic"))
	if !basic {
		f.Set("client_id", o.ClientID)
		if secret != "" {
			if !slices.Contains(m.Methods, "client_secret_post") {
				return nil, nil, oauthError()
			}
			f.Set("client_secret", secret)
		}
	}
	req, e := http.NewRequestWithContext(ctx, "POST", target, strings.NewReader(f.Encode()))
	if e != nil {
		return nil, nil, oauthError()
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if basic {
		req.SetBasicAuth(url.QueryEscape(o.ClientID), url.QueryEscape(secret))
	}
	c := p.client()
	r, e := c.Do(req)
	if e != nil {
		c.CloseIdleConnections()
		return nil, nil, oauthError()
	}
	return r, c, nil
}
func (p *MCPProber) Exchange(ctx context.Context, m service.OAuthMetadata, o service.PluginOAuth, secret string, params map[string]string) (service.OAuthTokens, error) {
	var out service.OAuthTokens
	f := url.Values{"resource": {o.Resource}}
	for k, v := range params {
		f.Set(k, v)
	}
	r, c, e := p.oauthPost(ctx, m.Token, m, o, secret, f)
	if e != nil {
		return out, e
	}
	defer c.CloseIdleConnections()
	defer r.Body.Close() //nolint:errcheck // Read-only response.
	var reply struct {
		service.OAuthTokens
		Error string `json:"error"`
	}
	if readOAuthJSON(r, &reply) != nil {
		return out, oauthError()
	}
	if reply.Error == "invalid_grant" || reply.Error == "bad_refresh_token" {
		return out, service.Fail("oauth_reconnect", 409, "需要重新连接 OAuth 账号")
	}
	if r.StatusCode != 200 || reply.Error != "" {
		return out, oauthError()
	}
	out = reply.OAuthTokens
	if len(out.ExternalAccount) > 255 || strings.ContainsAny(out.ExternalAccount, "\x00\r\n") || out.Access == "" || len(out.Access) > 16384 || strings.ContainsAny(out.Access, "\r\n\x00") || !strings.EqualFold(out.Type, "bearer") || out.ExpiresIn < 0 || out.ExpiresIn > 315360000 {
		return service.OAuthTokens{}, oauthError()
	}
	return out, nil
}
func (p *MCPProber) Revoke(ctx context.Context, m service.OAuthMetadata, o service.PluginOAuth, secret, token string) error {
	if m.Revocation == "" {
		return nil
	}
	r, c, e := p.oauthPost(ctx, m.Revocation, m, o, secret, url.Values{"token": {token}})
	if e != nil {
		return e
	}
	defer c.CloseIdleConnections()
	if e := r.Body.Close(); e != nil {
		return oauthError()
	}
	if r.StatusCode/100 != 2 {
		return oauthError()
	}
	return nil
}
