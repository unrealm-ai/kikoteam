package channel

import (
	"context"
	"net/url"
	"time"
)

type TriggerSchedule struct {
	ID      string    `json:"id"`
	Version int64     `json:"version,string"`
	NextDue time.Time `json:"next_due"`
}
type TriggerSchedules struct {
	ServerTime time.Time         `json:"server_time"`
	Items      []TriggerSchedule `json:"items"`
}

func (b *Backend) PollTriggerSchedules(ctx context.Context) (TriggerSchedules, error) {
	var out TriggerSchedules
	err := b.request(ctx, "POST", "/api/internal/v1/trigger-schedules/poll", map[string]int{"limit": 100}, &out)
	return out, err
}
func (b *Backend) TickTriggerSchedule(ctx context.Context, item TriggerSchedule, started time.Time) error {
	return b.request(ctx, "POST", "/api/internal/v1/trigger-schedules/"+url.PathEscape(item.ID)+"/tick", struct {
		Version           int64     `json:"version,string"`
		ExpectedDue       time.Time `json:"expected_due"`
		RecoveryStartedAt time.Time `json:"recovery_started_at"`
	}{item.Version, item.NextDue, started}, nil)
}
