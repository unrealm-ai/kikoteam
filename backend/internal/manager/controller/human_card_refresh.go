package controller

import (
	"context"
	"net/http"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func (s *ChannelServer) humanCardRefreshRoutes(mux *http.ServeMux) {
	store, ok := s.Store.(interface {
		ClaimHumanCardRefresh(context.Context) ([]service.HumanCardRefresh, error)
		AuthorizeHumanCardRefresh(context.Context, string, string) (service.HumanCardRefresh, error)
		CompleteHumanCardRefresh(context.Context, string, string, string) error
	})
	if !ok {
		return
	}
	const base = "/api/internal/v1/human-notices/card-refreshes"
	mux.HandleFunc("POST "+base+"/claim", s.wrap(func(r *http.Request) (any, error) {
		list, err := store.ClaimHumanCardRefresh(r.Context())
		return map[string]any{"items": list}, err
	}))
	mux.HandleFunc("POST "+base+"/{id}/authorize", s.wrap(func(r *http.Request) (any, error) {
		var body struct {
			Lease string `json:"lease_token"`
		}
		if err := decode(r, &body); err != nil {
			return nil, err
		}
		return store.AuthorizeHumanCardRefresh(r.Context(), r.PathValue("id"), body.Lease)
	}))
	mux.HandleFunc("POST "+base+"/{id}/complete", s.wrap(func(r *http.Request) (any, error) {
		var body struct {
			Lease  string `json:"lease_token"`
			Result string `json:"result"`
		}
		if err := decode(r, &body); err != nil {
			return nil, err
		}
		err := store.CompleteHumanCardRefresh(r.Context(), r.PathValue("id"), body.Lease, body.Result)
		return map[string]bool{"accepted": err == nil}, err
	}))
}
