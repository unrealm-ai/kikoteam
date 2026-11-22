package controller

import (
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"net/http"
)

func (s *Server) webGroupRoutes(m *http.ServeMux) {
	if s.WebGroups == nil {
		return
	}
	base := "/api/v1/workspaces/{workspace_id}/web-groups"
	for path, op := range map[string]operation{"POST " + base + "/{group_id}/leave": s.webGroupLeave, "GET " + base: s.webGroupList, "POST " + base: s.webGroupSave, "GET " + base + "/{group_id}": s.webGroupGet, "PUT " + base + "/{group_id}": s.webGroupSave} {
		m.HandleFunc(path, s.wrap(path, op))
	}
}
func (s *Server) webGroupList(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, err := s.actor(r)
	if err != nil {
		return nil, 0, err
	}
	query := r.URL.Query()
	if len(query["q"]) > 1 {
		return nil, 0, service.Fail("invalid_query", 400, "查询参数重复")
	}
	search := query.Get("q")
	query.Del("q")
	p, n, err := pagination(query, 50)
	if err != nil {
		return nil, 0, err
	}
	v, err := s.WebGroups.List(r.Context(), r.PathValue("workspace_id"), a.ID, search, p, n)
	return v, 200, err
}
func (s *Server) webGroupGet(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, err := s.actor(r)
	if err != nil {
		return nil, 0, err
	}
	v, err := s.WebGroups.Get(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("group_id"))
	return v, 200, err
}
func (s *Server) webGroupSave(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, err := s.actor(r)
	if err != nil {
		return nil, 0, err
	}
	var q service.WebGroupPut
	if err = decode(r, &q); err != nil {
		return nil, 0, err
	}
	v, err := s.WebGroups.Save(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("group_id"), q)
	status := 200
	if r.Method == http.MethodPost {
		status = 201
	}
	return v, status, err
}

func (s *Server) webGroupLeave(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, err := s.actor(r)
	if err != nil {
		return nil, 0, err
	}
	err = s.WebGroups.Leave(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("group_id"))
	return map[string]bool{"left": true}, 200, err
}
