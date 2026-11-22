package controller

import (
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"net/http"
)

func (s *Server) pluginRoutes(m *http.ServeMux) {
	if s.Plugins == nil {
		return
	}
	// The primary session cookie uses SameSite=Strict. A provider navigation
	// may arrive without it. A first-party link resumes with the original cookie.
	const callback = "GET /api/v1/auth/callbacks/plugin-oauth"
	complete := s.wrap(callback, s.completeOAuth)
	m.HandleFunc(callback, func(w http.ResponseWriter, r *http.Request) {
		if token(r) == "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Referrer-Policy", "no-referrer")
			w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
			//nolint:errcheck // Browser may close the continuation page.
			_, _ = w.Write([]byte(`<!doctype html><html lang="zh"><meta charset="utf-8"><title>完成插件授权</title><a href="">使用当前登录会话完成插件授权</a></html>`))
			return
		}
		complete(w, r)
	})
	for pattern, op := range map[string]operation{
		"GET /api/v1/workspaces/{workspace_id}/plugin-accounts":                                                s.sharedAccounts,
		"GET /api/v1/workspaces/{workspace_id}/plugin-accounts/{plugin_id}":                                    s.pluginAccountGet,
		"PUT /api/v1/workspaces/{workspace_id}/plugin-accounts/{plugin_id}":                                    s.pluginAccountPut,
		"DELETE /api/v1/workspaces/{workspace_id}/plugin-accounts/{plugin_id}":                                 s.pluginAccountDelete,
		"GET /api/v1/workspaces/{workspace_id}/agents/{agent_id}/plugins/{plugin_id}/connection-source":        s.connectionSource,
		"PUT /api/v1/workspaces/{workspace_id}/agents/{agent_id}/plugins/{plugin_id}/connection-source":        s.connectionSource,
		"POST /api/v1/workspaces/{workspace_id}/plugin-accounts/{plugin_id}/oauth/authorize":                   s.authorizeOAuth,
		"POST /api/v1/workspaces/{workspace_id}/agents/{agent_id}/plugin-accounts/{plugin_id}/oauth/authorize": s.authorizeOAuth,
		"GET /api/v1/workspaces/{workspace_id}/plugins":                                                        s.pluginList,
		"POST /api/v1/workspaces/{workspace_id}/plugins":                                                       s.pluginCreate,
		"GET /api/v1/workspaces/{workspace_id}/plugins/{plugin_id}":                                            s.pluginGet,
		"PATCH /api/v1/workspaces/{workspace_id}/plugins/{plugin_id}":                                          s.pluginUpdate,
		"GET /api/v1/workspaces/{workspace_id}/plugins/{plugin_id}/versions":                                   s.pluginVersions,
		"GET /api/v1/workspaces/{workspace_id}/agents/{agent_id}/plugin-accounts/{plugin_id}":                  s.pluginAccountGet,
		"PUT /api/v1/workspaces/{workspace_id}/agents/{agent_id}/plugin-accounts/{plugin_id}":                  s.pluginAccountPut,
		"DELETE /api/v1/workspaces/{workspace_id}/agents/{agent_id}/plugin-accounts/{plugin_id}":               s.pluginAccountDelete,
	} {
		m.HandleFunc(pattern, s.wrap(pattern, op))
	}
}
func (s *Server) pluginList(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	p, n, filter, e := resourcePage(r)
	if e != nil {
		return nil, 0, e
	}
	v, e := s.Plugins.List(r.Context(), r.PathValue("workspace_id"), a.ID, p, n, filter)
	return v, 200, e
}
func (s *Server) pluginGet(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	v, e := s.Plugins.Get(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("plugin_id"))
	return v, 200, e
}
func (s *Server) pluginVersions(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	p, n, e := page(r)
	if e != nil {
		return nil, 0, e
	}
	v, e := s.Plugins.Versions(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("plugin_id"), p, n)
	return v, 200, e
}
func (s *Server) pluginCreate(w http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	var req service.PluginCreate
	if e = decode(r, &req); e != nil {
		return nil, 0, e
	}
	v, e := s.Plugins.Create(r.Context(), r.PathValue("workspace_id"), a.ID, req)
	if e == nil {
		s.pluginLog(w, r, a.ID, "plugin.created", v.ID)
	}
	return v, 201, e
}
func (s *Server) pluginUpdate(w http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	var req service.PluginPatch
	if e = decode(r, &req); e != nil {
		return nil, 0, e
	}
	v, e := s.Plugins.Update(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("plugin_id"), req)
	if e == nil {
		s.pluginLog(w, r, a.ID, "plugin.updated", v.ID)
	}
	return v, 200, e
}
func (s *Server) pluginAccountGet(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	version := r.URL.Query().Get("plugin_version_id")
	if e := validateConnectionVersion(r, version); e != nil {
		return nil, 0, e
	}
	v, e := s.Plugins.GetAccount(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("agent_id"), r.PathValue("plugin_id"), r.URL.Query().Get("plugin_version_id"))
	return v, 200, e
}
func (s *Server) pluginAccountPut(w http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	var req service.PluginAccountPut
	if e = decode(r, &req); e != nil {
		return nil, 0, e
	}
	v, e := s.Plugins.PutAccount(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("agent_id"), r.PathValue("plugin_id"), req)
	if e == nil {
		s.pluginLog(w, r, a.ID, "plugin.account_saved", v.ID)
	}
	return v, 200, e
}
func (s *Server) pluginAccountDelete(w http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	var req struct {
		Version int64 `json:"version"`
	}
	if e = decode(r, &req); e != nil {
		return nil, 0, e
	}
	version := r.URL.Query().Get("plugin_version_id")
	if e := validateConnectionVersion(r, version); e != nil {
		return nil, 0, e
	}
	e = s.Plugins.RevokeAccount(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("agent_id"), r.PathValue("plugin_id"), req.Version, r.URL.Query().Get("plugin_version_id"))
	if e == nil {
		s.pluginLog(w, r, a.ID, "plugin.account_revoked", r.PathValue("plugin_id"))
	}
	return nil, 204, e
}

func (s *Server) pluginLog(w http.ResponseWriter, r *http.Request, actor, event, id string) {
	if s.Logger != nil {
		s.Logger.InfoContext(r.Context(), "Plugin configuration saved", "event", event, "trace_id", w.Header().Get("X-Request-ID"), "actor_id", actor, "workspace_id", r.PathValue("workspace_id"), "object_id", id, "employee_id", r.PathValue("agent_id"))
	}
}
func (s *Server) sharedAccounts(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	rows, e := s.Plugins.SharedAccounts(r.Context(), r.PathValue("workspace_id"), a.ID)
	return map[string]any{"items": rows}, 200, e
}
func (s *Server) connectionSource(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	var in struct {
		Source   string `json:"source"`
		Revision *int64 `json:"revision"`
	}
	if r.Method == "PUT" {
		if e = decode(r, &in); e != nil {
			return nil, 0, e
		}
		if in.Revision == nil {
			return nil, 0, service.Fail("revision_required", 400, "缺少来源修订号")
		}
	}
	v, e := s.Plugins.ConnectionSource(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("agent_id"), r.PathValue("plugin_id"), r.URL.Query().Get("plugin_version_id"), in.Source, in.Revision)
	return v, 200, e
}
func (s *Server) authorizeOAuth(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	var in service.PluginAccountPut
	if e = decode(r, &in); e != nil {
		return nil, 0, e
	}
	u, e := s.Plugins.AuthorizeOAuth(r.Context(), r.PathValue("workspace_id"), a.ID, token(r), r.PathValue("agent_id"), r.PathValue("plugin_id"), in)
	return map[string]string{"authorization_url": u}, 200, e
}
func (s *Server) completeOAuth(w http.ResponseWriter, r *http.Request) (any, int, error) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	q := r.URL.Query()
	code := q.Get("code")
	if q.Has("error") {
		code = ""
	}
	_, e = s.Plugins.CompleteOAuth(r.Context(), a.ID, token(r), q.Get("state"), code, q.Get("iss"))
	if e != nil {
		return nil, 0, e
	}
	w.Header().Set("Location", "/")
	return nil, http.StatusSeeOther, nil
}
func validateConnectionVersion(r *http.Request, v string) error {
	if v == "" && r.PathValue("agent_id") != "" {
		return nil
	}
	if v == "" {
		return service.Fail("invalid_plugin_version", 400, "请指定插件版本")
	}
	r.SetPathValue("version_id", v)
	if validIDs(r) != nil {
		return service.Fail("invalid_plugin_version", 400, "请指定有效的插件版本")
	}
	return nil
}
