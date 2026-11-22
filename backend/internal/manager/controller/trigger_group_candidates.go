package controller

import (
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"net/http"
	"strconv"
)

func (s *Server) triggerGroupCandidateRoutes(m *http.ServeMux) {
	base := "/api/v1/workspaces/{workspace_id}/agents/{agent_id}/trigger-group-candidates"
	m.HandleFunc("GET "+base, s.wrap("listTriggerGroupCandidates", s.triggerGroupCandidates))
	m.HandleFunc("POST "+base+"/resolve", s.wrap("resolveTriggerGroupCandidate", s.resolveTriggerGroupCandidate))
}
func (s *Server) triggerGroupCandidates(r *http.Request, actor string) (any, int, error) {
	if e := taskQuery(r, "limit", "cursor"); e != nil {
		return nil, 400, e
	}
	n := 100
	var e error
	if r.URL.Query().Get("limit") != "" {
		n, e = strconv.Atoi(r.URL.Query().Get("limit"))
		if e != nil {
			return nil, 400, service.Fail("invalid_argument", 400, "limit无效")
		}
	}
	out, e := s.Service.ListTriggerGroupCandidates(r.Context(), workspace(r), actor, r.PathValue("agent_id"), r.URL.Query().Get("cursor"), n)
	return map[string]any{"items": out.Items, "next_cursor": out.NextCursor, "truncated": out.NextCursor != nil}, 200, e

}
func (s *Server) resolveTriggerGroupCandidate(r *http.Request, actor string) (any, int, error) {
	var q service.TriggerGroupResolve
	if e := decode(r, &q); e != nil {
		return nil, 400, e
	}
	st, ok := s.Service.Store.(service.TriggerGroupCandidates)
	if !ok {
		return nil, 503, service.Fail("dependency_unavailable", 503, "群候选不可用")
	}
	out, e := st.ResolveTriggerGroupCandidate(r.Context(), workspace(r), actor, r.PathValue("agent_id"), q)
	return out, 200, e
}
