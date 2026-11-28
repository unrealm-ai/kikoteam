package controller

import (
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"net/http"
	"slices"
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
		poll, err := store.PollTriggerSchedules(r.Context(), q.Limit)
		if err != nil || r.URL.Query().Get("include_chat") != "true" {
			return poll, err
		}
		if chat, ok := s.Manager.Store.(service.ChatScheduleStore); ok {
			items, e := chat.PollChatSchedules(r.Context(), q.Limit)
			if e != nil {
				return poll, e
			}
			poll = mergeSchedulePoll(poll, items, q.Limit)
		}
		return poll, nil
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
		switch q.Kind {
		case "chat":
			return s.Manager.TickChatSchedule(r.Context(), r.PathValue("id"), q)
		case "":
			return store.TickTriggerSchedule(r.Context(), r.PathValue("id"), q)
		default:
			return nil, service.Fail("invalid_argument", 400, "不支持的定时来源")
		}
	}))
}

func mergeSchedulePoll(p service.TriggerSchedulePoll, chat []service.TriggerScheduleItem, limit int) service.TriggerSchedulePoll {
	p.Items = append(p.Items, chat...)
	slices.SortFunc(p.Items, func(a, b service.TriggerScheduleItem) int {
		if order := a.NextDue.Compare(b.NextDue); order != 0 {
			return order
		}
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	p.Items = p.Items[:min(limit, len(p.Items))]
	return p
}
