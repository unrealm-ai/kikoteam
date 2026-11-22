package controller

import (
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"net/http"
	"strconv"
)

func (s *Server) TriggerNotificationReadRoutes(m *http.ServeMux) {
	base := "/api/v1/workspaces/{workspace_id}/agents/{agent_id}"
	m.HandleFunc("GET "+base+"/trigger-notification-targets", s.wrap("listTriggerNotificationTargets", s.triggerNotificationTargets))
	m.HandleFunc("GET "+base+"/triggers/{trigger_id}/notifications", s.wrap("listTriggerNotifications", s.triggerNotificationViews))
}
func noticeReadLimit(r *http.Request) (int, error) {
	if e := taskQuery(r, "limit"); e != nil {
		return 0, e
	}
	n := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		var e error
		n, e = strconv.Atoi(v)
		if e != nil {
			return 0, service.Fail("invalid_argument", 400, "limit无效")
		}
	}
	if n < 1 || n > 100 {
		return 0, service.Fail("invalid_argument", 400, "limit无效")
	}
	return n, nil
}
func (s *Server) triggerNotificationTargets(r *http.Request, actor string) (any, int, error) {
	n, e := noticeReadLimit(r)
	if e != nil {
		return nil, 400, e
	}
	st, ok := s.Service.Store.(service.TriggerNotificationReads)
	if !ok {
		return nil, 503, service.Fail("dependency_unavailable", 503, "通知目标不可用")
	}
	items, more, e := st.TriggerNotificationCandidates(r.Context(), workspace(r), actor, r.PathValue("agent_id"), n)
	return map[string]any{"items": items, "truncated": more}, 200, e
}
func (s *Server) triggerNotificationViews(r *http.Request, actor string) (any, int, error) {
	n, e := noticeReadLimit(r)
	if e != nil {
		return nil, 400, e
	}
	st, ok := s.Service.Store.(service.TriggerNotificationReads)
	if !ok {
		return nil, 503, service.Fail("dependency_unavailable", 503, "通知查询不可用")
	}
	items, more, e := st.TriggerNotificationViews(r.Context(), workspace(r), actor, r.PathValue("agent_id"), r.PathValue("trigger_id"), n)
	return map[string]any{"items": items, "truncated": more}, 200, e
}
