package service

import "context"

// Provider presentation data only; never an authorization principal.
type ChannelMention struct {
	ID        string `json:"id"`
	Key       string `json:"key"`
	Name      string `json:"name,omitempty"`
	IDType    string `json:"id_type,omitempty"`
	TenantKey string `json:"tenant_key,omitempty"`
}
type ChannelMessageMetadata struct {
	ReplyMessageID string           `json:"reply_message_id,omitempty"`
	SenderName     string           `json:"sender_name,omitempty"`
	SenderTenant   string           `json:"sender_tenant,omitempty"`
	SenderIDType   string           `json:"sender_id_type,omitempty"`
	Mentions       []ChannelMention `json:"mentions,omitempty"`
	QuotedText     string           `json:"quoted_text,omitempty"`
}

type MediaReference struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Locator string `json:"locator"`
}
type MediaJob struct {
	MediaReference
	InboxID    string `json:"inbox_id"`
	Ordinal    int    `json:"ordinal"`
	Lease      string `json:"lease_token"`
	BindingID  string `json:"binding_id"`
	Generation int64  `json:"connection_generation"`
	Provider   string `json:"provider"`
	MessageID  string `json:"message_id"`
}
type ChannelInbound struct {
	Media                []MediaReference `json:"media,omitempty"`
	ReplyMessageID       string           `json:"reply_message_id,omitempty"`
	ControlEligible      bool             `json:"control_eligible,omitempty"`
	ChatName             string           `json:"chat_name,omitempty"`
	BotID                string           `json:"bot_id,omitempty"`
	ProviderDirected     bool             `json:"provider_directed,omitempty"`
	CreatedAtMS          int64            `json:"created_at_ms,omitempty"`
	BindingID            string           `json:"binding_id"`
	ConnectionGeneration int64            `json:"connection_generation"`
	ConfigRevision       int64            `json:"config_revision"`
	RulesVersion         int64            `json:"rules_version"`
	ProviderEventID      string           `json:"provider_event_id"`
	ProviderMessageID    string           `json:"provider_message_id"`
	ExternalTenantID     string           `json:"external_tenant_id"`
	ExternalChatID       string           `json:"external_chat_id"`
	ChatType             string           `json:"chat_type"`
	SenderID             string           `json:"sender_id"`
	Text                 string           `json:"text"`
	QuotedText           string           `json:"quoted_text,omitempty"`
	MessageType          string           `json:"message_type"`
	BotMentioned         bool             `json:"bot_mentioned"`
	Mentions             []ChannelMention `json:"mentions,omitempty"`
	SenderName           string           `json:"sender_name,omitempty"`
	SenderTenant         string           `json:"sender_tenant,omitempty"`
	SenderIDType         string           `json:"sender_id_type,omitempty"`
	ThreadID             string           `json:"thread_id,omitempty"`
}

// WecomControlReply is an instruction to the Hub to use its fixed reply
// on the authenticated WeCom callback. It never carries provider or user text.
type WecomControlReply string

const (
	WecomControlVerified WecomControlReply = "verified"
	WecomControlUsed     WecomControlReply = "used"
	WecomControlExpired  WecomControlReply = "expired"
	WecomControlRevoked  WecomControlReply = "revoked"
)

// A zero result is not a control message. Handled with no reply is a
// suppressed or duplicate control event that must never enter the Inbox.
type WecomControlResult struct {
	Handled bool
	Reply   WecomControlReply
}

func (v WecomControlResult) Valid() bool {
	switch v.Reply {
	case "":
		return true
	case WecomControlVerified, WecomControlUsed, WecomControlExpired, WecomControlRevoked:
		return v.Handled
	default:
		return false
	}
}

type ChannelReceipt struct {
	ID           string            `json:"id"`
	Status       string            `json:"status"`
	ControlReply WecomControlReply `json:"control_reply,omitempty"`
}
type ChannelDelivery struct {
	ArtifactID           string `json:"artifact_id,omitempty"`
	ConfigRevision       int64  `json:"config_revision,omitempty"`
	Provider             string `json:"provider"`
	ExternalTenantID     string `json:"external_tenant_id"`
	ChatType             string `json:"chat_type"`
	RecipientID          string `json:"recipient_id"`
	ProviderRequestID    string `json:"provider_request_id,omitempty"`
	TargetDigest         string `json:"target_digest"`
	QueryToken           string `json:"query_token,omitempty"`
	ID                   string `json:"id"`
	LeaseToken           string `json:"lease_token"`
	BindingID            string `json:"binding_id"`
	ConnectionGeneration int64  `json:"connection_generation"`
	ChatID               string `json:"chat_id"`
	ReplyToMessageID     string `json:"reply_to_message_id"`
	ThreadID             string `json:"thread_id"`
	Text                 string `json:"text"`
	Format               string `json:"format,omitempty"`
	IdempotencyKey       string `json:"idempotency_key"`
	Allowed              bool   `json:"allowed"`
}
type DeliveryCompletion struct {
	ProviderCreatedAtMS int64  `json:"provider_created_at_ms,string,omitempty"`
	ProviderChatID      string `json:"provider_chat_id,omitempty"`
	ProviderThreadID    string `json:"provider_thread_id,omitempty"`
	ProviderRequestID   string `json:"provider_request_id,omitempty"`
	TargetDigest        string `json:"target_digest"`
	QueryToken          string `json:"query_token,omitempty"`
	EvidenceType        string `json:"evidence_type,omitempty"`
	EvidenceID          string `json:"evidence_id,omitempty"`
	ProviderSessionID   string `json:"provider_session_id,omitempty"`
	LeaseToken          string `json:"lease_token"`
	Status              string `json:"status"`
	ProviderMessageID   string `json:"provider_message_id"`
	ErrorCode           string `json:"error_code"`
}
type ChannelStore interface {
	ClaimChannelHistory(context.Context, int) ([]ChannelHistoryJob, error)
	AuthorizeChannelHistory(context.Context, string, string) (ChannelHistoryJob, error)
	CompleteChannelHistory(context.Context, string, ChannelHistoryCompletion) error
	ReceiveChannel(context.Context, ChannelInbound) (ChannelReceipt, error)
	ProcessChannelInbox(context.Context, *Service) error
	MaterializeChannelReplies(context.Context) error
	ClaimChannelDeliveryQueries(context.Context, int) ([]ChannelDelivery, error)
	ClaimChannelDeliveries(ctx context.Context, limit int, supportsMarkdown bool, supportsMedia ...bool) ([]ChannelDelivery, error)
	AuthorizeChannelDelivery(context.Context, string, string) (ChannelDelivery, error)
	CompleteChannelDelivery(context.Context, string, DeliveryCompletion) error
}

type ChannelHistoryJob struct {
	TriggerFormat              string `json:"trigger_format,omitempty"`
	TriggerText                string `json:"trigger_text"`
	PreviousTriggerMessageID   string `json:"previous_trigger_message_id"`
	PreviousTriggerCreatedAtMS int64  `json:"previous_trigger_created_at_ms"`
	ExternalTenantID           string `json:"external_tenant_id"`
	ID                         string `json:"id"`
	LeaseToken                 string `json:"lease_token"`
	BindingID                  string `json:"binding_id"`
	ConnectionGeneration       int64  `json:"connection_generation"`
	ConfigRevision             int64  `json:"config_revision"`
	RulesVersion               int64  `json:"rules_version"`
	ChatID                     string `json:"chat_id"`
	ThreadID                   string `json:"thread_id"`
	TriggerMessageID           string `json:"trigger_message_id"`
	TriggerCreatedAtMS         int64  `json:"trigger_created_at_ms"`
	Limit                      int    `json:"limit"`
	Strategy                   string `json:"strategy"`
	Allowed                    bool   `json:"allowed"`
}
type ChannelHistoryItem struct {
	RecordKey    string           `json:"record_key,omitempty"`
	SenderName   string           `json:"sender_name,omitempty"`
	SenderTenant string           `json:"sender_tenant,omitempty"`
	SenderIDType string           `json:"sender_id_type,omitempty"`
	Mentions     []ChannelMention `json:"mentions,omitempty"`
	SenderType   string           `json:"sender_type,omitempty"`
	MessageType  string           `json:"message_type,omitempty"`
	MessageID    string           `json:"message_id"`
	SenderID     string           `json:"sender_id"`
	Text         string           `json:"text"`
	CreatedAtMS  int64            `json:"created_at_ms"`
}
type ChannelHistorySnapshot struct {
	Digest    string               `json:"digest,omitempty"`
	Truncated bool                 `json:"truncated,omitempty"`
	Status    string               `json:"status"`
	Items     []ChannelHistoryItem `json:"items"`
	ErrorCode string               `json:"error_code"`
}
type ChannelHistoryCompletion struct {
	LeaseToken string                 `json:"lease_token"`
	Snapshot   ChannelHistorySnapshot `json:"snapshot"`
}
