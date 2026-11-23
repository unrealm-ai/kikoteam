package service

type ChannelReactionClaim struct {
	InboxID    string `json:"inbox_id"`
	ClaimToken string `json:"claim_token"`
}

type ChannelReactionTarget struct {
	BindingID  string `json:"binding_id"`
	Provider   string `json:"provider"`
	Generation int64  `json:"connection_generation"`
	TenantID   string `json:"tenant_id"`
	BotID      string `json:"bot_id"`
	ChatID     string `json:"chat_id"`
	MessageID  string `json:"message_id"`
}
