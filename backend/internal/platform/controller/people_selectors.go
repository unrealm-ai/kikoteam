package controller

import (
	"net/http"
	"strconv"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func (s *Server) peopleSelectorRoutes(m *http.ServeMux) {
	if s.PeopleSelectors == nil {
		return
	}
	p := "/api/v1/workspaces/{workspace_id}/agents/{agent_id}/selectors"
	m.HandleFunc("GET "+p+"/people", s.wrap("GET "+p+"/people", s.peopleSelectorSearch))
	m.HandleFunc("GET "+p+"/departments", s.wrap("GET "+p+"/departments", s.peopleSelectorSearch))
	m.HandleFunc("GET "+p+"/people/sources", s.wrap("GET "+p+"/people/sources", s.peopleSelectorSources))
	m.HandleFunc("POST "+p+"/people/resolve", s.wrap("POST "+p+"/people/resolve", s.peopleSelectorResolve))
}
func peopleSelectorQuery(r *http.Request) (service.PeopleQuery, error) {
	v := r.URL.Query()
	bad := service.Fail("invalid_people_query", 400, "选择查询参数无效")
	for k, vs := range v {
		if len(vs) != 1 {
			return service.PeopleQuery{}, bad
		}
		switch k {
		case "provider", "purpose", "q", "parent_id", "department_id", "limit", "cursor", "snapshot_version":
		default:
			return service.PeopleQuery{}, bad
		}
	}
	q := service.PeopleQuery{Provider: v.Get("provider"), Purpose: v.Get("purpose"), Kind: "user", Q: v.Get("q"), ParentID: v.Get("parent_id"), DepartmentID: v.Get("department_id"), Limit: 50, Cursor: v.Get("cursor")}
	if r.Pattern == "GET /api/v1/workspaces/{workspace_id}/agents/{agent_id}/selectors/departments" {
		q.Kind = "department"
	}
	var e error
	if raw := v.Get("limit"); raw != "" {
		q.Limit, e = strconv.Atoi(raw)
		if e != nil {
			return q, bad
		}
	}
	if raw := v.Get("snapshot_version"); raw != "" {
		q.SnapshotVersion, e = strconv.ParseInt(raw, 10, 64)
		if e != nil {
			return q, bad
		}
	}
	return q, nil
}
func (s *Server) peopleSelectorSearch(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	q, e := peopleSelectorQuery(r)
	if e != nil {
		return nil, 0, e
	}
	v, e := s.PeopleSelectors.Search(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("agent_id"), q)
	return v, 200, e
}
func (s *Server) peopleSelectorSources(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	q := r.URL.Query()
	for k, v := range q {
		if k != "purpose" || len(v) != 1 {
			return nil, 0, service.Fail("invalid_people_query", 400, "查询参数无效")
		}
	}
	v, e := s.PeopleSelectors.Sources(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("agent_id"), q.Get("purpose"))
	return struct {
		Items []service.PeopleSource `json:"items"`
	}{v}, 200, e
}
func (s *Server) peopleSelectorResolve(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	var req struct {
		Purpose   string                  `json:"purpose"`
		Refs      []service.SelectionRef  `json:"refs"`
		Selectors []service.UsageSelector `json:"selectors"`
	}
	if e = decode(r, &req); e != nil {
		return nil, 0, e
	}
	if req.Selectors != nil {
		if req.Refs != nil {
			return nil, 0, service.Fail("invalid_people_refs", 400, "只能提供一种引用列表")
		}
		v, e := s.PeopleSelectors.ResolveSaved(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("agent_id"), req.Purpose, req.Selectors)
		return v, 200, e
	}
	v, e := s.PeopleSelectors.Resolve(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("agent_id"), req.Purpose, req.Refs)
	return v, 200, e
}
