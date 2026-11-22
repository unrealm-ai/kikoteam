package controller

import (
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"net/http"
)

func (s *ChannelServer) sharedGroupHistoryRoutes(m *http.ServeMux) {
	if s.Manager == nil {
		return
	}
	store, ok := s.Manager.Store.(service.SharedHistoryWorkerStore)
	if !ok {
		return
	}
	p := "/api/internal/v1/shared-group-history"
	m.HandleFunc("POST "+p+"/claim", s.wrap(func(r *http.Request) (any, error) {
		var q struct{}
		if e := decode(r, &q); e != nil {
			return nil, e
		}
		items, e := store.ClaimSharedHistory(r.Context())
		return map[string]any{"items": items}, e
	}))
	m.HandleFunc("POST "+p+"/{id}/authorize", s.wrap(func(r *http.Request) (any, error) {
		var q struct {
			LeaseToken string `json:"lease_token"`
		}
		if e := decode(r, &q); e != nil {
			return nil, e
		}
		return store.AuthorizeSharedHistory(r.Context(), r.PathValue("id"), q.LeaseToken)
	}))
	m.HandleFunc("POST "+p+"/{id}/complete", s.wrap(func(r *http.Request) (any, error) {
		var q service.SharedHistoryCompletion
		if e := decode(r, &q); e != nil {
			return nil, e
		}
		e := store.CompleteSharedHistory(r.Context(), r.PathValue("id"), q)
		return map[string]bool{"accepted": e == nil}, e
	}))
}
