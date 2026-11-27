package runtime

import (
	"context"
	"errors"
	"io"

	"github.com/unrealm-ai/kikoteam/agent-engine/internal/pi"
	"github.com/unrealm-ai/kikoteam/agent-engine/internal/repository"
)

type promptFailureReason struct {
	stage string
	err   error
}

func (e promptFailureReason) Error() string { return e.stage }
func (e promptFailureReason) Unwrap() error { return e.err }

func promptFailure(stage string, err error) error {
	if err == nil {
		return nil
	}
	return promptFailureReason{stage: stage, err: err}
}

func promptErrorKind(err error) string {
	switch {
	case errors.Is(err, errWorkMCPUnavailable):
		return "work_mcp_unavailable"
	case errors.Is(err, errAssistantMCPUnavailable):
		return "assistant_mcp_unavailable"
	case errors.Is(err, pi.ErrPromptAborted):
		return "pi_aborted"
	case errors.Is(err, pi.ErrPromptModel):
		return "model_error"
	case errors.Is(err, pi.ErrPromptTruncated):
		return "model_output_truncated"
	case errors.Is(err, pi.ErrToolBatchSettled):
		return "unexpected_tool_batch_end"
	case errors.Is(err, repository.ErrStale):
		return "stale_assignment"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "pi_stream_closed"
	default:
		return "unclassified"
	}
}

func (w *Worker) logPromptFailure(ctx context.Context, work repository.Work, turn repository.Turn, cause, err error) {
	if w.Log == nil || (cause == nil && err == nil) {
		return
	}
	stage := "pi_prompt"
	var reason promptFailureReason
	if errors.As(cause, &reason) {
		stage = reason.stage
	} else if cause != nil {
		stage = "worker_context"
	}
	if cause == nil {
		cause = err
	}
	// Do not log raw Pi/provider text, input, tool arguments, or credentials.
	w.Log.WarnContext(ctx, "Execution prompt failed", "event", "turn.prompt_failed",
		"session_id", work.SessionID, "turn_id", turn.ID, "input_id", turn.Input.ID,
		"stage", stage, "kind", promptErrorKind(cause))
}
