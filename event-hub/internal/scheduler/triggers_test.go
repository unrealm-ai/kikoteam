package scheduler

import (
	"context"
	"errors"
	"github.com/unrealm-ai/kikoteam/event-hub/internal/channel"
	"testing"
	"time"
)

type scheduleProbe struct {
	batch channel.TriggerSchedules
	seen  []time.Time
	fail  bool
}

func (p *scheduleProbe) PollTriggerSchedules(context.Context) (channel.TriggerSchedules, error) {
	return p.batch, nil
}
func (p *scheduleProbe) TickTriggerSchedule(_ context.Context, _ channel.TriggerSchedule, start time.Time) error {
	p.seen = append(p.seen, start)
	if p.fail {
		return errors.New("retry")
	}
	return nil
}
func TestScheduleRecoveryEpochAndRetry(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	p := &scheduleProbe{batch: channel.TriggerSchedules{ServerTime: now, Items: []channel.TriggerSchedule{{ID: "due", Version: 1, NextDue: now.Add(-time.Minute)}, {ID: "future", Version: 1, NextDue: now.Add(time.Minute)}}}, fail: true}
	w := Triggers{Backend: p}
	var start time.Time
	if err := w.tick(context.Background(), &start); err == nil {
		t.Fatal("failure hidden")
	}
	p.batch.ServerTime = now.Add(time.Second)
	p.fail = false
	if err := w.tick(context.Background(), &start); err != nil {
		t.Fatal(err)
	}
	if len(p.seen) != 2 || !p.seen[0].Equal(now) || !p.seen[1].Equal(now) {
		t.Fatal("restart epoch advanced during retry", p.seen)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := w.tick(ctx, &start); err == nil {
		t.Fatal("canceled leader ticked")
	}
}
