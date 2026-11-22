package runtime

import (
	"context"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/unrealm-ai/kikoteam/agent-engine/internal/integration"
	"github.com/unrealm-ai/kikoteam/agent-engine/internal/pi"
	"github.com/unrealm-ai/kikoteam/agent-engine/internal/repository"
)

type nativeConfirmation struct {
	nativeID string
	action   repository.RequiredAction
	opened   time.Time
}

func openConfirmation(ctx context.Context, store *repository.Store, work repository.Work, turn string, update pi.Update) (nativeConfirmation, error) {
	now := time.Now()
	nativeID, _ := update["id"].(string)
	title, titleOK := update["title"].(string)
	message, messageOK := update["message"].(string)
	if nativeID == "" || !titleOK || !messageOK || utf8.RuneCountInString(title)+utf8.RuneCountInString(message) > 65000 {
		return nativeConfirmation{}, errors.New("invalid Pi confirmation")
	}
	action := repository.RequiredAction{Type: "confirmation", RequestID: NewID(), TurnID: turn, Title: title, Message: message}
	if timeout, ok := update["timeout"].(float64); ok && timeout > 0 {
		expires := now.Add(time.Duration(timeout) * time.Millisecond).UTC()
		action.ExpiresAt = &expires
	}
	return nativeConfirmation{nativeID, action, now}, store.RequestConfirmation(ctx, work, action)
}

// Only the prompt owner accesses pending. Native IDs are valid on this pipe only.
func (w *Worker) checkConfirmations(ctx context.Context, work repository.Work, turn repository.Turn, client *pi.Client, runner *integration.Runner, pending map[string]nativeConfirmation) (bool, error) {
	retire := false
	for id, request := range pending {
		answer, err := w.Store.ClaimConfirmation(ctx, work, turn, id, request.action.ExpiresAt)
		if err != nil {
			return false, err
		}
		if answer != nil {
			// Pi independently times out a dialog. Never send a late answer to it.
			if request.action.ExpiresAt != nil && !time.Now().Before(*request.action.ExpiresAt) {
				return false, errors.New("confirmation expired before native delivery")
			}
			if err = client.Confirm(ctx, request.nativeID, *answer); err != nil {
				return false, err
			}
			delete(pending, id)
			continue
		}
		if request.action.ExpiresAt != nil && !time.Now().Before(*request.action.ExpiresAt) {
			delete(pending, id)
			continue
		}
		retire = retire || time.Since(request.opened) >= w.Idle
	}
	if !retire {
		return false, nil
	}
	if runner != nil {
		state, err := runner.State(ctx, work.SessionID)
		if err != nil || !state.Quiet() {
			return false, nil
		}
	}
	return w.Store.CanRetireConfirmation(ctx, work, turn.ID)
}
