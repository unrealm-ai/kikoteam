package controller

import (
	"context"
	"net/http"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

type humanNoticeStore interface {
	ClaimHumanNotices(context.Context, int, bool) ([]service.ChannelDelivery, error)
	AuthorizeHumanNotice(context.Context, string, string, bool) (service.ChannelDelivery, error)
	CompleteHumanNotice(context.Context, string, service.DeliveryCompletion) error
}

// The Hub token authenticates this transport; browser identities cannot
// authorize any notice or provide a recipient.
func (s *ChannelServer) humanNoticeRoutes(mux *http.ServeMux) {
	store, ok := s.Store.(humanNoticeStore)
	if !ok {
		return
	}
	const base = "/api/internal/v1/human-notices"
	for path, query := range map[string]bool{base + "/claim": false, base + "/queries/claim": true} {
		mux.HandleFunc("POST "+path, s.wrap(func(r *http.Request) (any, error) {
			var body struct {
				Limit int `json:"limit"`
			}
			if err := decode(r, &body); err != nil {
				return nil, err
			}
			items, err := store.ClaimHumanNotices(r.Context(), body.Limit, query)
			return map[string]any{"items": items}, err
		}))
	}
	for suffix, check := range map[string]bool{"/authorize": false, "/dispatch-check": true} {
		mux.HandleFunc("POST "+base+"/{id}"+suffix, s.wrap(func(r *http.Request) (any, error) {
			var body struct {
				Lease string `json:"lease_token"`
			}
			if err := decode(r, &body); err != nil {
				return nil, err
			}
			return store.AuthorizeHumanNotice(r.Context(), r.PathValue("id"), body.Lease, check)
		}))
	}
	mux.HandleFunc("POST "+base+"/{id}/complete", s.wrap(func(r *http.Request) (any, error) {
		var body service.DeliveryCompletion
		if err := decode(r, &body); err != nil {
			return nil, err
		}
		err := store.CompleteHumanNotice(r.Context(), r.PathValue("id"), body)
		return map[string]bool{"accepted": err == nil}, err
	}))
}
