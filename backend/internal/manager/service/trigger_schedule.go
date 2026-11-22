package service

import (
	"context"
	"encoding/json"
	"time"
)

type TriggerScheduleItem struct {
	ID      string    `json:"id"`
	Version int64     `json:"version,string"`
	NextDue time.Time `json:"next_due"`
}
type TriggerSchedulePoll struct {
	ServerTime time.Time             `json:"server_time"`
	Items      []TriggerScheduleItem `json:"items"`
}
type TriggerScheduleTick struct {
	Version           int64     `json:"version,string"`
	ExpectedDue       time.Time `json:"expected_due"`
	RecoveryStartedAt time.Time `json:"recovery_started_at"`
}
type TriggerScheduleResult struct {
	Status  string          `json:"status"`
	Receipt *TriggerReceipt `json:"receipt,omitempty"`
}
type TriggerScheduleStore interface {
	PollTriggerSchedules(context.Context, int) (TriggerSchedulePoll, error)
	TickTriggerSchedule(context.Context, string, TriggerScheduleTick) (TriggerScheduleResult, error)
}

func ValidateScheduleConfig(raw json.RawMessage) error {
	_, err := ParseTriggerSchedule(raw)
	return err
}
