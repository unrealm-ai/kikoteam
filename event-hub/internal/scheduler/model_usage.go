package scheduler

import (
	"context"
	"log/slog"

	"github.com/unrealm-ai/kikoteam/event-hub/internal/channel"
	"github.com/unrealm-ai/kikoteam/event-hub/internal/election"
)

// ModelUsage triggers Backend settlement while this Event Hub holds leadership.
type ModelUsage struct {
	Backend *channel.Backend
	Logger  *slog.Logger
}

func (*ModelUsage) Recover(context.Context, *election.Session) error { return nil }
func (w *ModelUsage) Run(ctx context.Context, _ *election.Session) error {
	return runReconciliation(ctx, 0, w.Backend.ReconcileModelUsage, w.Logger, "model_proxy.reconcile_failed")
}
