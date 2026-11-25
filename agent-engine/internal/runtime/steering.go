package runtime

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/unrealm-ai/kikoteam/agent-engine/internal/pi"
	"github.com/unrealm-ai/kikoteam/agent-engine/internal/repository"
)

// The prompt reader and the steering loop share one outstanding input per Turn.
// Install it before sending RPC because Pi may emit the user event before its reply.
type steering struct {
	pending atomic.Pointer[repository.Input]
}

func (s *steering) matchingUser(update pi.Update) *repository.Input {
	if update["type"] != "message_end" {
		return nil
	}
	message, _ := update["message"].(map[string]any)
	if message["role"] != "user" {
		return nil
	}
	text, ok := message["content"].(string)
	if !ok {
		blocks, _ := message["content"].([]any)
		if len(blocks) != 1 {
			return nil
		}
		block, _ := blocks[0].(map[string]any)
		if block["type"] != "text" {
			return nil
		}
		text, ok = block["text"].(string)
	}
	input := s.pending.Load()
	if !ok || input == nil || text != input.Text {
		return nil
	}
	return input
}

func (w *Worker) steer(ctx context.Context, work repository.Work, turn repository.Turn, client *pi.Client, state *steering) error {
	if state.pending.Load() != nil {
		return nil
	}
	native, err := client.GetState(ctx)
	if err != nil || !native.IsStreaming {
		return err
	}
	input, err := w.Store.ClaimSteering(ctx, work, turn)
	if err != nil || input == nil {
		return err
	}
	w.logInputClaim(work, turn, *input, "steer")
	state.pending.Store(input)
	disposition, err := client.Steer(ctx, input.Text)
	if errors.Is(err, pi.ErrCommandRejected) {
		return errors.Join(err, w.Store.ContextReceipt(ctx, work, input.ID, "absent"))
	}
	if disposition == "handled" {
		// Pi handled this command without queueing a user message. Later tools
		// keep their previous source. Complete settles the unobserved context.
		state.pending.CompareAndSwap(input, nil)
	}
	return err
}

func (w *Worker) finishSteering(ctx context.Context, work repository.Work, turn repository.Turn, client *pi.Client, state *steering, stopped bool) error {
	removed, err := client.TakeQueue(ctx)
	if err != nil {
		return err
	}
	input := state.pending.Load()
	if input == nil || input.ID == turn.Input.ID || len(removed) != 1 || removed[0] != input.Text {
		return nil
	}
	if stopped {
		return w.Store.ContextReceipt(ctx, work, input.ID, "absent")
	}
	if err := w.Store.ReturnSteering(ctx, work, turn, input.ID); err != nil {
		return err
	}
	state.pending.CompareAndSwap(input, nil)
	w.Log.Info("Steering input returned to queue", "event", "input.requeued", "input_id", input.ID, "session_id", work.SessionID, "turn_id", turn.ID)
	return nil
}
