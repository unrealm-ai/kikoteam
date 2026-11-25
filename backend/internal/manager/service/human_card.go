package service

// HumanCardAction is accepted only from the authenticated channel Hub after
// verifying the provider callback. The answer identity is derived from the
// original delivery on the Manager side, never from model output.
type HumanCardAction struct {
	BindingID      string            `json:"binding_id"`
	Provider       string            `json:"provider"`
	ExternalTenant string            `json:"tenant_id"`
	ExternalActor  string            `json:"actor_id"`
	Generation     int64             `json:"connection_generation"`
	Revision       int64             `json:"config_revision"`
	ProviderCardID string            `json:"provider_card_id"`
	ProviderChatID string            `json:"provider_chat_id,omitempty"`
	Decision       string            `json:"decision"`
	Text           string            `json:"text,omitempty"`
	Answers        []HumanAnswerItem `json:"answers,omitempty"`
}

// HumanConfirmationCard carries the canonical question, not provider markup.
type HumanConfirmationCard struct {
	Title            string              `json:"title"`
	Description      string              `json:"description"`
	DecisionRequired bool                `json:"decision_required"`
	Questions        []HumanQuestionItem `json:"questions,omitempty"`
	Compact          *HumanCardCompact   `json:"compact,omitempty"`
}

// HumanCardCompact is the complete display context without SMS-style
// directions or duplication of the native title, choices and actions.
type HumanCardCompact struct {
	Reference string `json:"reference"`
	Body      string `json:"body"`
}
