package service

import (
	"context"
	"time"
)

type TriggerNotificationCandidate struct {
	ConversationID string `json:"conversation_id"`
	Provider       string `json:"provider"`
	Label          string `json:"label"`
}
type TriggerNotificationView struct {
	ReminderEventID *string   `json:"reminder_event_id"`
	ID              string    `json:"id"`
	TaskID          *string   `json:"task_id"`
	SourceInputID   *string   `json:"source_input_id"`
	SourceTurnID    *string   `json:"source_turn_id"`
	ConversationID  string    `json:"conversation_id"`
	Status          string    `json:"status"`
	ContextStatus   string    `json:"context_status"`
	ContextError    *string   `json:"context_error"`
	ErrorCode       *string   `json:"error_code"`
	CreatedAt       time.Time `json:"created_at"`
}
type TriggerNotificationReads interface {
	TriggerNotificationCandidates(context.Context, string, string, string, int) ([]TriggerNotificationCandidate, bool, error)
	TriggerNotificationViews(context.Context, string, string, string, string, int) ([]TriggerNotificationView, bool, error)
}
