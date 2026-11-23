package controller

import (
	"net/http"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func (s *Server) channelDownloadSettingsRoutes(m *http.ServeMux) {
	if s.ChannelDownloads == nil {
		return
	}
	const path = "/api/v1/platform/channel-download-settings"
	m.HandleFunc("GET "+path, s.wrap("GET "+path, s.getChannelDownloadSettings))
	m.HandleFunc("PATCH "+path, s.wrap("PATCH "+path, s.patchChannelDownloadSettings))
}

func (s *Server) getChannelDownloadSettings(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	actor, err := s.actor(r)
	if err != nil {
		return nil, 0, err
	}
	out, err := s.ChannelDownloads.Get(r.Context(), actor.ID)
	return out, http.StatusOK, err
}

func (s *Server) patchChannelDownloadSettings(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	actor, err := s.actor(r)
	if err != nil {
		return nil, 0, err
	}
	var patch service.ChannelDownloadSettingsPatch
	if err := decode(r, &patch); err != nil {
		return nil, 0, err
	}
	out, err := s.ChannelDownloads.Save(r.Context(), actor.ID, patch)
	return out, http.StatusOK, err
}
