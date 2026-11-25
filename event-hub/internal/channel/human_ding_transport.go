package channel

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

const dingHumanCardTopic = "/v1.0/card/instances/callback"

func canonicalDingHumanCard(card *humanConfirmationCard) humanDingCardSpec {
	spec := humanDingCardSpec{Kind: "form", Title: card.Title, Description: card.Description}
	if card.DecisionRequired {
		spec.Kind = "approval"
	}
	for _, q := range card.Questions {
		f := humanDingCardField{Name: q.ID, Label: q.Text, Type: q.AnswerType, Required: q.Required}
		for _, o := range q.Options {
			f.Options = append(f.Options, humanDingCardOption{Value: o.ID, Label: o.Label})
		}
		spec.Fields = append(spec.Fields, f)
	}
	return spec
}
func sendDingHumanConfirmation(ctx context.Context, b Binding, d Delivery, fallback deliverySender) deliveryReceipt {
	payload, err := humanDingCardPayload(d.ID, b.BotOpenID, d.RecipientID, canonicalDingHumanCard(d.HumanCard))
	if err != nil {
		return fallback(ctx, b, d)
	}
	h := newDingHTTP()
	token, err := h.token(ctx, b)
	if err != nil {
		return deliveryReceipt{Status: "failed", EvidenceType: "not_dispatched", ErrorCode: "dingtalk_authentication_failed"}
	}
	if d.BeforeDispatch != nil {
		if err = d.BeforeDispatch(); err != nil {
			return deliveryReceipt{Status: "failed", EvidenceType: "not_dispatched", ErrorCode: "binding_changed_before_send"}
		}
	}
	resp, err := h.do(ctx, "POST", "/v1.0/card/instances/createAndDeliver", token, payload)
	unknown := deliveryReceipt{Status: "unknown", ErrorCode: "dingtalk_human_card_unknown"}
	if err != nil {
		return unknown
	}
	defer resp.Body.Close() //nolint:errcheck // Read result is checked below.
	var out struct {
		Code    string `json:"code"`
		Success bool   `json:"success"`
		Result  struct {
			Track    string `json:"outTrackId"`
			Delivery []struct {
				Success bool `json:"success"`
			} `json:"deliverResults"`
		} `json:"result"`
	}
	if err = decodeDingResponse(resp.Body, &out); err != nil {
		return unknown
	}
	if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
		if !out.Success && out.Result.Track == "" {
			return fallback(ctx, b, d)
		}
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && out.Success && out.Result.Track == d.ID && len(out.Result.Delivery) == 1 && out.Result.Delivery[0].Success {
		return deliveryReceipt{Status: "sent", EvidenceType: "send_response", ProviderRequestID: d.ID, EvidenceID: d.ID, HumanConfirmationCard: true}
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && out.Result.Track == d.ID && len(out.Result.Delivery) == 1 && !out.Result.Delivery[0].Success {
		return fallback(ctx, b, d)
	}
	return unknown
}

func handleDingHumanCardEvent(ctx context.Context, b Binding, backend *Backend, frame dingFrame, write func(any) error) error {
	ack := func(data string) error {
		return write(map[string]any{"code": 200, "headers": map[string]string{"contentType": "application/json", "messageId": frame.Headers["messageId"]}, "message": "OK", "data": data})
	}
	const unchanged = `{"response":{}}`
	var ev humanDingCallback
	if len(frame.Data) > 128<<10 || json.Unmarshal([]byte(frame.Data), &ev) != nil || !humanCardNoticeID.MatchString(ev.Track) ||
		!dingID(ev.Actor) || ev.Tenant != b.TenantID || !dingID(frame.Headers["messageId"]) {
		return ack(unchanged)
	}
	action := humanCardAction{ExternalActor: ev.Actor, ProviderCardID: ev.Track}
	work, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	initial, e := nativeHumanCardRequest(work, backend, b, ev.Track, "read-card", action)
	if e != nil || initial.Card == nil {
		return ack(unchanged)
	}
	spec := canonicalDingHumanCard(initial.Card)
	result := initial
	if initial.State == "pending" {
		answer, e := parseHumanDingCardAnswer([]byte(frame.Data), humanDingCardExpectation{TrackID: ev.Track, TenantID: b.TenantID, StaffID: ev.Actor, Spec: spec})
		if e != nil {
			return ack(unchanged)
		}
		action.Decision = answer.Decision
		if action.Decision == "submit" {
			action.Decision = "information"
		}
		for _, f := range spec.Fields {
			values := answer.Values[f.Name]
			if len(values) == 0 {
				continue
			}
			item := humanCardAnswerItem{QuestionID: f.Name}
			if f.Type == "multi_choice" {
				item.OptionIDs = values
			} else {
				item.Value = values[0]
			}
			action.Answers = append(action.Answers, item)
		}
		result, e = submitNativeHumanCard(work, backend, b, ev.Track, action)
		if e != nil {
			return ack(unchanged)
		}
	}
	var settled string
	switch result.State {
	case "approved":
		if spec.Kind == "approval" {
			settled = "approve"
		} else {
			settled = "submit"
		}
	case "rejected":
		settled = "reject"
	default:
		return ack(unchanged)
	}
	params, e := humanDingCardTerminalParams(spec, humanDingCardAnswer{Decision: settled})
	if e != nil {
		return ack(unchanged)
	}
	card := map[string]any{"cardParamMap": params}
	if spec.Kind == "approval" {
		wire, e := json.Marshal(map[string]any{"response": map[string]any{"cardData": card}})
		if e != nil {
			return ack(unchanged)
		}
		return ack(string(wire))
	}
	if e := ack(unchanged); e != nil {
		return e
	}
	update, cancelUpdate := context.WithTimeout(ctx, 5*time.Second)
	defer cancelUpdate()
	h := newDingHTTP()
	tok, e := h.token(update, b)
	if e == nil {
		var out struct {
			Success bool  `json:"success"`
			Result  *bool `json:"result"`
		}
		e = h.request(update, "PUT", "/v1.0/card/instances", tok, map[string]any{"outTrackId": ev.Track, "cardData": card, "cardUpdateOptions": map[string]bool{"updateCardDataByKey": true}}, &out)
		if e == nil && out.Success && (out.Result == nil || *out.Result) {
			return nil
		}
	}
	if backend.Logger != nil {
		backend.Logger.Warn("DingTalk confirmation UI update not confirmed", "event", "human.card_update_failed")
	}
	return nil
}
