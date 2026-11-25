package channel

import (
	"context"
	"time"
)

func (s *wecomSession) handleWecomNativeConfirmation(ctx context.Context, backend *Backend, f wecomFrame) {
	event, ok := parseWecomHumanCardEvent(f, s.binding)
	if !ok || !humanCardNoticeID.MatchString(event.TaskID) || !validWecomID(f.Headers.ReqID) {
		return
	}
	work, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	result, err := submitNativeHumanCard(work, backend, s.binding, event.TaskID, humanCardAction{ExternalActor: event.ActorID, ProviderCardID: event.TaskID, Decision: event.Decision, Answers: event.Answers})
	if err != nil || result.Card == nil {
		return
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
	default:
		return
	}
	body, ok := renderWecomHumanCardUpdate(event.TaskID, event.ActorID, decision)
	if !ok {
		return
	}
	if err = s.write(work, "aibot_respond_update_msg", f.Headers.ReqID, body); err != nil && backend.Logger != nil {
		backend.Logger.Warn("WeCom confirmation UI update not dispatched", "event", "human.card_update_failed")
	}
}
