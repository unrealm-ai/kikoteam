package controller

import (
	"context"
	"net/http"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

// Register separately: no synthetic inbound record or public actor token.
func (s *ChannelServer) TriggerNotificationRoutes(mux *http.ServeMux) {
	store, ok := s.Store.(service.TriggerNotificationStore)
	if !ok {
		return
	}
	for path, query := range map[string]bool{"/api/internal/v1/trigger-notifications/claim": false, "/api/internal/v1/trigger-notification-queries/claim": true} {
		mux.HandleFunc("POST "+path, s.wrap(func(r *http.Request) (any, error) {
			var v struct {
				Limit int `json:"limit"`
			}
			if e := decode(r, &v); e != nil {
				return nil, e
			}
			var items []service.ChannelDelivery
			var e error
			if query {
				items, e = store.ClaimTriggerNotificationQueries(r.Context(), v.Limit)
			} else {
				items, e = store.ClaimTriggerNotifications(r.Context(), v.Limit)
			}
			return map[string]any{"items": items}, e
		}))
	}
	mux.HandleFunc("POST /api/internal/v1/trigger-notifications/{id}/authorize", s.wrap(func(r *http.Request) (any, error) {
		var v struct {
			LeaseToken string `json:"lease_token"`
		}
		if e := decode(r, &v); e != nil {
			return nil, e
		}
		if r.Header.Get("X-Kiko-Trigger-Result-Card") == "1" {
			if card, ok := store.(interface {
				AuthorizeTriggerNotificationWithResultCard(context.Context, string, string) (service.ChannelDelivery, error)
			}); ok {
				return card.AuthorizeTriggerNotificationWithResultCard(r.Context(), r.PathValue("id"), v.LeaseToken)
			}
		}
		if r.Header.Get("X-Kiko-Delivery-Task-Detail") == "1" {
			if detail, ok := store.(interface {
				AuthorizeTriggerNotificationWithDetail(context.Context, string, string) (service.ChannelDelivery, error)
			}); ok {
				return detail.AuthorizeTriggerNotificationWithDetail(r.Context(), r.PathValue("id"), v.LeaseToken)
			}
		}
		return store.AuthorizeTriggerNotification(r.Context(), r.PathValue("id"), v.LeaseToken)
	}))
	mux.HandleFunc("POST /api/internal/v1/trigger-notifications/{id}/complete", s.wrap(func(r *http.Request) (any, error) {
		var v service.DeliveryCompletion
		if e := decode(r, &v); e != nil {
			return nil, e
		}
		e := store.CompleteTriggerNotification(r.Context(), r.PathValue("id"), v)
		return map[string]bool{"accepted": e == nil}, e
	}))
}

// Web notifications do not depend on a configured IM transport token.
func (s *ChannelServer) RunTriggerNotifications(ctx context.Context) {
	store, ok := s.Store.(service.TriggerNotificationStore)
	if !ok {
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if c, ok := s.Store.(interface{ ProcessTriggerNotificationContexts(context.Context) error }); ok {
				if e := c.ProcessTriggerNotificationContexts(ctx); e != nil && s.Logger != nil {
					s.Logger.Warn("Notification context pending", "event", "trigger.context_pending")
				}
			}
			if err := store.MaterializeTriggerNotifications(ctx); err != nil && s.Logger != nil {
				s.Logger.Warn("Trigger notifications pending", "event", "trigger.notifications_retry")
			}
		}
	}
}
