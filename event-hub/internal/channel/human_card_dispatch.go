package channel

import (
	"context"

	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
)

func sendHumanConfirmationCard(ctx context.Context, b Binding, d Delivery, send deliverySender) deliveryReceipt {
	if d.ChatType != "direct" || !humanCardNoticeID.MatchString(d.ID) {
		return send(ctx, b, d)
	}
	switch b.Provider {
	case "dingtalk":
		return sendDingHumanConfirmation(ctx, b, d, send)
	case "wecom":
		content, ok := renderWecomHumanCard(d.HumanCard, d.ID)
		if !ok {
			return send(ctx, b, d)
		}
		native := d
		native.Format, native.humanCardContent = "human_confirmation_card", content
		result := send(ctx, b, native)
		if result.Status == "failed" && result.EvidenceType == "send_response" && result.ErrorCode == taskDetailRejected {
			return send(ctx, b, d)
		}
		result.HumanConfirmationCard = result.Status == "sent"
		return result
	case "feishu":
	default:
		return send(ctx, b, d)
	}
	spec := feishuHumanCardSpec{Token: d.ID, Summary: d.HumanCard.Description, DecisionRequired: d.HumanCard.DecisionRequired}
	for _, q := range d.HumanCard.Questions {
		item := feishuHumanQuestion{ID: q.ID, Text: q.Text, AnswerType: q.AnswerType, Required: q.Required}
		for _, o := range q.Options {
			item.Options = append(item.Options, feishuHumanOption(o))
		}
		spec.Questions = append(spec.Questions, item)
	}
	content, ok := buildFeishuHumanCard(spec)
	if !ok {
		return send(ctx, b, d)
	}
	native := d
	native.Format, native.humanCardContent = "human_confirmation_card", content
	result := send(ctx, b, native)
	if result.Status == "failed" && result.EvidenceType == "send_response" && result.ErrorCode == taskDetailRejected {
		return send(ctx, b, d)
	}
	result.HumanConfirmationCard = result.Status == "sent"
	return result
}

func registerFeishuHumanCardHandler(handler *dispatcher.EventDispatcher, b Binding, backend *Backend) {
	handler.OnP2CardActionTrigger(func(ctx context.Context, e *callback.CardActionTriggerEvent) (*callback.CardActionTriggerResponse, error) {
		action, ok := parseFeishuHumanCardEvent(b, e)
		if !ok || !humanCardNoticeID.MatchString(action.Token) {
			return &callback.CardActionTriggerResponse{}, nil
		}
		answer := humanCardAction{ExternalActor: action.ActorID, ProviderCardID: action.MessageID, ProviderChatID: action.ChatID, Decision: action.Decision, Text: action.Text}
		if answer.Decision == "submit" {
			answer.Decision = "information"
		}
		for _, v := range action.Answers {
			answer.Answers = append(answer.Answers, humanCardAnswerItem(v))
		}
		result, err := submitNativeHumanCard(ctx, backend, b, action.Token, answer)
		if err != nil || result.Card == nil {
			return &callback.CardActionTriggerResponse{Toast: &callback.Toast{Type: "warning", Content: "无法确认本次提交，请刷新卡片或用文字核对。"}}, nil
		}
		decision := ""
		switch result.State {
		case "approved":
			if result.Card.DecisionRequired {
				decision = "approve"
			} else {
				decision = "information"
			}
		case "rejected":
			decision = "reject"
		case "revised", "cancelled":
		default:
			return &callback.CardActionTriggerResponse{Toast: &callback.Toast{Type: "warning", Content: "请检查必答项后提交。"}}, nil
		}
		return &callback.CardActionTriggerResponse{Card: completeFeishuHumanCard(feishuHumanCardSpec{Summary: result.Card.Description}, decision, "")}, nil
	})
}
