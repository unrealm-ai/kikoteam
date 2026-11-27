package controller

import (
	"context"
	"net/http"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type hostedAssistantSettings interface {
	ReadAdmin(context.Context, string) (service.AssistantSettings, error)
	SaveAdmin(context.Context, string, service.AssistantSettingsPatch) (service.AssistantSettings, error)
	Entry(context.Context, string, string) (service.AssistantEntry, error)
}

func (s *Server) hostedAssistantRoutes(m *http.ServeMux) {
	if s.Assistants == nil {
		return
	}
	for pattern, handler := range map[string]operation{
		"GET /api/v1/platform/assistant-settings":         s.assistantSettings,
		"PATCH /api/v1/platform/assistant-settings":       s.saveAssistantSettings,
		"GET /api/v1/workspaces/{workspace_id}/assistant": s.assistantEntry,
	} {
		m.HandleFunc(pattern, s.wrap(pattern, handler))
	}
}

func (s *Server) assistantSettings(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, err := s.actor(r)
	if err != nil {
		return nil, 0, err
	}
	out, err := s.Assistants.ReadAdmin(r.Context(), a.ID)
	return out, 200, err
}

func (s *Server) saveAssistantSettings(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, err := s.actor(r)
	if err != nil {
		return nil, 0, err
	}
	var p service.AssistantSettingsPatch
	if err = decode(r, &p); err != nil {
		return nil, 0, err
	}
	out, err := s.Assistants.SaveAdmin(r.Context(), a.ID, p)
	return out, 200, err
}

func (s *Server) assistantEntry(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, err := s.actor(r)
	if err != nil {
		return nil, 0, err
	}
	out, err := s.Assistants.Entry(r.Context(), r.PathValue("workspace_id"), a.ID)
	return out, 200, err
}
