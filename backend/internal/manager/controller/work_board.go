package controller

import (
	"net/http"
	"strconv"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func (s *Server) workBoardRoutes(m *http.ServeMux) {
	base := "/api/v1/workspaces/{workspace_id}"
	m.HandleFunc("GET "+base+"/work-items", s.wrap("listWorkItems", s.listWorkItems))
	m.HandleFunc("GET "+base+"/work-items/{work_item_id}", s.wrap("getWorkItem", s.getWorkItem))
	policy := base + "/agents/{agent_id}/handoff-scope"
	m.HandleFunc("GET "+policy, s.wrap("getHandoffScope", s.workPolicy))
	m.HandleFunc("PUT "+policy, s.wrap("updateHandoffScope", s.workPolicy))
}
func (s *Server) listWorkItems(r *http.Request, actor string) (any, int, error) {
	if err := taskQuery(r, "status", "employee_id", "limit", "cursor", "include_counts"); err != nil {
		return nil, 400, err
	}
	n, e := limit(r)
	if e != nil {
		return nil, 400, e
	}
	q := r.URL.Query()
	counts, e := includeCounts(r)
	if e != nil {
		return nil, 400, e
	}
	v, e := s.Service.ListWork(r.Context(), workspace(r), actor, q.Get("status"), q.Get("employee_id"), q.Get("cursor"), n, counts)
	return v, 200, e
}
func (s *Server) getWorkItem(r *http.Request, actor string) (any, int, error) {
	if e := taskQuery(r, "before", "limit"); e != nil {
		return nil, 400, e
	}
	n, e := limit(r)
	if e != nil {
		return nil, 400, e
	}
	if n == 0 {
		n = 50
	}
	id := r.PathValue("work_item_id")
	var before int64
	if v := r.URL.Query().Get("before"); v != "" {
		before, e = strconv.ParseInt(v, 10, 64)
		if e != nil || before <= 0 {
			return nil, 400, service.Fail("invalid_argument", 400, "时间线游标无效")
		}
	}
	if !service.ValidID(id) || n < 1 || n > 100 || !service.ValidID(workspace(r)) {
		return nil, 400, service.Fail("invalid_argument", 400, "事项或页大小无效")
	}
	st, ok := s.Service.Store.(service.WorkBoardStore)
	if !ok {
		return nil, 503, service.Fail("dependency_unavailable", 503, "事项不可用")
	}
	v, e := st.WorkDetail(r.Context(), workspace(r), actor, id, before, n)
	return v, 200, e
}
func (s *Server) workPolicy(r *http.Request, actor string) (any, int, error) {
	agent := r.PathValue("agent_id")
	if !service.ValidID(agent) || !service.ValidID(workspace(r)) {
		return nil, 400, service.Fail("invalid_argument", 400, "员工标识无效")
	}
	var q *service.ReceiverPolicy
	if r.Method == "PUT" {
		q = &service.ReceiverPolicy{}
		if err := decode(r, q); err != nil {
			return nil, 400, err
		}
	}
	st, ok := s.Service.Store.(service.WorkBoardStore)
	if !ok {
		return nil, 503, service.Fail("dependency_unavailable", 503, "事项不可用")
	}
	v, e := st.ReceiverPolicy(r.Context(), workspace(r), actor, agent, q)
	return v, 200, e
}
