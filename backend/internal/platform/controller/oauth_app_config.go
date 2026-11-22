package controller

import (
	"net/http"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func (s *Server) oauthAppRoutes(m *http.ServeMux) {
	if s.OAuthApps == nil {
		return
	}
	const p = "/api/v1/platform/oauth-apps/github"
	m.HandleFunc("GET "+p, s.wrap("GET "+p, s.gitHubOAuthApp))
	m.HandleFunc("PATCH "+p, s.wrap("PATCH "+p, s.patchGitHubOAuthApp))
}

func (s *Server) gitHubOAuthApp(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	actor, err := s.actor(r)
	if err != nil {
		return nil, 0, err
	}
	out, err := s.OAuthApps.GitHubStatus(r.Context(), actor.ID)
	return out, 200, err
}

func (s *Server) patchGitHubOAuthApp(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	actor, err := s.actor(r)
	if err != nil {
		return nil, 0, err
	}
	var p service.OAuthAppPatch
	if err = decode(r, &p); err != nil {
		return nil, 0, err
	}
	out, err := s.OAuthApps.UpdateGitHub(r.Context(), actor.ID, p)
	return out, 200, err
}
