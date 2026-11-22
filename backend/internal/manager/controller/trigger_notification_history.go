package controller

import (
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"net/http"
)

func (s *ChannelServer) notificationContextRoutes(mux *http.ServeMux) {
	if s.Manager == nil {
		return
	}
	store, ok := s.Manager.Store.(service.NotificationContextStore)
	if !ok {
		return
	}
	mux.HandleFunc("POST /api/internal/v1/trigger-notification-contexts/claim", s.wrap(func(r *http.Request) (any, error) {
		var v struct {
			Limit int `json:"limit"`
		}
		if e := decode(r, &v); e != nil {
			return nil, e
		}
		items, e := store.ClaimNotificationContexts(r.Context(), v.Limit)
		return map[string]any{"items": items}, e
	}))
	mux.HandleFunc("POST /api/internal/v1/trigger-notification-contexts/{id}/authorize", s.wrap(func(r *http.Request) (any, error) {
		var v struct {
			LeaseToken string `json:"lease_token"`
		}
		if e := decode(r, &v); e != nil {
			return nil, e
		}
		return store.AuthorizeNotificationContext(r.Context(), r.PathValue("id"), v.LeaseToken)
	}))
	mux.HandleFunc("POST /api/internal/v1/trigger-notification-contexts/{id}/complete", s.wrap(func(r *http.Request) (any, error) {
		var v service.NotificationContextCompletion
		if e := decode(r, &v); e != nil {
			return nil, e
		}
		e := store.CompleteNotificationContext(r.Context(), r.PathValue("id"), v)
		return map[string]bool{"accepted": e == nil}, e
	}))
}
