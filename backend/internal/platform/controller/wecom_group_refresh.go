package controller

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"net/http"
)

func (s *Server) wecomGroupRefreshRoutes(m *http.ServeMux) {
	p := "/api/v1/workspaces/{workspace_id}/agents/{agent_id}/channels/wecom/groups/refresh"
	h := func(_ http.ResponseWriter, r *http.Request) (any, int, error) {
		a, e := s.actor(r)
		if e != nil {
			return nil, 0, e
		}
		if s.WecomGroups == nil {
			return nil, 503, service.Fail("dependency_unavailable", 503, "群刷新不可用")
		}
		if r.Method == "POST" {
			var q struct{}
			if e = decode(r, &q); e != nil {
				return nil, 400, e
			}
		}
		out, e := s.WecomGroups.Run(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("agent_id"), r.PathValue("id"))
		return out, 200, e
	}
	m.HandleFunc("POST "+p, s.wrap("refreshWecomRecentGroups", h))
	m.HandleFunc("GET "+p+"/{id}", s.wrap("getWecomGroupRefresh", h))
}
func WecomGroupRefreshInternalRoutes(m *http.ServeMux, store service.WecomGroupRefreshStore, token string) {
	h := func(action string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			if len(token) < 32 || r.Header.Get("Cookie") != "" || r.Header.Get("Origin") != "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
				w.WriteHeader(401)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
			var out any
			var e error
			switch action {
			case "claim":
				var q struct{}
				e = decode(r, &q)
				if e == nil {
					var items []service.WecomGroupRefreshJob
					items, e = store.ClaimWecomGroups(r.Context())
					out = map[string]any{"items": items}
				}
			case "authorize":
				var q struct {
					LeaseToken string `json:"lease_token"`
				}
				e = decode(r, &q)
				if e == nil {
					out, e = store.AuthorizeWecomGroups(r.Context(), r.PathValue("id"), q.LeaseToken)
				}
			case "complete":
				var q service.WecomGroupRefreshCompletion
				e = decode(r, &q)
				if e == nil {
					e = store.CompleteWecomGroups(r.Context(), r.PathValue("id"), q)
					out = map[string]bool{"accepted": e == nil}
				}
			}
			if e != nil {
				status, code := 503, "refresh_unavailable"
				var se *service.Error
				if errors.As(e, &se) {
					status, code = se.Status, se.Code
				}
				w.WriteHeader(status)
				out = map[string]any{"error": map[string]string{"code": code}}
			}
			if err := json.NewEncoder(w).Encode(out); err != nil {
				return
			}
		}
	}
	base := "/api/internal/v1/wecom-group-refreshes"
	m.HandleFunc("POST "+base+"/claim", h("claim"))
	m.HandleFunc("POST "+base+"/{id}/authorize", h("authorize"))
	m.HandleFunc("POST "+base+"/{id}/complete", h("complete"))
}
