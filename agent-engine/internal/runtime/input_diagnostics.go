package runtime

import (
	"github.com/unrealm-ai/kikoteam/agent-engine/internal/repository"
	"time"
)

// Claim means durable platform consumption, not delivery to or adoption by Pi.
// Never include message text, principals, credentials or provider response bodies.
func (w *Worker) logInputClaim(work repository.Work, turn repository.Turn, input repository.Input, mode string) {
	if w.Log == nil {
		return
	}
	attrs := []any{"event", "input.claimed", "input_id", input.ID, "session_id", work.SessionID, "turn_id", turn.ID, "input_seq", input.Seq, "consumption_mode", mode, "principal_type", input.PrincipalType}
	if !input.CreatedAt.IsZero() {
		wait := time.Since(input.CreatedAt).Milliseconds()
		if wait < 0 {
			wait = 0
		}
		attrs = append(attrs, "queue_wait_ms", wait)
	}
	w.Log.Info("Input durably claimed", attrs...)
}
