package service

import "context"

// A selector names an existing authenticated employee group conversation. It
// never accepts caller-supplied provider, tenant, credential or recipient IDs.
type TriggerNotificationTarget struct {
	ConversationID string `json:"conversation_id"`
}
type TriggerNotificationStore interface {
	MaterializeTriggerNotifications(context.Context) error
	ClaimTriggerNotifications(context.Context, int) ([]ChannelDelivery, error)
	AuthorizeTriggerNotification(context.Context, string, string) (ChannelDelivery, error)
	CompleteTriggerNotification(context.Context, string, DeliveryCompletion) error
	ClaimTriggerNotificationQueries(context.Context, int) ([]ChannelDelivery, error)
}
