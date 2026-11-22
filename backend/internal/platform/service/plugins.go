package service

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"
)

func pluginAuthorize(ctx context.Context, t PluginTx, a string, write bool) error {
	m, e := t.Member(ctx, a)
	if errors.Is(e, ErrWorkspaceMissing) || e == nil && (m.Status != "active" || write && m.Role != "owner" && m.Role != "admin") {
		return Fail("workspace_forbidden", 403, "没有插件管理权限")
	}
	return e
}
func ValidatePlugin(p *Plugin) error {
	p.Name = strings.TrimSpace(p.Name)
	p.Description = strings.TrimSpace(p.Description)
	p.Endpoint = strings.TrimSpace(p.Endpoint)
	if p.Name == "" || utf8.RuneCountInString(p.Name) > 100 || utf8.RuneCountInString(p.Description) > 2000 || strings.ContainsRune(p.Name+p.Description, '\x00') {
		return Fail("invalid_plugin", 400, "插件名称或说明无效")
	}
	u, e := url.Parse(p.Endpoint)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(p.Endpoint) > 2048 {
		return Fail("invalid_plugin_endpoint", 400, "MCP 地址必须为不含账号、查询参数的 HTTPS 地址")
	}
	if p.AuthType != "none" && p.AuthType != "bearer" && p.AuthType != "api_key" && p.AuthType != "oauth" {
		return Fail("invalid_plugin_auth", 400, "不支持的认证方式")
	}
	if p.AuthType != "api_key" {
		p.HeaderName = ""
		return nil
	}
	p.HeaderName = http.CanonicalHeaderKey(strings.TrimSpace(p.HeaderName))
	lower := strings.ToLower(p.HeaderName)
	if len(lower) == 0 || len(lower) > 100 || strings.HasPrefix(lower, "proxy-") || strings.HasPrefix(lower, "sec-") || strings.HasPrefix(lower, "mcp-") {
		return Fail("invalid_plugin_header", 400, "认证 Header 无效")
	}
	for _, c := range lower {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return Fail("invalid_plugin_header", 400, "认证 Header 无效")
		}
	}
	switch lower {
	case "host", "cookie", "authorization", "content-length", "content-type", "connection", "transfer-encoding", "accept", "origin", "referer", "upgrade", "te", "trailer":
		return Fail("invalid_plugin_header", 400, "认证 Header 不允许覆盖协议字段")
	}
	return nil
}
func (s *PluginService) List(ctx context.Context, w, a string, p, n int, filter ResourceListFilter) (PluginPage, error) {
	out := PluginPage{Items: []Plugin{}, Page: p, PageSize: n}
	if err := filter.normalize(); err != nil {
		return out, err
	}
	if e := pageValid(p, n); e != nil {
		return out, e
	}
	e := s.Store.WithWorkspace(ctx, w, func(t PluginTx) error {
		if e := pluginAuthorize(ctx, t, a, false); e != nil {
			return e
		}
		var e error
		out.Items, out.Total, e = t.List(ctx, p, n, filter)
		return e
	})
	return out, e
}
func (s *PluginService) Get(ctx context.Context, w, a, id string) (Plugin, error) {
	var out Plugin
	e := s.Store.WithWorkspace(ctx, w, func(t PluginTx) error {
		if e := pluginAuthorize(ctx, t, a, false); e != nil {
			return e
		}
		var e error
		out, e = t.Get(ctx, id)
		return e
	})
	return out, e
}
func (s *PluginService) Versions(ctx context.Context, w, a, id string, p, n int) (PluginPage, error) {
	out := PluginPage{Items: []Plugin{}, Page: p, PageSize: n}
	if e := pageValid(p, n); e != nil {
		return out, e
	}
	e := s.Store.WithWorkspace(ctx, w, func(t PluginTx) error {
		if e := pluginAuthorize(ctx, t, a, false); e != nil {
			return e
		}
		if _, e := t.Get(ctx, id); e != nil {
			return e
		}
		var e error
		out.Items, out.Total, e = t.Versions(ctx, id, p, n)
		return e
	})
	return out, e
}
func (s *PluginService) Create(ctx context.Context, w, a string, r PluginCreate) (Plugin, error) {
	out := Plugin{ID: NewID(), VersionID: NewID(), Name: r.Name, Description: r.Description, Endpoint: r.Endpoint, AuthType: r.AuthType, HeaderName: r.HeaderName, Version: 1, Enabled: true}
	if e := s.ConfigureOAuth(w, &out, r.OAuth); e != nil {
		return out, e
	}
	if e := ValidatePlugin(&out); e != nil {
		return out, e
	}
	e := s.Store.WithWorkspace(ctx, w, func(t PluginTx) error {
		if e := pluginAuthorize(ctx, t, a, true); e != nil {
			return e
		}
		if e := t.Save(ctx, out, true, true); e != nil {
			return e
		}
		var e error
		out, e = t.Get(ctx, out.ID)
		return e
	})
	return out, e
}
func (s *PluginService) Update(ctx context.Context, w, a, id string, r PluginPatch) (Plugin, error) {
	var out Plugin
	e := s.Store.WithWorkspace(ctx, w, func(t PluginTx) error {
		if e := pluginAuthorize(ctx, t, a, true); e != nil {
			return e
		}
		p, e := t.Get(ctx, id)
		if e != nil {
			return e
		}
		if p.Scope == "official" {
			return Fail("official_resource_read_only", 403, "官方定义仅允许平台管理员修改")
		}
		if r.Version != p.Version {
			return Fail("plugin_version_conflict", 409, "插件已更新，请刷新")
		}
		old := p
		if r.Name != nil {
			p.Name = *r.Name
		}
		if r.Description != nil {
			p.Description = *r.Description
		}
		if r.Endpoint != nil {
			p.Endpoint = *r.Endpoint
		}
		if r.AuthType != nil {
			p.AuthType = *r.AuthType
		}
		if r.HeaderName != nil {
			p.HeaderName = *r.HeaderName
		}
		if r.Enabled != nil {
			p.Enabled = *r.Enabled
		}
		if e = s.ConfigureOAuth(w, &p, r.OAuth); e != nil {
			return e
		}
		if e = ValidatePlugin(&p); e != nil {
			return e
		}
		changed := r.OAuth != nil || p.Name != old.Name || p.Description != old.Description || p.Endpoint != old.Endpoint || p.AuthType != old.AuthType || p.HeaderName != old.HeaderName
		p.Version++
		if changed {
			p.VersionID = NewID()
		}
		if e = t.Save(ctx, p, false, changed); e != nil {
			return e
		}
		out, e = t.Get(ctx, id)
		return e
	})
	return out, e
}
