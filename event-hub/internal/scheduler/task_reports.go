package scheduler

import (
	"context"
	"github.com/unrealm-ai/kikoteam/event-hub/internal/channel"
	"github.com/unrealm-ai/kikoteam/event-hub/internal/election"
	"log/slog"
	"time"
)

type TaskReports struct {
	Backend  *channel.Backend
	Logger   *slog.Logger
	Interval time.Duration
}

func (w *TaskReports) Recover(context.Context, *election.Session) error { return nil }
func (w *TaskReports) Run(ctx context.Context, _ *election.Session) error {
	return runReconciliation(ctx, w.Interval, w.Backend.ReconcileTaskReports, w.Logger, "task.report_reconcile_failed")
}

func runReconciliation(ctx context.Context, interval time.Duration, callBackend func(context.Context) error, logger *slog.Logger, event string) error {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return nil
		}
		call, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := callBackend(call)
		cancel()
		if err != nil && ctx.Err() == nil && logger != nil {
			logger.Warn("Backend reconciliation pending", "event", event)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
