package runtime

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/unrealm-ai/kikoteam/agent-engine/internal/pi"
	"github.com/unrealm-ai/kikoteam/agent-engine/internal/repository"
)

func TestPromptFailureDiagnosticsPreferFirstCauseAndRedactRawError(t *testing.T) {
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(promptFailure("steering", errors.Join(context.DeadlineExceeded, errors.New("PRIVATE-DETAIL"))))
	cancel(promptFailure("boundary_poll", errors.New("later failure")))
	var buf bytes.Buffer
	w := Worker{Log: slog.New(slog.NewJSONHandler(&buf, nil))}
	w.logPromptFailure(ctx, repository.Work{}, repository.Turn{}, context.Cause(ctx), pi.ErrPromptAborted)
	if got := buf.String(); !strings.Contains(got, `"stage":"steering"`) ||
		!strings.Contains(got, `"kind":"deadline_exceeded"`) || strings.Contains(got, "PRIVATE-DETAIL") ||
		strings.Contains(got, "later failure") {
		t.Fatalf("unexpected diagnostic: %s", got)
	}
	buf.Reset()
	w.logPromptFailure(t.Context(), repository.Work{}, repository.Turn{}, nil, pi.ErrPromptAborted)
	if got := buf.String(); !strings.Contains(got, `"stage":"pi_prompt"`) || !strings.Contains(got, `"kind":"pi_aborted"`) {
		t.Fatalf("unexpected Pi diagnostic: %s", got)
	}
}
