package runtime

import (
	"testing"

	"github.com/unrealm-ai/kikoteam/agent-engine/internal/integration"
	"github.com/unrealm-ai/kikoteam/agent-engine/internal/repository"
)

func TestProgressRotatesCredentialsWithoutRestartingPi(t *testing.T) {
	t.Setenv("KIKO_RUNTIME_TEMP_ROOT", t.TempDir())
	worker := Worker{Root: t.TempDir(), Backend: &integration.Backend{URL: "http://localhost:8080"}}
	w := repository.Work{WorkspaceID: NewID(), AgentID: NewID(), Mode: "task", TaskID: NewID(), ConversationID: NewID(), Assignment: repository.Assignment{SessionID: NewID()}}
	cfg := integration.HarnessConfig{WorkspaceID: w.WorkspaceID, AgentID: w.AgentID, TaskID: w.TaskID, SessionID: w.SessionID, Mode: w.Mode, ConversationID: w.ConversationID, TaskProgressToken: "first"}
	var signature string
	for _, token := range []string{"first", "second"} {
		cfg.TaskProgressToken = token
		input := NewID()
		out, next, err := worker.prepareConfiguration(t.Context(), w, repository.Turn{Input: repository.Input{ID: input}}, cfg, integration.Endpoint{})
		if err != nil {
			t.Fatal(err)
		}
		if signature != "" && signature != next {
			t.Fatal("per-input progress credential restarted Pi")
		}
		if out.RelayServers["task_progress"].ToolExposure["update_task_progress"] != "direct" || out.RelayHeaders["task_progress"]["Authorization"] != "Bearer "+token || out.RelayHeaders["task_progress"]["X-Kiko-Progress-Input"] != input {
			t.Fatal("missing active scoped progress tool")
		}
		signature = next
	}
}
