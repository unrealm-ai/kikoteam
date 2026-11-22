package controller

import (
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"net/http"
	"strconv"
)

func (s *Server) groupSelectorRoutes(m *http.ServeMux) {
	if s.GroupSelectors == nil {
		return
	}
	p := "/api/v1/workspaces/{workspace_id}/agents/{agent_id}/selectors/groups"
	m.HandleFunc("GET "+p, s.wrap("listGroupSelections", s.groupSelectorSearch))
	m.HandleFunc("POST "+p+"/resolve", s.wrap("resolveGroupSelections", s.groupSelectorResolve))
}
func (s *Server) groupSelectorSearch(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	actor, err := s.actor(r)
	if err != nil {
		return nil, 0, err
	}
	values := r.URL.Query()
	for key, v := range values {
		if len(v) != 1 || (key != "provider" && key != "q" && key != "limit" && key != "cursor") {
			return nil, 0, service.Fail("invalid_group_query", 400, "群查询参数无效")
		}
	}
	q := service.GroupSelectionQuery{Provider: values.Get("provider"), Q: values.Get("q"), Cursor: values.Get("cursor"), Limit: 20}
	if raw := values.Get("limit"); raw != "" {
		q.Limit, err = strconv.Atoi(raw)
		if err != nil {
			return nil, 0, service.Fail("invalid_group_query", 400, "分页大小无效")
		}
	}
	out, err := s.GroupSelectors.Search(r.Context(), r.PathValue("workspace_id"), actor.ID, r.PathValue("agent_id"), q)
	return out, 200, err
}
func (s *Server) groupSelectorResolve(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	actor, err := s.actor(r)
	if err != nil {
		return nil, 0, err
	}
	var q struct {
		Refs      []service.SelectionRef  `json:"refs"`
		Selectors []service.UsageSelector `json:"selectors"`
	}
	if err = decode(r, &q); err != nil {
		return nil, 0, err
	}
	if q.Selectors != nil {
		if q.Refs != nil {
			return nil, 0, service.Fail("invalid_selection", 400, "只能提供一种引用列表")
		}
		out, err := s.GroupSelectors.ResolveSaved(r.Context(), r.PathValue("workspace_id"), actor.ID, r.PathValue("agent_id"), q.Selectors)
		return out, 200, err
	}
	out, err := s.GroupSelectors.Resolve(r.Context(), r.PathValue("workspace_id"), actor.ID, r.PathValue("agent_id"), q.Refs)
	return out, 200, err
}
