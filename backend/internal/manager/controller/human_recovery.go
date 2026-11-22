package controller

import (
	"context"
	"net/http"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

type humanRecoveryStore interface {
	HumanRecoveryList(context.Context, string, string, string) (service.HumanRecoveryListing, error)
	RecoverHumanRequest(context.Context, string, string, string, string, service.HumanRecoveryAction) (service.HumanRecoveryItem, error)
}

func (s *Server) humanRecoveryRoutes(mux *http.ServeMux, base string) {
	store, ok := s.Engines.(humanRecoveryStore)
	if !ok {
		return
	}
	path := base + "/agents/{agent_id}/human-recovery"
	mux.HandleFunc("GET "+path, s.wrap("listHumanRecovery", func(r *http.Request, actor string) (any, int, error) {
		out, err := store.HumanRecoveryList(r.Context(), workspace(r), actor, r.PathValue("agent_id"))
		return out, 200, err
	}))
	mux.HandleFunc("POST "+path+"/{request_id}", s.wrap("recoverHumanRequest", func(r *http.Request, actor string) (any, int, error) {
		var action service.HumanRecoveryAction
		if err := decode(r, &action); err != nil {
			return nil, 400, err
		}
		out, err := store.RecoverHumanRequest(r.Context(), workspace(r), actor, r.PathValue("agent_id"), r.PathValue("request_id"), action)
		return out, 200, err
	}))
}
