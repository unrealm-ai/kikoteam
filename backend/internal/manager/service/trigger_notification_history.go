package service

import "context"

type NotificationContextCompletion struct {
	LeaseToken string                 `json:"lease_token"`
	Snapshot   ChannelHistorySnapshot `json:"snapshot"`
	AnchorID   string                 `json:"anchor_message_id"`
	AnchorTime int64                  `json:"anchor_created_at_ms"`
	ChatID     string                 `json:"chat_id"`
	ThreadID   string                 `json:"thread_id"`
}
type NotificationContextStore interface {
	ClaimNotificationContexts(context.Context, int) ([]ChannelHistoryJob, error)
	AuthorizeNotificationContext(context.Context, string, string) (ChannelHistoryJob, error)
	CompleteNotificationContext(context.Context, string, NotificationContextCompletion) error
}
