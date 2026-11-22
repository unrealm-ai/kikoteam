package runtime

import (
	"context"
	"testing"

	"github.com/unrealm-ai/kikoteam/agent-engine/internal/integration"
	"github.com/unrealm-ai/kikoteam/agent-engine/internal/repository"
)

func TestWorkMCPInputCredentialsDoNotReplacePi(t *testing.T) {
	w := Worker{Root: t.TempDir(), Backend: &integration.Backend{URL: "http://manager.invalid"}}
	current := repository.Work{Assignment: repository.Assignment{SessionID: "session"}, WorkspaceID: "workspace", AgentID: "employee", Mode: "task", TaskID: "task", ConversationID: "conversation"}
	cfg := integration.HarnessConfig{
		SessionID: current.SessionID, WorkspaceID: current.WorkspaceID,
		AgentID: current.AgentID, Mode: current.Mode,
		TaskID: current.TaskID, ConversationID: current.ConversationID,
		WorkRole: "ordinary", WorkMCPToken: "test-first", WorkLocalToken: "test-first-local",
	}
	turn := repository.Turn{Input: repository.Input{ID: "first"}}
	first, firstSignature, err := w.prepareConfiguration(context.Background(), current, turn, cfg, integration.Endpoint{})
	if err != nil {
		t.Fatal(err)
	}
	turn.Input.ID = "second"
	cfg.WorkMCPToken, cfg.WorkLocalToken = "test-second", "test-second-local"
	second, secondSignature, err := w.prepareConfiguration(context.Background(), current, turn, cfg, integration.Endpoint{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := second.MCP[workMCPServer]; ok || firstSignature != secondSignature {
		t.Fatal("rotating Work MCP credentials changed static Pi configuration")
	}
	if first.HumanTools.Input == second.HumanTools.Input || second.HumanTools.WorkMCPToken != "test-second" ||
		second.HumanTools.WorkMCPURL != "http://manager.invalid/mcp/work-items/ordinary" {
		t.Fatal("latest Work MCP authority was not refreshed in turn configuration")
	}
	cfg.WorkRole = "review"
	_, reviewSignature, err := w.prepareConfiguration(context.Background(), current, turn, cfg, integration.Endpoint{})
	if err != nil {
		t.Fatal(err)
	}
	if secondSignature == reviewSignature {
		t.Fatal("a change of Work tool definitions must still change the signature")
	}
}

func TestCorrectionConfigurationOnlyCarriesHumanReviewTools(t *testing.T) {
	w := Worker{Root: t.TempDir(), Backend: &integration.Backend{URL: "http://manager.invalid"}}
	current := repository.Work{Assignment: repository.Assignment{SessionID: "session"}, WorkspaceID: "workspace", AgentID: "employee", Mode: "task", TaskID: "task", ConversationID: "conversation"}
	for _, role := range []string{"ordinary", "assignee", "review"} {
		cfg := integration.HarnessConfig{SessionID: current.SessionID, WorkspaceID: current.WorkspaceID, AgentID: current.AgentID, Mode: current.Mode, TaskID: current.TaskID, ConversationID: current.ConversationID,
			HumanMCPToken: "human-mcp", HumanToolsToken: "human-local", WorkRole: role, WorkMCPToken: "work-mcp", WorkLocalToken: "work-local", GroupHistoryToken: "history", CapabilityToken: "capability"}
		turn := repository.Turn{Input: repository.Input{ID: "correction", CorrectionOnly: true}}
		out, signature, e := w.prepareConfiguration(context.Background(), current, turn, cfg, integration.Endpoint{URL: "http://runner.invalid", Token: "builtin"})
		if e != nil || signature == "" || !out.PlanOnly || len(out.MCP) != 0 || len(out.Skills) != 0 || out.PolicyExtension != "" || out.HumanToolsExtension == "" || out.WorkMCPRole != "" || out.ReceiptExtension == "" {
			t.Fatalf("role %s exposed unneeded capabilities: err=%v", role, e)
		}
		if out.HumanTools.Token != cfg.HumanToolsToken || out.HumanTools.Input != turn.Input.ID ||
			out.HumanTools.Mode != "task" || !out.HumanTools.ReapprovalOnly || out.HumanTools.RecipientsMCP ||
			out.HumanTools.WorkToken != "" || out.HumanTools.WorkMCPToken != "" {
			t.Fatalf("role %s did not use only input-scoped review tools", role)
		}
	}
}

func TestReportChatOnlyUsesSelectionEndpoint(t *testing.T) {
	w := Worker{Root: t.TempDir(), Backend: &integration.Backend{URL: "http://manager.invalid"}}
	chat := repository.Work{Assignment: repository.Assignment{SessionID: "chat"}, WorkspaceID: "w", AgentID: "a", ConversationID: "conv", Mode: "chat"}
	cfg := integration.HarnessConfig{WorkspaceID: "w", AgentID: "a", SessionID: "chat", ConversationID: "conv", Mode: "chat", ReportOnly: true, DelegationToken: "chat-scope", HumanToolsToken: "human", HumanMCPToken: "human"}
	turn := repository.Turn{Input: repository.Input{ID: "report"}}
	out, _, err := w.prepareConfiguration(t.Context(), chat, turn, cfg, integration.Endpoint{URL: "http://runner.invalid", Token: "runner"})
	if err != nil {
		t.Fatal(err)
	}
	server, ok := out.MCP["task_delegation"].(map[string]any)
	if !ok || len(out.MCP) != 1 || server["url"] != "http://manager.invalid/mcp/task-delegation?mode=report" || out.HumanToolsExtension != "" {
		t.Fatal("report exposed other tools", out.MCP)
	}
}
