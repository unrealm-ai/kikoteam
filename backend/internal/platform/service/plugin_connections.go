package service

import (
	"context"
	"errors"
	"time"
)

type PluginOAuth struct {
	ReadTools []string `json:"read_tools,omitempty"`
	ID        string   `json:"id,omitempty"`
	Issuer    string   `json:"issuer"`
	ClientID  string   `json:"client_id"`
	Resource  string   `json:"resource"`
	Scopes    string   `json:"scopes"`
}
type OAuthClientInput struct {
	ReadTools    []string `json:"read_tools,omitempty"`
	Issuer       string   `json:"issuer"`
	ClientID     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret,omitempty"`
	Resource     string   `json:"resource"`
	Scopes       string   `json:"scopes"`
}
type ConnectionSource struct {
	Source   string         `json:"source"`
	Revision int64          `json:"revision"`
	Account  *PluginAccount `json:"account,omitempty"`
}
type OAuthAttempt struct {
	TenantClientID, GitHubAppPin                                                         string
	StateHash, Workspace, Actor, SessionHash, Agent, Plugin, PluginVersion, Name, Issuer string
	AccountVersion, SourceRevision                                                       int64
	Verifier                                                                             []byte
}
type ConnectionTx interface {
	ManagedAccount(context.Context, string, string, string) (PluginAccount, error)
	NoteDiscoveredTools(context.Context, RuntimeAccountPin, []MCPTool) error
	SessionTools(context.Context, string, string, string, []MCPTool) ([]MCPTool, error)
	SaveTenantOAuth(context.Context, Plugin) error
	Source(context.Context, string, string) (ConnectionSource, error)
	SetSource(context.Context, string, string, string, string, *int64) (ConnectionSource, error)
	SharedAccounts(context.Context) ([]PluginAccount, error)
	SaveAttempt(context.Context, OAuthAttempt) error
	TakeAttempt(context.Context, string, string, string) (OAuthAttempt, error)
	ClientSecret(context.Context, string) ([]byte, error)
	RefreshAccount(context.Context, PluginAccount) error
}

func connectionOwner(ctx context.Context, t PluginTx, agent string) error {
	if agent == "" {
		return nil
	}
	return t.AgentExists(ctx, agent)
}
func firstVersion(v []string) string {
	if len(v) > 0 {
		return v[0]
	}
	return ""
}
func (s *PluginService) SharedAccounts(ctx context.Context, w, actor string) ([]PluginAccount, error) {
	var out []PluginAccount
	err := s.Store.WithWorkspace(ctx, w, func(t PluginTx) error {
		if e := pluginAuthorize(ctx, t, actor, true); e != nil {
			return e
		}
		var e error
		out, e = t.SharedAccounts(ctx)
		return e
	})
	return out, err
}
func (s *PluginService) ConnectionSource(ctx context.Context, w, actor, agent, plugin, version, source string, revision *int64) (ConnectionSource, error) {
	var out ConnectionSource
	err := s.Store.WithWorkspace(ctx, w, func(t PluginTx) error {
		if e := AuthorizeEmployee(ctx, t, actor, agent); e != nil {
			return e
		}
		if e := t.AgentExists(ctx, agent); e != nil {
			return e
		}
		p, e := t.Get(ctx, plugin)
		if e != nil {
			return e
		}
		if version == "" {
			version = p.VersionID
		} else if _, e = t.GetVersion(ctx, plugin, version); e != nil {
			return e
		}
		if revision != nil {
			if source != "workspace" && source != "agent" {
				return Fail("invalid_source", 400, "账号来源无效")
			}
			out, e = t.SetSource(ctx, agent, plugin, actor, source, revision)
		} else {
			out, e = t.Source(ctx, agent, plugin)
		}
		if e != nil {
			return e
		}
		owner := ""
		if out.Source == "agent" {
			owner = agent
		}
		a, e := t.ManagedAccount(ctx, owner, plugin, version)
		if errors.Is(e, ErrWorkspaceMissing) {
			return nil
		}
		if e == nil {
			out.Account = &a
		}
		return e
	})
	return out, err
}

type OAuthTokens struct {
	GitHubAppPin    string `json:"kiko_github_app_pin,omitempty"`
	ExternalAccount string `json:"account_id,omitempty"`
	Access          string `json:"access_token"`
	Refresh         string `json:"refresh_token"`
	Scope           string `json:"scope"`
	Type            string `json:"token_type"`
	ExpiresIn       int64  `json:"expires_in"`
}
type OAuthMetadata struct {
	Issuer, Authorization, Token, Revocation string
	ResponseIssuer                           bool
	Methods                                  []string
}
type OAuthTransport interface {
	Discover(context.Context, Plugin) (OAuthMetadata, error)
	Exchange(context.Context, OAuthMetadata, PluginOAuth, string, map[string]string) (OAuthTokens, error)
	Revoke(context.Context, OAuthMetadata, PluginOAuth, string, string) error
}

func tokenExpiry(t OAuthTokens) *time.Time {
	if t.ExpiresIn <= 0 {
		return nil
	}
	v := time.Now().UTC().Add(time.Duration(t.ExpiresIn) * time.Second)
	return &v
}
func pinnedConnection(ctx context.Context, t PluginTx, agent string, pin RuntimeAccountPin) (PluginAccount, error) {
	src, e := t.Source(ctx, agent, pin.PluginID)
	if e != nil {
		return PluginAccount{}, e
	}
	want := pin.Source
	if want == "" {
		want = "agent"
	}
	if src.Source != want || src.Revision != pin.SourceRevision {
		return PluginAccount{}, Fail("plugin_authorization_changed", 403, "插件连接来源已变更")
	}
	owner := ""
	if want == "agent" {
		owner = agent
	}
	return t.Account(ctx, owner, pin.PluginID, pin.PluginVersionID)
}
