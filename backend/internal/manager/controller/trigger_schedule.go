package controller

import (
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"net/http"
)

func (s *ChannelServer) triggerScheduleRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/internal/v1/trigger-schedules/poll", s.wrap(func(r *http.Request) (any, error) {
		var q struct {
			Limit int `json:"limit"`
		}
		if err := decode(r, &q); err != nil {
			return nil, err
		}
		store, ok := s.Manager.Store.(service.TriggerScheduleStore)
		if !ok {
			return nil, service.Fail("dependency_unavailable", 503, "定时服务不可用")
		}
		return store.PollTriggerSchedules(r.Context(), q.Limit)
	}))
	mux.HandleFunc("POST /api/internal/v1/trigger-schedules/{id}/tick", s.wrap(func(r *http.Request) (any, error) {
		var q service.TriggerScheduleTick
		if err := decode(r, &q); err != nil {
			return nil, err
		}
		store, ok := s.Manager.Store.(service.TriggerScheduleStore)
		if !ok {
			return nil, service.Fail("dependency_unavailable", 503, "定时服务不可用")
		}
		return store.TickTriggerSchedule(r.Context(), r.PathValue("id"), q)
	}))
}
