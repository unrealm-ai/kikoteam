package runtime

import (
	"testing"

	"github.com/unrealm-ai/kikoteam/agent-engine/internal/integration"
	"github.com/unrealm-ai/kikoteam/agent-engine/internal/repository"
)

func TestTaskSpaceHistoryMCPOnlyTaskSpaceExecution(t *testing.T) {
	worker := Worker{Root: t.TempDir(), Backend: &integration.Backend{URL: "http://localhost:8080"}}
	for _, tc := range []struct {
		name, mode, space, conversation, token string
		want                                   bool
	}{
		{"space task", "task", NewID(), "", "history-token", true},
		{"space task without token", "task", NewID(), "", "", false},
		{"conversation task", "task", "", NewID(), "history-token", false},
		{"chat", "chat", "", NewID(), "history-token", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("KIKO_RUNTIME_TEMP_ROOT", t.TempDir())
			w := repository.Work{WorkspaceID: NewID(), AgentID: NewID(), Mode: tc.mode, TaskSpaceID: tc.space, ConversationID: tc.conversation, Assignment: repository.Assignment{SessionID: NewID()}}
			if tc.mode == "task" {
				w.TaskID = NewID()
			}
			cfg := integration.HarnessConfig{WorkspaceID: w.WorkspaceID, AgentID: w.AgentID, TaskID: w.TaskID, SessionID: w.SessionID, Mode: tc.mode, TaskSpaceID: tc.space, ConversationID: tc.conversation, TaskSpaceHistoryToken: tc.token}
			input := NewID()
			out, signature, err := worker.prepareConfiguration(t.Context(), w, repository.Turn{Input: repository.Input{ID: input}}, cfg, integration.Endpoint{})
			if err != nil {
				t.Fatal(err)
			}
			if _, static := out.MCP["task_space_history"]; static || (out.HumanTools.TaskSpaceHistoryURL != "") != tc.want {
				t.Fatal("unexpected TaskSpace history registration")
			}
			if tc.want {
				if out.HumanTools.TaskSpaceHistoryURL != "http://localhost:8080/mcp/task-space-history" || out.HumanTools.Input != input || out.HumanTools.TaskSpaceHistoryToken != tc.token {
					t.Fatal("incorrect task history endpoint or credentials")
				}
				cfg.TaskSpaceHistoryToken = "next-token"
				next, nextSignature, err := worker.prepareConfiguration(t.Context(), w, repository.Turn{Input: repository.Input{ID: NewID()}}, cfg, integration.Endpoint{})
				if err != nil || nextSignature != signature || next.HumanTools.TaskSpaceHistoryToken != cfg.TaskSpaceHistoryToken {
					t.Fatal("rotating the task history token must preserve Pi")
				}
			}
		})
	}
}
