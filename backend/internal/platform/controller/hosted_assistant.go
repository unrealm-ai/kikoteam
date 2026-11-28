package controller

import (
	"context"
	"net/http"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type hostedAssistantSettings interface {
	Entry(context.Context, string, string) (service.AssistantEntry, error)
}

func (s *Server) hostedAssistantRoutes(m *http.ServeMux) {
	if s.Assistants == nil {
		return
	}
	m.HandleFunc("GET /api/v1/workspaces/{workspace_id}/assistant", s.wrap("GET /api/v1/workspaces/{workspace_id}/assistant", s.assistantEntry))
}

func (s *Server) assistantEntry(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, err := s.actor(r)
	if err != nil {
		return nil, 0, err
	}
	out, err := s.Assistants.Entry(r.Context(), r.PathValue("workspace_id"), a.ID)
	return out, 200, err
}
