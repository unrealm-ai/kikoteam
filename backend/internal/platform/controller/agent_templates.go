package controller

import (
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"net/http"
	"strconv"
)

func (s *Server) templateRoutes(m *http.ServeMux) {
	if s.Templates == nil {
		return
	}
	for _, base := range []string{"/api/v1/official-agent-templates", "/api/v1/workspaces/{workspace_id}/agent-templates"} {
		for pattern, op := range map[string]operation{"GET " + base: s.templateList, "POST " + base: s.templateCreate, "GET " + base + "/{template_id}": s.templateGet, "PATCH " + base + "/{template_id}": s.templateUpdate, "DELETE " + base + "/{template_id}": s.templateDelete, "POST " + base + "/{template_id}/publish": s.templatePublish, "GET " + base + "/{template_id}/versions": s.templateVersions, "GET " + base + "/{template_id}/versions/{version_id}": s.templateVersion} {
			m.HandleFunc(pattern, s.wrap(pattern, op))
		}
	}
	for pattern, op := range map[string]operation{"POST /api/v1/workspaces/{workspace_id}/agents/from-template": s.templateEmployee, "POST /api/v1/workspaces/{workspace_id}/agents/{agent_id}/agent-templates": s.templateFromAgent, "GET /api/v1/workspaces/{workspace_id}/agents/{agent_id}/setup-status": s.templateSetup} {
		m.HandleFunc(pattern, s.wrap(pattern, op))
	}
}
func templateQuery(r *http.Request) (service.TemplateQuery, error) {
	q := service.TemplateQuery{Page: 1, PageSize: 20}
	for k, v := range r.URL.Query() {
		if len(v) != 1 {
			return q, service.Fail("invalid_query", 400, "查询参数重复")
		}
		switch k {
		case "q":
			q.Q = v[0]
		case "category":
			q.Category = v[0]
		case "sort_by":
			q.SortBy = v[0]
		case "sort_order":
			q.SortOrder = v[0]
		case "view":
			q.View = v[0]
		case "status":
			q.Status = v[0]
		case "page", "page_size":
			n, e := strconv.Atoi(v[0])
			if e != nil || n < 1 || k == "page_size" && n > 100 {
				return q, service.Fail("invalid_query", 400, "分页无效")
			}
			if k == "page" {
				q.Page = n
			} else {
				q.PageSize = n
			}
		default:
			return q, service.Fail("invalid_query", 400, "未知查询参数")
		}
	}
	if q.View != "" && q.View != "manage" {
		return q, service.Fail("invalid_query", 400, "视图无效")
	}
	return q, nil
}
func (s *Server) templateList(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	q, e := templateQuery(r)
	if e != nil {
		return nil, 0, e
	}
	v, e := s.Templates.List(r.Context(), r.PathValue("workspace_id"), a.ID, q)
	return v, 200, e
}
func (s *Server) templateGet(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	q, e := templateQuery(r)
	if e != nil {
		return nil, 0, e
	}
	v, e := s.Templates.Get(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("template_id"), q.View == "manage")
	return v, 200, e
}
func (s *Server) templateCreate(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	var b struct {
		Content service.TemplateContent `json:"content"`
	}
	if e = decode(r, &b); e != nil {
		return nil, 0, e
	}
	v, e := s.Templates.Create(r.Context(), r.PathValue("workspace_id"), a.ID, b.Content)
	return v, 201, e
}
func (s *Server) templateUpdate(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	var b service.TemplatePatch
	if e = decode(r, &b); e != nil {
		return nil, 0, e
	}
	v, e := s.Templates.Update(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("template_id"), b)
	return v, 200, e
}
func (s *Server) templatePublish(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	var b service.TemplatePublish
	if e = decode(r, &b); e != nil {
		return nil, 0, e
	}
	v, e := s.Templates.Publish(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("template_id"), b)
	return v, 200, e
}
func (s *Server) templateDelete(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	var b struct {
		Version int64 `json:"version"`
	}
	if e = decode(r, &b); e != nil {
		return nil, 0, e
	}
	return nil, 204, s.Templates.Delete(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("template_id"), b.Version)
}
func (s *Server) templateVersions(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	q, e := templateQuery(r)
	if e != nil {
		return nil, 0, e
	}
	v, e := s.Templates.Versions(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("template_id"), q)
	return v, 200, e
}
func (s *Server) templateVersion(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	q, e := templateQuery(r)
	if e != nil {
		return nil, 0, e
	}
	v, e := s.Templates.Version(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("template_id"), r.PathValue("version_id"), q.View == "manage")
	return v, 200, e
}
func (s *Server) templateEmployee(w http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	var b service.TemplateCreation
	if e = decode(r, &b); e != nil {
		return nil, 0, e
	}
	v, e := s.Templates.CreateFromTemplate(r.Context(), r.PathValue("workspace_id"), a.ID, b)
	if e == nil {
		s.agentSaved(w, r, a.ID, "agent.created_from_template", v)
	}
	return v, 201, e
}
func (s *Server) templateFromAgent(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	var b struct {
		AgentVersion  int64  `json:"agent_version"`
		ConfigVersion int64  `json:"config_version"`
		Name          string `json:"name"`
	}
	if e = decode(r, &b); e != nil {
		return nil, 0, e
	}
	v, e := s.Templates.FromAgent(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("agent_id"), b.AgentVersion, b.ConfigVersion, b.Name)
	return v, 201, e
}
func (s *Server) templateSetup(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	v, e := s.Templates.Setup(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("agent_id"))
	return v, 200, e
}
func (s *Server) adminRoutes(m *http.ServeMux) {
	if s.Admin == nil {
		return
	}
	m.HandleFunc("GET /api/v1/accounts", s.wrap("GET /api/v1/accounts", s.adminList))
	m.HandleFunc("PATCH /api/v1/accounts/{account_id}/platform-admin", s.wrap("PATCH /api/v1/accounts/{account_id}/platform-admin", s.adminSet))
}
func (s *Server) adminList(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	p, n := 1, 20
	for k, v := range r.URL.Query() {
		if len(v) != 1 {
			return nil, 0, service.Fail("invalid_query", 400, "查询参数重复")
		}
		if k == "page" || k == "page_size" {
			x, err := strconv.Atoi(v[0])
			if err != nil {
				return nil, 0, service.Fail("invalid_query", 400, "分页无效")
			}
			if k == "page" {
				p = x
			} else {
				n = x
			}
		} else if k != "q" && k != "is_platform_admin" {
			return nil, 0, service.Fail("invalid_query", 400, "未知查询条件")
		}
	}
	v, e := s.Admin.List(r.Context(), a.ID, r.URL.Query().Get("q"), r.URL.Query().Get("is_platform_admin"), p, n)
	return v, 200, e
}
func (s *Server) adminSet(w http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	var b struct {
		Version         int64 `json:"version"`
		IsPlatformAdmin *bool `json:"is_platform_admin"`
	}
	if e = decode(r, &b); e != nil {
		return nil, 0, e
	}
	if b.IsPlatformAdmin == nil {
		return nil, 0, service.Fail("invalid_request", 400, "请明确指定管理员资格")
	}
	v, e := s.Admin.Set(r.Context(), a.ID, r.PathValue("account_id"), service.AdminPatch{Version: b.Version, IsPlatformAdmin: *b.IsPlatformAdmin})
	if e == nil && s.Logger != nil {
		s.Logger.InfoContext(r.Context(), "Platform administrator role changed", "event", "account.platform_admin_changed", "trace_id", w.Header().Get("X-Request-ID"), "actor_id", a.ID, "account_id", v.AccountID, "is_platform_admin", v.IsPlatformAdmin)
	}
	return v, 200, e
}
