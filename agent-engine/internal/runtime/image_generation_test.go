package runtime

import (
	"testing"

	"github.com/unrealm-ai/kikoteam/agent-engine/internal/integration"
	"github.com/unrealm-ai/kikoteam/agent-engine/internal/repository"
)

func TestImageToolFollowsTaskTurnGrant(t *testing.T) {
	t.Setenv("KIKO_RUNTIME_TEMP_ROOT", t.TempDir())
	w := Worker{Root: t.TempDir(), Backend: &integration.Backend{URL: "http://backend.test"}}
	for _, tc := range []struct {
		mode, token      string
		correction, want bool
	}{{"task", "pinned", false, true}, {"task", "", false, false}, {"task", "pinned", true, false}, {"chat", "pinned", false, false}} {
		current := repository.Work{Assignment: repository.Assignment{SessionID: NewID()}, Mode: tc.mode, WorkspaceID: "workspace", AgentID: "agent", ConversationID: "conv"}
		if tc.mode == "task" {
			current.TaskID = "task"
		}
		cfg := integration.HarnessConfig{SessionID: current.SessionID, WorkspaceID: current.WorkspaceID, AgentID: current.AgentID, Mode: tc.mode, TaskID: current.TaskID, ConversationID: current.ConversationID, ImageGenerationToken: tc.token}
		v, signature, err := w.prepareConfiguration(t.Context(), current, repository.Turn{Input: repository.Input{ID: "input", CorrectionOnly: tc.correction}}, cfg, integration.Endpoint{})
		if err != nil {
			t.Fatal(err)
		}
		entry, ok := v.RelayServers["image_generation"]
		if ok != tc.want {
			t.Fatal(tc)
		}
		if ok && (entry.URL != "http://backend.test/mcp/platform-plugins/image-generation" || entry.Timeout != 340 || entry.ToolExposure["generate_image"] != "direct" || v.RelayHeaders["image_generation"]["Authorization"] != "Bearer pinned") {
			t.Fatal("incorrect MCP mount")
		}
		if ok {
			cfg.ImageGenerationToken = "rotated"
			next, nextSignature, err := w.prepareConfiguration(t.Context(), current, repository.Turn{Input: repository.Input{ID: "second"}}, cfg, integration.Endpoint{})
			if err != nil || nextSignature != signature || next.RelayHeaders["image_generation"]["Authorization"] != "Bearer rotated" {
				t.Fatal("rotating an input grant must preserve the task runner", err)
			}
		}
	}
}
