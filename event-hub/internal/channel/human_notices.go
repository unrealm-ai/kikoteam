package channel

import (
	"context"
	"log/slog"
	"time"

	"github.com/unrealm-ai/kikoteam/event-hub/internal/election"
)

type HumanNotices struct {
	Backend *Backend
	Lookup  func(string) (Binding, bool)
	Logger  *slog.Logger
}

func (*HumanNotices) Recover(context.Context, *election.Session) error { return nil }
func (w *HumanNotices) Run(ctx context.Context, _ *election.Session) error {
	b := *w.Backend
	b.humanNoticeDelivery = true
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		if err := RunDeliveries(ctx, &b, w.Lookup); err != nil && ctx.Err() == nil && w.Logger != nil {
			w.Logger.Warn("Human confirmation notice delivery pending", "event", "human.notice_delivery_retry")
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
	return nil
}
