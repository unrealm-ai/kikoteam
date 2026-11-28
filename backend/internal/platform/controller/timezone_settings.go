package controller

import (
	"net/http"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func (s *Server) timezoneSettingsRoutes(m *http.ServeMux) {
	if s.Timezone == nil {
		return
	}
	const path = "/api/v1/platform/timezone-settings"
	m.HandleFunc("GET "+path, s.wrap("GET "+path, s.getTimezoneSettings))
	m.HandleFunc("PATCH "+path, s.wrap("PATCH "+path, s.patchTimezoneSettings))
}

func (s *Server) getTimezoneSettings(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	actor, err := s.actor(r)
	if err != nil {
		return nil, 0, err
	}
	out, err := s.Timezone.Get(r.Context(), actor.ID)
	return out, http.StatusOK, err
}

func (s *Server) patchTimezoneSettings(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	actor, err := s.actor(r)
	if err != nil {
		return nil, 0, err
	}
	var patch service.TimezoneSettingsPatch
	if err := decode(r, &patch); err != nil {
		return nil, 0, err
	}
	out, err := s.Timezone.Save(r.Context(), actor.ID, patch)
	return out, http.StatusOK, err
}
