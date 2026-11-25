package channel

import (
	"context"
	"reflect"
	"regexp"
)

var humanCardNoticeID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type humanConfirmationCard struct {
	Title            string                      `json:"title"`
	Description      string                      `json:"description"`
	DecisionRequired bool                        `json:"decision_required"`
	Questions        []humanConfirmationQuestion `json:"questions,omitempty"`
}
type humanConfirmationQuestion struct {
	ID         string                    `json:"id"`
	Text       string                    `json:"text"`
	Required   bool                      `json:"required"`
	AnswerType string                    `json:"answerType"`
	Options    []humanConfirmationOption `json:"options,omitempty"`
}
type humanConfirmationOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}
type humanCardAnswerItem struct {
	QuestionID string   `json:"questionId"`
	Value      string   `json:"value,omitempty"`
	OptionIDs  []string `json:"optionIds,omitempty"`
}
type humanCardAction struct {
	BindingID      string                `json:"binding_id"`
	Provider       string                `json:"provider"`
	ExternalTenant string                `json:"tenant_id"`
	ExternalActor  string                `json:"actor_id"`
	Generation     int64                 `json:"connection_generation"`
	Revision       int64                 `json:"config_revision"`
	ProviderCardID string                `json:"provider_card_id"`
	ProviderChatID string                `json:"provider_chat_id,omitempty"`
	Decision       string                `json:"decision"`
	Text           string                `json:"text,omitempty"`
	Answers        []humanCardAnswerItem `json:"answers,omitempty"`
}
type humanCardAnswerResult struct {
	State string                 `json:"state"`
	Card  *humanConfirmationCard `json:"card"`
}

func sameHumanCardTarget(a, b *humanConfirmationCard) bool { return reflect.DeepEqual(a, b) }

func submitNativeHumanCard(ctx context.Context, backend *Backend, b Binding, notice string, answer humanCardAction) (humanCardAnswerResult, error) {
	return nativeHumanCardRequest(ctx, backend, b, notice, "answer-card", answer)
}

func nativeHumanCardRequest(ctx context.Context, backend *Backend, b Binding, notice, operation string, answer humanCardAction) (humanCardAnswerResult, error) {
	answer.BindingID, answer.Provider, answer.ExternalTenant = b.ID, b.Provider, b.TenantID
	answer.Generation, answer.Revision = b.Generation, b.Revision
	var out humanCardAnswerResult
	err := backend.request(ctx, "POST", "/api/internal/v1/human-notices/"+notice+"/"+operation, answer, &out)
	return out, err
}
