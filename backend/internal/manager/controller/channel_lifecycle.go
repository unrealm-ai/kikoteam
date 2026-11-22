package controller

import (
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"net/http"
)

func (s *ChannelServer) lifecycleRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/internal/v1/channel-lifecycle", s.wrap(func(r *http.Request) (any, error) {
		store, ok := s.Store.(service.ChannelLifecycleStore)
		if !ok {
			return nil, service.Fail("unavailable", 503, "生命周期存储不可用")
		}
		var v service.ChannelLifecycle
		if err := decode(r, &v); err != nil {
			return nil, err
		}
		return store.ReceiveChannelLifecycle(r.Context(), v)
	}))
	mux.HandleFunc("POST /api/internal/v1/channel-groups/claim", s.wrap(func(r *http.Request) (any, error) {
		store, ok := s.Store.(service.ChannelLifecycleStore)
		if !ok {
			return nil, service.Fail("unavailable", 503, "生命周期存储不可用")
		}
		items, err := store.ClaimChannelGroupChecks(r.Context())
		return map[string]any{"items": items}, err
	}))
	mux.HandleFunc("POST /api/internal/v1/channel-groups/{id}/complete", s.wrap(func(r *http.Request) (any, error) {
		store, ok := s.Store.(service.ChannelLifecycleStore)
		if !ok {
			return nil, service.Fail("unavailable", 503, "生命周期存储不可用")
		}
		var v service.ChannelGroupCompletion
		if err := decode(r, &v); err != nil {
			return nil, err
		}
		err := store.CompleteChannelGroupCheck(r.Context(), r.PathValue("id"), v)
		return map[string]bool{"accepted": err == nil}, err
	}))
}
