package runtime

import (
	"context"
	"github.com/unrealm-ai/kikoteam/agent-engine/internal/pi"
	"github.com/unrealm-ai/kikoteam/agent-engine/internal/repository"
)

// One outstanding receipt preserves the existing receipt extension's purpose
// and exclusive intent protocol, without adding or changing Pi extensions.
type steering struct {
	inputs []repository.Input
	last   string
}

func (w *Worker) steer(ctx context.Context, work repository.Work, turn repository.Turn, client *pi.Client, state *steering) error {
	if pi.NativeInputState(w.nativeDirectory(work), client.SessionID(), state.last) != "committed" {
		return nil
	}
	if err := w.Store.ContextReceipt(ctx, work, state.last, "committed"); err != nil {
		return err
	}
	native, err := client.GetState(ctx)
	if err != nil {
		return err
	}
	if !native.IsStreaming {
		return nil
	}
	input, err := w.Store.ClaimSteering(ctx, work, turn)
	if err != nil || input == nil {
		return err
	}
	w.logInputClaim(work, turn, *input, "steer")
	state.inputs = append(state.inputs, *input)
	state.last = input.ID
	text, err := pi.PrepareInputReceipt(w.nativeDirectory(work), client.SessionID(), input.ID, input.Text)
	if err != nil {
		return err
	}
	return client.Steer(ctx, text)
}
func (w *Worker) finishSteering(ctx context.Context, work repository.Work, turn repository.Turn, client *pi.Client, state steering, stopped bool) error {
	removed, err := client.TakeQueue(ctx)
	if err != nil {
		return err
	}
	for _, input := range state.inputs {
		receipt := pi.NativeInputState(w.nativeDirectory(work), client.SessionID(), input.ID)
		if receipt != "committed" && input.ID == state.last {
			for _, text := range removed {
				if text == input.Text {
					receipt = "absent"
					if !stopped {
						if err = pi.ForgetUnconsumedIntent(w.nativeDirectory(work), input.ID); err != nil {
							return err
						}
						if err = w.Store.ReturnSteering(ctx, work, turn, input.ID); err != nil {
							return err
						}
						w.Log.Info("Steering input returned to queue", "event", "input.requeued", "input_id", input.ID, "session_id", work.SessionID, "turn_id", turn.ID)
						receipt = "pending"
					}
					break
				}
			}
		}
		if receipt != "pending" {
			if err = w.Store.ContextReceipt(ctx, work, input.ID, receipt); err != nil {
				return err
			}
		}
		w.Log.Info("Steering input reconciled", "event", "input.steer_reconciled", "input_id", input.ID, "session_id", work.SessionID, "turn_id", turn.ID, "context_state", receipt)
	}
	return nil
}
