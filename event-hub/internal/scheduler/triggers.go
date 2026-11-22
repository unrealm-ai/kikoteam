package scheduler

import (
	"context"
	"errors"
	"github.com/unrealm-ai/kikoteam/event-hub/internal/channel"
	"github.com/unrealm-ai/kikoteam/event-hub/internal/election"
	"log/slog"
	"time"
)

type TriggerScheduleBackend interface {
	PollTriggerSchedules(context.Context) (channel.TriggerSchedules, error)
	TickTriggerSchedule(context.Context, channel.TriggerSchedule, time.Time) error
}

// Manager owns persisted plans and atomic occurrence admission. Leader loss
// cancels this loop; competing/stale ticks are fenced by binding version+due.
type Triggers struct {
	Backend  TriggerScheduleBackend
	Logger   *slog.Logger
	Interval time.Duration
}

func (*Triggers) Recover(context.Context, *election.Session) error { return nil }
func (w *Triggers) Run(ctx context.Context, _ *election.Session) error {
	interval := w.Interval
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var started time.Time
	for {
		if ctx.Err() != nil {
			return nil
		}
		if err := w.tick(ctx, &started); err != nil && ctx.Err() == nil && w.Logger != nil {
			w.Logger.Warn("Trigger schedules pending", "event", "trigger.schedule_retry")
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
func (w *Triggers) tick(ctx context.Context, started *time.Time) error {
	batch, err := w.Backend.PollTriggerSchedules(ctx)
	if err != nil {
		return err
	}
	if batch.ServerTime.IsZero() {
		return errors.New("missing scheduler server time")
	}
	if started.IsZero() {
		*started = batch.ServerTime
	}
	var result error
	for _, item := range batch.Items {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if item.NextDue.After(batch.ServerTime) {
			continue
		}
		if err = w.Backend.TickTriggerSchedule(ctx, item, *started); err != nil {
			result = errors.Join(result, err)
		}
	}
	return result
}
