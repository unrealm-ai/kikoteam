package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

var crmToolName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)
var tenantCRMPath = regexp.MustCompile(`^/mcp/[A-Za-z0-9_-]+/[A-Za-z0-9_-]+/?$`)

func oauthURI(s string) bool {
	u, e := url.Parse(s)
	return e == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && len(s) <= 2048
}

// The resource can identify the service root or a tenant-specific parent of
// the MCP endpoint. Tokens must never be sent to another origin or tenant.
func oauthResourceMatches(resource, endpoint string) bool {
	if !oauthURI(resource) || !oauthURI(endpoint) {
		return false
	}
	r, err := url.Parse(resource)
	if err != nil {
		return false
	}
	p, err := url.Parse(endpoint)
	if err != nil || !strings.EqualFold(r.Host, p.Host) {
		return false
	}
	base := strings.TrimRight(r.EscapedPath(), "/")
	target := strings.TrimRight(p.EscapedPath(), "/")
	return target == base || strings.HasPrefix(target, base+"/")
}
func (s *PluginService) ConfigureOAuth(w string, p *Plugin, in *OAuthClientInput) error {
	if p.AuthType != "oauth" {
		p.OAuth = nil
		p.ClientCredential = nil
		if in != nil {
			return Fail("invalid_oauth_client", 400, "当前认证方式不接受 OAuth 应用")
		}
		return nil
	}
	if in == nil {
		if p.OAuth == nil || !oauthResourceMatches(p.OAuth.Resource, p.Endpoint) || p.OAuth.ClientID == "" {
			return Fail("oauth_client_required", 400, "请配置目标服务的 OAuth 应用")
		}
		return nil
	}
	resource := in.Resource
	if resource == "" {
		resource = p.Endpoint
	}
	if !oauthURI(in.Issuer) || !oauthResourceMatches(resource, p.Endpoint) || in.ClientID == "" || len(in.ClientID) > 1024 || len(in.ClientSecret) > 8192 || len(in.Scopes) > 2048 || strings.ContainsAny(in.ClientID+in.Scopes, "\x00\r\n") {
		return Fail("invalid_oauth_client", 400, "OAuth 应用、权限或目标资源无效")
	}
	c := &PluginOAuth{ID: NewID(), Issuer: in.Issuer, ClientID: in.ClientID, Resource: resource, Scopes: strings.Join(strings.Fields(in.Scopes), " ")}
	sealed, e := s.encryptAccount(w, PluginAccount{ID: c.ID, PluginID: "oauth-client"}, in.ClientSecret)
	if e != nil {
		return e
	}
	p.OAuth = c
	p.ClientCredential = sealed
	return nil
}

// Deployment credentials are scoped to the immutable official GitHub resource and issuer.
func (s *PluginService) bindOAuthClient(ctx context.Context, t PluginTx, p *Plugin) (githubOAuthMaterial, error) {
	if p.isCatalogGitHubOAuth() {
		if !p.officialGitHub() || p.OAuth == nil || p.OAuth.Resource != p.Endpoint || p.OAuth.Issuer != githubIssuer {
			return githubOAuthMaterial{}, Fail("invalid_oauth_client", 409, "GitHub 插件的授权目标无效")
		}
		app, err := s.gitHubOAuthMaterial(ctx, t)
		if err != nil {
			return app, err
		}
		client := *p.OAuth
		client.ClientID = app.clientID
		p.OAuth = &client
		return app, nil
	}
	if p.OAuth == nil || p.OAuth.ID == "" {
		return githubOAuthMaterial{}, Fail("oauth_client_required", 409, "插件缺少 OAuth 应用")
	}
	return githubOAuthMaterial{}, nil
}

func (p Plugin) isCatalogGitHubOAuth() bool {
	return p.Scope == "official" && p.ID == CatalogID("plugin", "github") && p.AuthType == "oauth"
}

func (s *PluginService) clientKey(ctx context.Context, t PluginTx, w string, p *Plugin) (string, githubOAuthMaterial, error) {
	if app, err := s.bindOAuthClient(ctx, t, p); app.secret != "" || err != nil {
		return app.secret, app, err
	}
	c, err := t.ClientSecret(ctx, p.OAuth.ID)
	if err != nil {
		return "", githubOAuthMaterial{}, err
	}
	if p.Scope == "official" && p.ID != CatalogID("plugin", "fxiaoke") {
		w = ""
	}
	key, err := s.accountSecret(w, PluginAccount{ID: p.OAuth.ID, PluginID: "oauth-client", Credential: c}, nil)
	return key, githubOAuthMaterial{}, err
}
func randomOAuth() string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}
func challenge(v string) string {
	sum := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
func (s *PluginService) oauthTransport() (OAuthTransport, error) {
	v, ok := s.Probe.(OAuthTransport)
	if !ok || s.RedirectURI == "" {
		return nil, Fail("oauth_unavailable", 503, "OAuth 接入尚未配置")
	}
	return v, nil
}
func (s *PluginService) checkAttempt(ctx context.Context, t PluginTx, a *OAuthAttempt) (Plugin, PluginAccount, error) {
	var out PluginAccount
	if e := pluginAuthorize(ctx, t, a.Actor, true); e != nil {
		return Plugin{}, out, e
	}
	if a.Agent != "" {
		if e := t.AgentExists(ctx, a.Agent); e != nil {
			return Plugin{}, out, e
		}
		src, e := t.Source(ctx, a.Agent, a.Plugin)
		if e != nil {
			return Plugin{}, out, e
		}
		if a.SourceRevision < 0 {
			a.SourceRevision = src.Revision
		} else if src.Revision != a.SourceRevision {
			return Plugin{}, out, Fail("connection_source_conflict", 409, "授权期间账号来源已变更")
		}
	}
	p, e := t.GetVersion(ctx, a.Plugin, a.PluginVersion)
	if e != nil {
		return p, out, e
	}
	p, e = p.forAuthMethod("oauth")
	if e != nil || !p.Enabled {
		return p, out, Fail("oauth_unavailable", 409, "该版本不支持 OAuth 授权")
	}
	app, err := s.bindOAuthClient(ctx, t, &p)
	if err != nil {
		return p, out, err
	}
	if app.attemptPin != "" {
		if a.GitHubAppPin != app.attemptPin && (a.GitHubAppPin != "" || a.Issuer != "") {
			return p, out, githubOAuthAppChanged()
		}
		a.GitHubAppPin = app.attemptPin
	}
	if p.ID == CatalogID("plugin", "fxiaoke") {
		if a.TenantClientID != "" && a.TenantClientID != p.OAuth.ID {
			return p, out, Fail("oauth_client_changed", 409, "授权期间企业 OAuth 应用已更新")
		}
		a.TenantClientID = p.OAuth.ID
	}
	out, e = t.Account(ctx, a.Agent, a.Plugin, a.PluginVersion)
	if errors.Is(e, ErrWorkspaceMissing) {
		out = PluginAccount{ID: NewID(), AgentID: a.Agent, PluginID: a.Plugin, PluginVersionID: a.PluginVersion}
		e = nil
	}
	if e == nil && out.Version != a.AccountVersion {
		e = Fail("plugin_version_conflict", 409, "授权期间连接已变更")
	}
	return p, out, e
}
func (s *PluginService) AuthorizeOAuth(ctx context.Context, w, actor, session, agent, plugin string, r PluginAccountPut) (string, error) {
	transport, e := s.oauthTransport()
	if e != nil {
		return "", e
	}
	r.Name = strings.TrimSpace(r.Name)
	if session == "" || !agentUUID(r.PluginVersionID) || r.Version < 0 || r.Name == "" || utf8.RuneCountInString(r.Name) > 100 || r.Secret != nil || (r.AuthMethod != "" && r.AuthMethod != "oauth") {
		return "", Fail("invalid_oauth_request", 400, "授权参数无效")
	}
	state := w + "." + randomOAuth()
	verifier := randomOAuth()
	a := OAuthAttempt{Workspace: w, Actor: actor, SessionHash: HashToken(session), Agent: agent, Plugin: plugin, PluginVersion: r.PluginVersionID, Name: r.Name, StateHash: HashToken(state), AccountVersion: r.Version, SourceRevision: -1}
	var p Plugin
	e = s.Store.WithWorkspace(ctx, w, func(t PluginTx) error {
		if r.TenantOAuth != nil {
			if err := pluginAuthorize(ctx, t, actor, true); err != nil {
				return err
			}
			if plugin != CatalogID("plugin", "fxiaoke") {
				return Fail("invalid_oauth_client", 400, "该插件不支持空间专属 OAuth 应用")
			}
			tenant, err := t.GetVersion(ctx, plugin, r.PluginVersionID)
			if err != nil {
				return err
			}
			u, err := url.Parse(r.TenantOAuth.Resource)
			if err != nil || !oauthURI(r.TenantOAuth.Resource) || u.Host != "open.fxiaoke.com" || !tenantCRMPath.MatchString(u.EscapedPath()) {
				return Fail("invalid_oauth_client", 400, "请输入纷享销客的企业 MCP 服务地址")
			}
			if len(r.TenantOAuth.ReadTools) == 0 || len(r.TenantOAuth.ReadTools) > 32 {
				return Fail("invalid_oauth_client", 400, "请指定企业 MCP 中允许读取 CRM 数据的工具名称")
			}
			for _, tool := range r.TenantOAuth.ReadTools {
				if !crmToolName.MatchString(tool) {
					return Fail("invalid_oauth_client", 400, "CRM 只读工具名称无效")
				}
			}
			tenant.Endpoint = r.TenantOAuth.Resource
			if err = s.ConfigureOAuth(w, &tenant, r.TenantOAuth); err != nil {
				return err
			}
			tenant.OAuth.ReadTools = r.TenantOAuth.ReadTools
			if err = t.SaveTenantOAuth(ctx, tenant); err != nil {
				return err
			}
		}
		var err error
		p, _, err = s.checkAttempt(ctx, t, &a)
		return err
	})
	if e != nil {
		return "", e
	}
	m, e := transport.Discover(ctx, p)
	if e != nil {
		return "", e
	}
	a.Issuer = m.Issuer
	a.Verifier, e = s.encryptAccount(w, PluginAccount{ID: a.StateHash, PluginID: "oauth-verifier"}, verifier)
	if e != nil {
		return "", e
	}
	e = s.Store.WithWorkspace(ctx, w, func(t PluginTx) error {
		current, _, err := s.checkAttempt(ctx, t, &a)
		if err != nil {
			return err
		}
		if current.OAuth.ID != p.OAuth.ID {
			return Fail("oauth_client_changed", 409, "OAuth 应用已更新，请重新授权")
		}
		return t.SaveAttempt(ctx, a)
	})
	if e != nil {
		return "", e
	}
	u, parseErr := url.Parse(m.Authorization)
	if parseErr != nil {
		return "", parseErr
	}
	q := u.Query()
	for k, v := range map[string]string{"client_id": p.OAuth.ClientID, "response_type": "code", "redirect_uri": s.RedirectURI, "state": state, "code_challenge": challenge(verifier), "code_challenge_method": "S256", "resource": p.OAuth.Resource} {
		q.Set(k, v)
	}
	if p.OAuth.Scopes != "" {
		q.Set("scope", p.OAuth.Scopes)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}
func scopeSubset(actual, allowed string) bool {
	list := strings.Fields(allowed)
	for _, v := range strings.Fields(actual) {
		if !slices.Contains(list, v) {
			return false
		}
	}
	return true
}
func (s *PluginService) CompleteOAuth(ctx context.Context, actor, session, state, code, issuer string) (PluginAccount, error) {
	var out PluginAccount
	w, _, ok := strings.Cut(state, ".")
	if !ok || !agentUUID(w) || len(state) > 180 || len(code) > 8192 || session == "" {
		return out, Fail("invalid_oauth_state", 400, "授权请求无效或已过期")
	}
	var a OAuthAttempt
	var p Plugin
	var key string
	var app githubOAuthMaterial
	transport, e := s.oauthTransport()
	if e != nil {
		return out, e
	}
	e = s.Store.WithWorkspace(ctx, w, func(t PluginTx) error {
		var err error
		a, err = t.TakeAttempt(ctx, HashToken(state), actor, HashToken(session))
		if err != nil {
			return err
		}
		p, out, err = s.checkAttempt(ctx, t, &a)
		if err != nil {
			return err
		}
		key, app, err = s.clientKey(ctx, t, w, &p)
		return err
	})
	if e != nil {
		return out, e
	}
	if code == "" {
		return out, Fail("oauth_cancelled", 400, "第三方授权已取消")
	}
	m, e := transport.Discover(ctx, p)
	if e != nil {
		return out, e
	}
	if a.Issuer != m.Issuer || (issuer != "" || m.ResponseIssuer) && issuer != a.Issuer {
		return out, Fail("invalid_oauth_issuer", 400, "授权来源不匹配")
	}
	verifier, e := s.accountSecret(w, PluginAccount{ID: a.StateHash, PluginID: "oauth-verifier", Credential: a.Verifier}, nil)
	if e != nil {
		return out, e
	}
	tokens, e := transport.Exchange(ctx, m, *p.OAuth, key, map[string]string{"grant_type": "authorization_code", "code": code, "code_verifier": verifier, "redirect_uri": s.RedirectURI})
	if e != nil {
		return out, e
	}
	tokens.GitHubAppPin = app.grantPin
	if tokens.Scope == "" {
		tokens.Scope = p.OAuth.Scopes
	}
	if p.OAuth.Scopes != "" && !scopeSubset(tokens.Scope, p.OAuth.Scopes) {
		return out, Fail("oauth_scope_changed", 409, "服务商授予了未经批准的权限")
	}
	tools, e := s.probeAccount(ctx, p, tokens.Access)
	if e != nil {
		return out, e
	}
	e = s.Store.WithWorkspace(ctx, w, func(t PluginTx) error {
		var err error
		_, out, err = s.checkAttempt(ctx, t, &a)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(tokens)
		if err != nil {
			return err
		}
		out.Credential, err = s.encryptAccount(w, out, string(raw))
		if err != nil {
			return err
		}
		out.Source = "workspace"
		if a.Agent != "" {
			out.Source = "agent"
		}
		out.Name = a.Name
		out.AuthMethod = "oauth"
		out.Version++
		out.Enabled = true
		out.Validated = true
		out.Tools = tools
		out.ValidatedAt = time.Now().UTC()
		out.GrantedScopes = tokens.Scope
		out.ExternalAccount = tokens.ExternalAccount
		out.ExpiresAt = tokenExpiry(tokens)
		if err = t.SaveAccount(ctx, out, a.AccountVersion == 0); err != nil {
			return err
		}
		if a.Agent != "" {
			_, err = t.SetSource(ctx, a.Agent, a.Plugin, actor, "agent", &a.SourceRevision)
		}
		return err
	})
	return out, e
}
func (s *PluginService) accessToken(ctx context.Context, t PluginTx, w string, p Plugin, a PluginAccount) (string, error) {
	raw, e := s.accountSecret(w, a, nil)
	if e != nil || p.AuthType != "oauth" {
		return raw, e
	}
	var tokens OAuthTokens
	if json.Unmarshal([]byte(raw), &tokens) != nil || tokens.Access == "" {
		return "", Fail("oauth_reconnect", 409, "需要重新连接 OAuth 账号")
	}
	if a.ExpiresAt == nil || time.Until(*a.ExpiresAt) > 10*time.Second {
		return tokens.Access, nil
	}
	if tokens.Refresh == "" {
		return "", Fail("oauth_reconnect", 409, "OAuth 账号已过期，请重新连接")
	}
	transport, e := s.oauthTransport()
	if e != nil {
		return "", e
	}
	key, app, e := s.clientKey(ctx, t, w, &p)
	if e != nil {
		return "", e
	}
	if !app.permitsGrant(tokens.GitHubAppPin) {
		return "", githubOAuthReconnect()
	}
	m, e := transport.Discover(ctx, p)
	if e != nil {
		return "", e
	}
	fresh, e := transport.Exchange(ctx, m, *p.OAuth, key, map[string]string{"grant_type": "refresh_token", "refresh_token": tokens.Refresh})
	if e != nil {
		return "", e
	}
	if fresh.ExternalAccount != "" && a.ExternalAccount != "" && fresh.ExternalAccount != a.ExternalAccount {
		return "", Fail("oauth_reconnect", 409, "业务账号身份已变更，请重新连接")
	}
	if fresh.Scope == "" {
		fresh.Scope = tokens.Scope
	}
	if !scopeSubset(fresh.Scope, tokens.Scope) || !scopeSubset(tokens.Scope, fresh.Scope) {
		return "", Fail("oauth_reconnect", 409, "授权范围已变更，请重新连接")
	}
	fresh.GitHubAppPin = app.grantPin
	if fresh.Refresh == "" {
		fresh.Refresh = tokens.Refresh
	}
	data, e := json.Marshal(fresh)
	if e != nil {
		return "", e
	}
	a.Credential, e = s.encryptAccount(w, a, string(data))
	if e != nil {
		return "", e
	}
	a.ExpiresAt = tokenExpiry(fresh)
	if e = t.RefreshAccount(ctx, a); e != nil {
		return "", e
	}
	return fresh.Access, nil
}
