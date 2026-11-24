package taskdelegation

import (
	"encoding/json"
	"strings"
	"testing"

	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func TestModelTaskResultUsesOneTaskIdentityAcrossShapes(t *testing.T) {
	task, session, turn, input := manager.NewID(), manager.NewID(), manager.NewID(), manager.NewID()
	s := manager.Session{ID: session, TaskID: &task, Mode: "task"}
	appendResult := manager.Input{ID: input, SessionID: session, Text: "user content remains unchanged"}
	for _, tc := range []struct {
		name, selected, mustContain string
		output                      any
	}{
		{"list", "", `"task_id":"` + task + `"`, manager.Page[manager.Session]{Items: []manager.Session{s}}},
		{"stop", task, `"task_id":"` + task + `"`, s},
		{"append", task, `"id":"` + input + `"`, appendResult},
		{"planned", task, `"task_id":"` + task + `"`, manager.PlannedTask{TaskID: task, SessionID: session}},
		{"exact turn and events", task, `"id":"` + turn + `"`, map[string]any{
			"turn":                   manager.Turn{ID: turn, SessionID: session},
			"input":                  &appendResult,
			"source_chat_session_id": session,
			"messages":               []json.RawMessage{json.RawMessage(`{"session_id":"` + session + `","content":"public text"}`)},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := modelTaskResult(tc.output, tc.selected)
			if err != nil {
				t.Fatal(err)
			}
			b, err := json.Marshal(out)
			if err != nil || !strings.Contains(string(b), tc.mustContain) || strings.Contains(string(b), session) ||
				strings.Contains(string(b), `"session_id"`) || strings.Contains(string(b), `"source_chat_session_id"`) {
				t.Fatalf("unexpected model task projection: %s (%v)", b, err)
			}
		})
	}
}
