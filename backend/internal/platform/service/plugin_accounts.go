package service

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

func (s *PluginService) pluginCipher() (cipher.AEAD, error) {
	if len(s.Secret) < 32 {
		return nil, errors.New("plugin credential encryption unavailable")
	}
	k := sha256.Sum256(append([]byte("kiko:plugin-credential:v1:"), s.Secret...))
	b, e := aes.NewCipher(k[:])
	if e != nil {
		return nil, e
	}
	return cipher.NewGCM(b)
}
func (s *PluginService) accountSecret(w string, p PluginAccount, secret *string) (string, error) {
	if secret != nil {
		return *secret, nil
	}
	if len(p.Credential) == 0 {
		return "", nil
	}
	a, e := s.pluginCipher()
	if e != nil {
		return "", e
	}
	if len(p.Credential) < a.NonceSize() {
		return "", errors.New("invalid plugin ciphertext")
	}
	v, e := a.Open(nil, p.Credential[:a.NonceSize()], p.Credential[a.NonceSize():], []byte(w+":"+p.PluginID+":"+p.ID+":v1"))
	if e != nil {
		return "", errors.New("plugin credential authentication failed")
	}
	return string(v), nil
}
func (s *PluginService) encryptAccount(w string, p PluginAccount, secret string) ([]byte, error) {
	a, e := s.pluginCipher()
	if e != nil {
		return nil, e
	}
	n := make([]byte, a.NonceSize())
	if _, e = rand.Read(n); e != nil {
		return nil, e
	}
	return a.Seal(n, n, []byte(secret), []byte(w+":"+p.PluginID+":"+p.ID+":v1")), nil
}
func (s *PluginService) GetAccount(ctx context.Context, w, a, employee, plugin string, versions ...string) (PluginAccount, error) {
	var out PluginAccount
	e := s.Store.WithWorkspace(ctx, w, func(t PluginTx) error {
		if e := pluginAuthorize(ctx, t, a, true); e != nil {
			return e
		}
		if e := connectionOwner(ctx, t, employee); e != nil {
			return e
		}
		var e error
		out, e = t.ManagedAccount(ctx, employee, plugin, firstVersion(versions))
		return e
	})
	return out, e
}
func (s *PluginService) probeAccount(ctx context.Context, p Plugin, secret string) ([]MCPTool, error) {
	if s.Probe == nil {
		return nil, Fail("mcp_unavailable", 503, "MCP 探测未配置")
	}
	tools, err := s.Probe.Probe(ctx, p, secret)
	if err != nil {
		return nil, err
	}
	if verifier, ok := s.Probe.(interface {
		VerifyConnection(context.Context, Plugin, string) error
	}); ok {
		err = verifier.VerifyConnection(ctx, p, secret)
	}
	return tools, err
}
func (s *PluginService) PutAccount(ctx context.Context, w, a, employee, plugin string, r PluginAccountPut) (PluginAccount, error) {
	if r.TenantOAuth != nil {
		return PluginAccount{}, Fail("invalid_plugin_credential", 400, "请从 OAuth 入口配置应用")
	}
	r.Name = strings.TrimSpace(r.Name)
	if r.Name == "" || utf8.RuneCountInString(r.Name) > 100 || !agentUUID(r.PluginVersionID) || r.Version < 0 {
		return PluginAccount{}, Fail("invalid_plugin_account", 400, "账号名称或版本无效")
	}
	if r.Secret != nil && (len(*r.Secret) > 8192 || strings.ContainsAny(*r.Secret, "\r\n\x00")) {
		return PluginAccount{}, Fail("invalid_plugin_credential", 400, "插件凭据无效")
	}
	var baseline staticConnectionBaseline
	e := s.Store.WithWorkspace(ctx, w, func(t PluginTx) error { return baseline.read(ctx, t, a, employee, plugin, r) })
	p, out := baseline.definition, baseline.account
	legacy, currentVersion, sourceRevision := baseline.legacy, baseline.currentVersion, baseline.sourceRevision
	if e != nil {
		return PluginAccount{}, e
	}
	if p.AuthType != "none" && r.Secret == nil && (out.Version == 0 || out.PluginVersionID != r.PluginVersionID || out.AuthMethod != p.AuthType) {
		return PluginAccount{}, Fail("plugin_secret_required", 400, "首次接入或切换定义版本需要重新填写凭据")
	}
	secret, e := s.accountSecret(w, out, r.Secret)
	if e != nil {
		return PluginAccount{}, e
	}
	if p.AuthType == "none" {
		secret = ""
	} else if secret == "" {
		return PluginAccount{}, Fail("plugin_secret_required", 400, "请填写账号凭据")
	}
	tools, e := s.probeAccount(ctx, p, secret)
	if e != nil {
		return PluginAccount{}, e
	}
	out.Credential, e = s.encryptAccount(w, out, secret)
	if e != nil {
		return PluginAccount{}, e
	}
	out.PluginVersionID = r.PluginVersionID
	out.AuthMethod = p.AuthType
	out.GrantedScopes, out.ExternalAccount, out.ExpiresAt = "", "", nil
	out.Source = "workspace"
	if employee != "" {
		out.Source = "agent"
	}
	out.Name = r.Name
	out.Version++
	out.Enabled = true
	out.Validated = true
	out.Tools = tools
	out.ValidatedAt = time.Now().UTC()
	e = s.Store.WithWorkspace(ctx, w, func(t PluginTx) error {
		if e := pluginAuthorize(ctx, t, a, true); e != nil {
			return e
		}
		if e := connectionOwner(ctx, t, employee); e != nil {
			return e
		}
		cur, e := t.Get(ctx, plugin)
		if e != nil {
			return e
		}
		if !cur.Enabled || cur.Version != currentVersion {
			return Fail("plugin_version_conflict", 409, "探测期间插件已更新，请重试")
		}
		if legacy != nil {
			cur, err := t.Account(ctx, employee, plugin, legacy.PluginVersionID)
			if err != nil {
				return err
			}
			if cur.Version != legacy.Version {
				return Fail("plugin_version_conflict", 409, "原账号已变更")
			}
		}
		if err := t.SaveAccount(ctx, out, r.Version == 0 || legacy != nil); err != nil {
			return err
		}
		if employee != "" {
			_, err := t.SetSource(ctx, employee, plugin, a, "agent", &sourceRevision)
			return err
		}
		// Return the current number of employees using this shared connection.
		// They may already be assigned when the connection is first created.
		var err error
		out, err = t.ManagedAccount(ctx, "", plugin, out.PluginVersionID)
		return err
	})
	return out, e
}
func (s *PluginService) RevokeAccount(ctx context.Context, w, a, employee, plugin string, version int64, versions ...string) error {
	var upstream OAuthTokens
	var materialErr error
	var def Plugin
	var key string
	err := s.Store.WithWorkspace(ctx, w, func(t PluginTx) error {
		if e := pluginAuthorize(ctx, t, a, true); e != nil {
			return e
		}
		p, e := t.Account(ctx, employee, plugin, firstVersion(versions))
		if e != nil {
			return e
		}
		if p.Version != version {
			return Fail("plugin_version_conflict", 409, "账号已更新，请刷新")
		}
		def, materialErr = t.GetVersion(ctx, plugin, p.PluginVersionID)
		if materialErr == nil {
			def, materialErr = def.forAuthMethod(p.AuthMethod)
		}
		if materialErr == nil && def.AuthType == "oauth" && len(p.Credential) > 0 {
			var app githubOAuthMaterial
			key, app, materialErr = s.clientKey(ctx, t, w, &def)
			if materialErr == nil {
				var raw string
				raw, materialErr = s.accountSecret(w, p, nil)
				if materialErr == nil {
					materialErr = json.Unmarshal([]byte(raw), &upstream)
					if materialErr == nil && !app.permitsGrant(upstream.GitHubAppPin) {
						materialErr = githubOAuthReconnect()
					}
				}
			}
		}
		p.Version++
		p.Enabled = false
		p.Validated = false
		p.Credential = []byte{}
		p.Tools = []MCPTool{}
		p.ExpiresAt = nil
		p.GrantedScopes = ""
		p.ExternalAccount = ""
		return t.SaveAccount(ctx, p, false)
	})
	if err == nil && materialErr != nil {
		return Fail("upstream_revocation_unconfirmed", 502, "本地连接已撤销，第三方撤销结果未确认")
	}
	if err != nil || upstream.Access == "" {
		return err
	}
	transport, e := s.oauthTransport()
	if e == nil {
		var m OAuthMetadata
		m, e = transport.Discover(ctx, def)
		if e == nil && m.Revocation != "" {
			for _, token := range []string{upstream.Refresh, upstream.Access} {
				if token != "" {
					if e = transport.Revoke(ctx, m, *def.OAuth, key, token); e != nil {
						break
					}
				}
			}
		}
	}
	if e != nil {
		return Fail("upstream_revocation_unconfirmed", 502, "本地连接已撤销，第三方撤销结果未确认")
	}
	return nil
}

type staticConnectionBaseline struct {
	definition                     Plugin
	account                        PluginAccount
	currentVersion, sourceRevision int64
	legacy                         *PluginAccount
}

func (b *staticConnectionBaseline) read(ctx context.Context, t PluginTx, a, employee, plugin string, r PluginAccountPut) error {
	if e := pluginAuthorize(ctx, t, a, true); e != nil {
		return e
	}
	if e := connectionOwner(ctx, t, employee); e != nil {
		return e
	}
	current, e := t.Get(ctx, plugin)
	if e != nil {
		return e
	}
	if !current.Enabled {
		return Fail("plugin_disabled", 409, "插件已停用")
	}
	if employee != "" {
		src, err := t.Source(ctx, employee, plugin)
		if err != nil {
			return err
		}
		b.sourceRevision = src.Revision
	}
	b.currentVersion = current.Version
	b.definition, e = t.GetVersion(ctx, plugin, r.PluginVersionID)
	if e != nil {
		return e
	}
	b.account, e = t.Account(ctx, employee, plugin, r.PluginVersionID)
	if errors.Is(e, ErrWorkspaceMissing) {
		b.account = PluginAccount{ID: NewID(), AgentID: employee, PluginID: plugin, Tools: []MCPTool{}}
		if employee != "" && r.Version > 0 {
			existing, err := t.Account(ctx, employee, plugin, "")
			if err != nil {
				return err
			}
			if existing.Version != r.Version {
				return Fail("plugin_version_conflict", 409, "连接已更新")
			}
			b.legacy = &existing
		}
	} else if e != nil {
		return e
	}
	if b.legacy == nil && b.account.Version != r.Version {
		return Fail("plugin_version_conflict", 409, "账号已更新，请刷新")
	}
	method := r.AuthMethod
	if method == "" {
		method = b.account.AuthMethod
		if method == "" {
			method = b.definition.DefaultAuthMethod
		}
	}
	if method == "oauth" {
		return Fail("oauth_required", 409, "请通过 OAuth 连接账号")
	}
	b.definition, e = b.definition.forAuthMethod(method)
	return e
}
