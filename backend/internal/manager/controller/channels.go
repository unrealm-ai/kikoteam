package controller

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"log/slog"
	"net/http"
	"time"
)

type ChannelServer struct {
	Store   service.ChannelStore
	Manager *service.Service
	Token   string
	Logger  *slog.Logger
}

func (s *ChannelServer) Routes(mux *http.ServeMux) {
	s.mediaRoutes(mux)
	s.reactionRoutes(mux)
	s.triggerScheduleRoutes(mux)
	s.sharedGroupHistoryRoutes(mux)
	s.notificationContextRoutes(mux)
	s.lifecycleRoutes(mux)
	s.humanNoticeRoutes(mux)
	mux.HandleFunc("POST /api/internal/v1/task-results/reconcile", s.wrap(s.reconcileReports))
	mux.HandleFunc("POST /api/internal/v1/channel-history/claim", s.wrap(func(r *http.Request) (any, error) {
		var v struct {
			Limit int `json:"limit"`
		}
		if e := decode(r, &v); e != nil {
			return nil, e
		}
		items, e := s.Store.ClaimChannelHistory(r.Context(), v.Limit)
		return map[string]any{"items": items}, e
	}))
	mux.HandleFunc("POST /api/internal/v1/channel-history/{id}/authorize", s.wrap(func(r *http.Request) (any, error) {
		var v struct {
			LeaseToken string `json:"lease_token"`
		}
		if e := decode(r, &v); e != nil {
			return nil, e
		}
		return s.Store.AuthorizeChannelHistory(r.Context(), r.PathValue("id"), v.LeaseToken)
	}))
	mux.HandleFunc("POST /api/internal/v1/channel-history/{id}/complete", s.wrap(func(r *http.Request) (any, error) {
		var v service.ChannelHistoryCompletion
		if e := decode(r, &v); e != nil {
			return nil, e
		}
		e := s.Store.CompleteChannelHistory(r.Context(), r.PathValue("id"), v)
		return map[string]bool{"accepted": e == nil}, e
	}))
	mux.HandleFunc("POST /api/internal/v1/channel-inbox", s.wrap(func(r *http.Request) (any, error) {
		var v service.ChannelInbound
		if e := decode(r, &v); e != nil {
			return nil, e
		}
		return s.Store.ReceiveChannel(r.Context(), v)
	}))

	mux.HandleFunc("POST /api/internal/v1/channel-delivery-queries/claim", s.wrap(func(r *http.Request) (any, error) {
		var v struct {
			Limit int `json:"limit"`
		}
		if e := decode(r, &v); e != nil {
			return nil, e
		}
		items, e := s.Store.ClaimChannelDeliveryQueries(r.Context(), v.Limit)
		return map[string]any{"items": items}, e
	}))
	mux.HandleFunc("POST /api/internal/v1/channel-deliveries/claim", s.wrap(func(r *http.Request) (any, error) {
		var v struct {
			Limit int `json:"limit"`
		}
		if e := decode(r, &v); e != nil {
			return nil, e
		}
		items, e := s.Store.ClaimChannelDeliveries(r.Context(), v.Limit, r.Header.Get("X-Kiko-Delivery-Markdown") == "1", r.Header.Get("X-Kiko-Delivery-Media") == "1")
		return map[string]any{"items": items}, e
	}))
	mux.HandleFunc("POST /api/internal/v1/channel-deliveries/{id}/authorize", s.wrap(func(r *http.Request) (any, error) {
		var v struct {
			LeaseToken string `json:"lease_token"`
		}
		if e := decode(r, &v); e != nil {
			return nil, e
		}
		return s.Store.AuthorizeChannelDelivery(r.Context(), r.PathValue("id"), v.LeaseToken)
	}))
	mux.HandleFunc("POST /api/internal/v1/channel-deliveries/{id}/complete", s.wrap(func(r *http.Request) (any, error) {
		var v service.DeliveryCompletion
		if e := decode(r, &v); e != nil {
			return nil, e
		}
		e := s.Store.CompleteChannelDelivery(r.Context(), r.PathValue("id"), v)
		return map[string]bool{"accepted": e == nil}, e
	}))
}
func (s *ChannelServer) wrap(op func(*http.Request) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if r.Header.Get("Cookie") != "" || r.Header.Get("Origin") != "" || len(s.Token) < 32 || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.Token)) != 1 {
			w.WriteHeader(401)
			return
		}
		limit := int64(1 << 20)
		if r.Pattern == "PUT /api/internal/v1/channel-media/{id}/{ordinal}" {
			limit = service.ProjectUploadLimit + 1
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		v, e := op(r.WithContext(ctx))
		status := 200
		if e != nil {
			status = 503
			code := "dependency_unavailable"
			var problem *service.Error
			if errors.As(e, &problem) {
				status = problem.Status
				code = problem.Code
			}
			v = map[string]any{"error": map[string]string{"code": code}}
		}
		w.WriteHeader(status)
		if e = json.NewEncoder(w).Encode(v); e != nil && s.Logger != nil {
			s.Logger.Debug("Channel response closed", "event", "channel.response_closed")
		}
	}
}
func (s *ChannelServer) Run(ctx context.Context) {
	if len(s.Token) < 32 {
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:

			for n := 0; n < 20; n++ {
				if e := s.Store.ProcessChannelInbox(ctx, s.Manager); e != nil {
					if s.Logger != nil {
						s.Logger.Warn("Channel inbox pending", "event", "channel.inbox_retry")
					}
					break
				}
			}
			if e := s.Store.MaterializeChannelReplies(ctx); e != nil && s.Logger != nil {
				s.Logger.Warn("Channel replies pending", "event", "channel.reply_retry")
			}
		}
	}
}
