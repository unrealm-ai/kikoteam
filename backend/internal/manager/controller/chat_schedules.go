package controller

import (
	"net/http"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func (s *Server) chatScheduleManagementRoutes(m *http.ServeMux) {
	base := "/api/v1/workspaces/{workspace_id}/agents/{agent_id}/chat-schedules"
	m.HandleFunc("GET "+base, s.wrap("listManagedChatSchedules", func(r *http.Request, actor string) (any, int, error) {
		v, err := s.Service.ManagedChatSchedules(r.Context(), workspace(r), actor, r.PathValue("agent_id"))
		return map[string]any{"items": v}, http.StatusOK, err
	}))
	m.HandleFunc("PATCH "+base+"/{schedule_id}", s.wrap("updateManagedChatSchedule", func(r *http.Request, actor string) (any, int, error) {
		var q service.ManagedChatSchedulePatch
		if err := decode(r, &q); err != nil {
			return nil, http.StatusBadRequest, err
		}
		v, err := s.Service.UpdateManagedChatSchedule(r.Context(), workspace(r), actor, r.PathValue("agent_id"), r.PathValue("schedule_id"), q)
		return v, http.StatusOK, err
	}))
}
