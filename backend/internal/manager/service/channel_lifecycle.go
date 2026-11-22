package service

import "context"

type ChannelLifecycle struct {
	BindingID    string   `json:"binding_id"`
	Generation   int64    `json:"connection_generation"`
	Revision     int64    `json:"config_revision"`
	TenantID     string   `json:"external_tenant_id"`
	ChatID       string   `json:"external_chat_id"`
	EventID      string   `json:"provider_event_id"`
	OccurredAtMS int64    `json:"occurred_at_ms"`
	EventType    string   `json:"event_type"`
	MemberIDs    []string `json:"member_ids"`
}
type ChannelGroupCheck struct {
	ID         string `json:"id"`
	LeaseToken string `json:"lease_token"`
	BindingID  string `json:"binding_id"`
	Generation int64  `json:"connection_generation"`
	Revision   int64  `json:"config_revision"`
	TenantID   string `json:"external_tenant_id"`
	ChatID     string `json:"external_chat_id"`
}
type ChannelGroupCompletion struct {
	LeaseToken string `json:"lease_token"`
	Present    *bool  `json:"present"`
}
type ChannelLifecycleStore interface {
	ReceiveChannelLifecycle(context.Context, ChannelLifecycle) (ChannelReceipt, error)
	ClaimChannelGroupChecks(context.Context) ([]ChannelGroupCheck, error)
	CompleteChannelGroupCheck(context.Context, string, ChannelGroupCompletion) error
}
