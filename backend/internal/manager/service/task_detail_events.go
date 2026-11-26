package service

import (
	"context"
	"encoding/json"
)

type taskDetailEventsStore interface {
	TaskDetailEvents(context.Context, TaskDetailGrant, EventFilter) ([]json.RawMessage, int64, error)
}

func (s *Service) taskDetailEventReader(ctx context.Context, token string) (string, loadEvents, error) {
	_, grant, err := s.taskDetailAccess(ctx, token)
	if err != nil {
		return "", nil, err
	}
	store, ok := s.Store.(taskDetailEventsStore)
	if !ok {
		return "", nil, Fail("configuration_unusable", 503, "任务执行过程暂不可用")
	}
	return "task-detail-execution:" + taskDetailCursorScope(grant.ID), func(f EventFilter) ([]json.RawMessage, int64, error) {
		return store.TaskDetailEvents(ctx, grant, f)
	}, nil
}

func (s *Service) PublicTaskEventHistory(ctx context.Context, token, cursor string) (EventPage, error) {
	scope, load, err := s.taskDetailEventReader(ctx, token)
	if err != nil {
		return EventPage{}, err
	}
	return s.readEventHistory(scope, cursor, 50, load)
}

// PublicTaskEventBatch revalidates the token on each batch, including heartbeat
// polls, and creates reconnect cursors for this exact grant only.
func (s *Service) PublicTaskEventBatch(ctx context.Context, token, after string) ([]json.RawMessage, string, func(int64) (string, error), error) {
	scope, load, err := s.taskDetailEventReader(ctx, token)
	if err != nil {
		return nil, "", nil, err
	}
	items, next, err := s.readEventStream(scope, after, load)
	return items, next, func(seq int64) (string, error) {
		return s.encodeCursor(cursor{Scope: scope, Kind: "stream", Seq: seq})
	}, err
}
