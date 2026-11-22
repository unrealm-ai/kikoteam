package runtime

import (
	"bytes"
	"encoding/json"
	"github.com/unrealm-ai/kikoteam/agent-engine/internal/repository"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestInputClaimDiagnosticCorrelationAndRedaction(t *testing.T) {
	for _, mode := range []string{"turn", "steer"} {
		t.Run(mode, func(t *testing.T) {
			var b bytes.Buffer
			w := Worker{Log: slog.New(slog.NewJSONHandler(&b, nil))}
			input := repository.Input{ID: "input", Seq: 7, Text: "SECRET_MESSAGE", SubmittedBy: "SECRET_ACTOR", CreatedAt: time.Now().Add(-time.Second)}
			w.logInputClaim(repository.Work{Assignment: repository.Assignment{SessionID: "session"}}, repository.Turn{ID: "turn"}, input, mode)
			var out map[string]any
			if e := json.Unmarshal(b.Bytes(), &out); e != nil {
				t.Fatal(e)
			}
			if out["event"] != "input.claimed" || out["input_id"] != "input" || out["turn_id"] != "turn" || out["session_id"] != "session" || out["consumption_mode"] != mode || out["queue_wait_ms"].(float64) < 1000 {
				t.Fatal(out)
			}
			if strings.Contains(b.String(), "SECRET") {
				t.Fatal("sensitive data logged")
			}
		})
	}
	var b bytes.Buffer
	w := Worker{Log: slog.New(slog.NewJSONHandler(&b, nil))}
	w.logInputClaim(repository.Work{}, repository.Turn{}, repository.Input{}, "turn")
	if strings.Contains(b.String(), "queue_wait_ms") {
		t.Fatal("invented queue wait")
	}
}
