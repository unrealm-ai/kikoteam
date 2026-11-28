package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

type TriggerScheduleItem struct {
	Kind    string    `json:"kind,omitempty"`
	ID      string    `json:"id"`
	Version int64     `json:"version,string"`
	NextDue time.Time `json:"next_due"`
}
type TriggerSchedulePoll struct {
	ServerTime time.Time             `json:"server_time"`
	Items      []TriggerScheduleItem `json:"items"`
}
type TriggerScheduleTick struct {
	Kind              string    `json:"kind,omitempty"`
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

// ScheduleAdmissionSkip marks a deterministic denial. Temporary backend
// failures must leave the occurrence due for a retry, not consume it.
func ScheduleAdmissionSkip(err error) string {
	var e *Error
	if errors.As(err, &e) && e.Status >= 400 && e.Status < 500 && e.Status != 408 && e.Status != 429 {
		return e.Code
	}
	return ""
}
