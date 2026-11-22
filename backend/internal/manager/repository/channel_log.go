package repository

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"unicode"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func safeChannelLogID(value string) string {
	if len(value) > 256 || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return "invalid_identifier"
	}
	return value
}

func channelErrorCode(err error) string {
	var problem *service.Error
	switch {
	case errors.As(err, &problem):
		return problem.Code
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		return "context_cancelled"
	default:
		return "dependency_unavailable"
	}
}

func (s *Store) channelLog(ctx context.Context, event string, fields ...any) {
	if s.ChannelLog == nil {
		return
	}
	level := slog.LevelInfo
	switch event {
	case "channel.inbox_processing_failed":
		level = slog.LevelWarn
	case "channel.inbox_admission_failed":
		for i := 0; i+1 < len(fields); i += 2 {
			if fields[i] == "error_code" && (fields[i+1] == "dependency_unavailable" || fields[i+1] == "deadline_exceeded") {
				level = slog.LevelWarn
			}
		}
	}
	s.ChannelLog.Log(ctx, level, "Channel message lifecycle", append([]any{"event", event}, fields...)...)
}

func (s *Store) channelInboxLog(ctx context.Context, w inboxWork, event string, fields ...any) {
	s.channelLog(ctx, event, append([]any{"provider", w.Provider, "binding_id", w.Binding, "inbox_id", w.ID}, fields...)...)
}
