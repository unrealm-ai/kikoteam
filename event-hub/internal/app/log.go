package app

import (
	"context"
	"errors"
	"log/slog"
	"os"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pressly/goose/v3"
	"github.com/unrealm-ai/kikoteam/event-hub/database/postgres"
	"github.com/unrealm-ai/kikoteam/event-hub/internal/election"
)

func NewLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
		if a.Key == slog.TimeKey {
			a.Value = slog.TimeValue(a.Value.Time().UTC())
		}
		return a
	}})).With("service", "event-hub")
}

// ErrorAttrs intentionally excludes error messages: drivers and migration errors
// can contain credentials, SQL statements or data values.
func ErrorAttrs(err error) []any {
	code := "operation_failed"
	if errors.Is(err, context.Canceled) {
		code = "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		code = "timeout"
	}
	if errors.Is(err, election.ErrLost) {
		code = "leader_session_lost"
	}
	if errors.Is(err, ErrShutdownTimeout) {
		code = "shutdown_timeout"
	}
	var preparation *goose.PreparationError
	if errors.As(err, &preparation) {
		code = "migration_parse_failed"
	}
	var history *postgres.HistoryError
	if errors.As(err, &history) {
		code = "migration_history_missing"
	}
	attrs := []any{"error_code", code}
	if history != nil {
		attrs = append(attrs, "migration_version", history.Version)
	}
	if preparation != nil {
		attrs = append(attrs, "migration_version", preparation.Version)
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		attrs = append(attrs, "sqlstate", pg.Code)
	}
	var partial *goose.PartialError
	if errors.As(err, &partial) && partial.Failed != nil {
		attrs = append(attrs, "migration_version", partial.Failed.Source.Version)
	}
	return attrs
}
