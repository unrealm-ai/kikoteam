package controller

import (
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"net/http"
)

func (s *Server) skillRoutes(m *http.ServeMux) {
	if s.Skills == nil {
		return
	}
	for pattern, op := range map[string]operation{
		"GET /api/v1/workspaces/{workspace_id}/skills":                                              s.skillList,
		"POST /api/v1/workspaces/{workspace_id}/skills":                                             s.skillCreate,
		"POST /api/v1/workspaces/{workspace_id}/skill-imports":                                      s.skillImport,
		"GET /api/v1/workspaces/{workspace_id}/skill-imports/{import_id}":                           s.skillImportGet,
		"GET /api/v1/workspaces/{workspace_id}/skills/{skill_id}":                                   s.skillGet,
		"PATCH /api/v1/workspaces/{workspace_id}/skills/{skill_id}":                                 s.skillUpdate,
		"GET /api/v1/workspaces/{workspace_id}/skills/{skill_id}/versions":                          s.skillVersions,
		"POST /api/v1/workspaces/{workspace_id}/skills/{skill_id}/versions":                         s.skillPublish,
		"GET /api/v1/workspaces/{workspace_id}/skills/{skill_id}/versions/{skill_version_id}/files": s.skillFile,
	} {
		m.HandleFunc(pattern, s.wrap(pattern, op))
	}
}
func (s *Server) skillList(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	p, n, filter, e := resourcePage(r)
	if e != nil {
		return nil, 0, e
	}
	v, e := s.Skills.List(r.Context(), r.PathValue("workspace_id"), a.ID, p, n, filter)
	return v, 200, e
}
func (s *Server) skillGet(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	v, e := s.Skills.Get(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("skill_id"))
	return v, 200, e
}
func (s *Server) skillImport(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	var b service.SkillImportRequest
	if e = decode(r, &b); e != nil {
		return nil, 0, e
	}
	v, e := s.Skills.Import(r.Context(), r.PathValue("workspace_id"), a.ID, b)
	return v, 201, e
}
func (s *Server) skillImportGet(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	v, e := s.Skills.GetImport(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("import_id"))
	return v, 200, e
}
func (s *Server) skillCreate(w http.ResponseWriter, r *http.Request) (any, int, error) {
	return s.skillSave(w, r, "")
}
func (s *Server) skillPublish(w http.ResponseWriter, r *http.Request) (any, int, error) {
	return s.skillSave(w, r, r.PathValue("skill_id"))
}
func (s *Server) skillSave(w http.ResponseWriter, r *http.Request, target string) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	var b service.SkillPublish
	if e = decode(r, &b); e != nil {
		return nil, 0, e
	}
	v, e := s.Skills.Publish(r.Context(), r.PathValue("workspace_id"), a.ID, target, b)
	if e == nil {
		s.skillSaved(w, r, a.ID, v)
	}
	return v, 201, e
}
func (s *Server) skillUpdate(w http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	var b service.SkillPatch
	if e = decode(r, &b); e != nil {
		return nil, 0, e
	}
	v, e := s.Skills.Update(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("skill_id"), b)
	if e == nil {
		s.skillSaved(w, r, a.ID, v)
	}
	return v, 200, e
}
func (s *Server) skillVersions(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	p, n, e := page(r)
	if e != nil {
		return nil, 0, e
	}
	items, total, e := s.Skills.Versions(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("skill_id"), p, n)
	return map[string]any{"items": items, "total": total, "page": p, "page_size": n}, 200, e
}
func (s *Server) skillFile(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	q := r.URL.Query()
	if len(q) != 1 || len(q["path"]) != 1 || q.Get("path") == "" {
		return nil, 0, service.Fail("invalid_query", 400, "需要唯一 path 参数")
	}
	v, e := s.Skills.File(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("skill_id"), r.PathValue("skill_version_id"), q.Get("path"))
	return v, 200, e
}
func (s *Server) skillSaved(w http.ResponseWriter, r *http.Request, actor string, v service.Skill) {
	if s.Logger != nil {
		s.Logger.InfoContext(r.Context(), "Skill configuration saved", "event", "skill.saved", "trace_id", w.Header().Get("X-Request-ID"), "workspace_id", r.PathValue("workspace_id"), "actor_id", actor, "skill_id", v.ID, "version", v.Version, "content_version_id", v.CurrentVersionID)
	}
}
