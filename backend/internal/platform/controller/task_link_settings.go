package controller

import (
	"net/http"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func (s *Server) taskLinkSettingsRoutes(m *http.ServeMux) {
	if s.TaskLinks == nil {
		return
	}
	const path = "/api/v1/platform/task-link-settings"
	m.HandleFunc("GET "+path, s.wrap("GET "+path, s.getTaskLinkSettings))
	m.HandleFunc("PATCH "+path, s.wrap("PATCH "+path, s.patchTaskLinkSettings))
}

func (s *Server) getTaskLinkSettings(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	actor, err := s.actor(r)
	if err != nil {
		return nil, 0, err
	}
	out, err := s.TaskLinks.Get(r.Context(), actor.ID)
	return out, http.StatusOK, err
}

func (s *Server) patchTaskLinkSettings(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	actor, err := s.actor(r)
	if err != nil {
		return nil, 0, err
	}
	var patch service.TaskLinkSettingsPatch
	if err := decode(r, &patch); err != nil {
		return nil, 0, err
	}
	out, err := s.TaskLinks.Save(r.Context(), actor.ID, patch)
	return out, http.StatusOK, err
}
