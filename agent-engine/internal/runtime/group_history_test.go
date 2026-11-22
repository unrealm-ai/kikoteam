package runtime

import (
	"context"
	"github.com/unrealm-ai/kikoteam/agent-engine/internal/integration"
	"github.com/unrealm-ai/kikoteam/agent-engine/internal/repository"
	"testing"
)

func TestGroupHistoryMCPOnlyConversationTask(t *testing.T) {
	w := repository.Work{WorkspaceID: NewID(), AgentID: NewID(), ConversationID: NewID(), TaskID: NewID(), Mode: "task", Assignment: repository.Assignment{SessionID: NewID()}}
	cfg := integration.HarnessConfig{WorkspaceID: w.WorkspaceID, AgentID: w.AgentID, ConversationID: w.ConversationID, TaskID: w.TaskID, SessionID: w.SessionID, Mode: "task", GroupHistoryToken: "purpose-token"}
	worker := Worker{Root: t.TempDir(), Backend: &integration.Backend{URL: "http://localhost:8080"}}
	out, _, e := worker.prepareConfiguration(context.Background(), w, repository.Turn{}, cfg, integration.Endpoint{})
	if e != nil {
		t.Fatal(e)
	}
	if _, ok := out.MCP["group_history"]; !ok {
		t.Fatal("task missing authorized history MCP")
	}
	w.Mode = "chat"
	cfg.Mode = "chat"
	out, _, e = worker.prepareConfiguration(context.Background(), w, repository.Turn{}, cfg, integration.Endpoint{})
	if e != nil {
		t.Fatal(e)
	}
	if _, ok := out.MCP["group_history"]; ok {
		t.Fatal("chat received task history credential")
	}
	w.Mode = "task"
	cfg.Mode = "task"
	w.TaskSpaceID = NewID()
	cfg.TaskSpaceID = w.TaskSpaceID
	w.ConversationID = ""
	cfg.ConversationID = ""
	out, _, e = worker.prepareConfiguration(context.Background(), w, repository.Turn{}, cfg, integration.Endpoint{})
	if e != nil {
		t.Fatal(e)
	}
	if _, ok := out.MCP["group_history"]; ok {
		t.Fatal("TaskSpace got group authority")
	}
}
