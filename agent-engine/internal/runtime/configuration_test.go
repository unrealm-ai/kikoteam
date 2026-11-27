package runtime

import (
	"errors"
	"strings"
	"testing"

	"github.com/unrealm-ai/kikoteam/agent-engine/internal/integration"
	"github.com/unrealm-ai/kikoteam/agent-engine/internal/repository"
)

func TestTaskMCPInputCredentialsDoNotReplacePi(t *testing.T) {
	t.Setenv("KIKO_RUNTIME_TEMP_ROOT", t.TempDir())
	w := Worker{Root: t.TempDir(), Backend: &integration.Backend{URL: "http://manager.invalid"}}
	current := repository.Work{Assignment: repository.Assignment{SessionID: NewID()}, WorkspaceID: "workspace", AgentID: "employee", Mode: "task", TaskID: "task", ConversationID: "conversation"}
	cfg := integration.HarnessConfig{
		SessionID: current.SessionID, WorkspaceID: current.WorkspaceID,
		AgentID: current.AgentID, Mode: current.Mode,
		TaskID: current.TaskID, ConversationID: current.ConversationID,
		WorkRole: "ordinary", WorkMCPToken: "test-first", WorkLocalToken: "test-first-local",
		HumanMCPToken: "human-first", HumanToolsToken: "ask-first",
	}
	cfg.Resources.Plugins = []integration.Plugin{{PluginID: "test", AccountID: "account", Endpoint: "/mcp/plugin-connections/account",
		Headers: map[string]string{"Authorization": "Bearer relay-first"}, Tools: []integration.PluginTool{{Name: "lookup"}}}}
	turn := repository.Turn{Input: repository.Input{ID: "first"}}
	first, firstSignature, err := w.prepareConfiguration(t.Context(), current, turn, cfg, integration.Endpoint{})
	if err != nil {
		t.Fatal(err)
	}
	turn.Input.ID = "second"
	cfg.WorkMCPToken, cfg.WorkLocalToken = "test-second", "test-second-local"
	cfg.HumanMCPToken, cfg.HumanToolsToken = "human-second", "ask-second"
	cfg.Resources.Plugins[0].Headers = map[string]string{"Authorization": "Bearer relay-second"}
	second, secondSignature, err := w.prepareConfiguration(t.Context(), current, turn, cfg, integration.Endpoint{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := second.MCP[workMCPServer]; ok || firstSignature != secondSignature {
		t.Fatal("rotating task credentials changed static Pi configuration")
	}
	if _, ok := second.MCP[humanMCPServer]; ok || second.HumanTools.RecipientsMCPToken != "human-second" ||
		second.HumanTools.RecipientsMCPURL != "http://manager.invalid/mcp/human-confirmations/task" || !second.HumanTools.RecipientsMCP {
		t.Fatal("task recipient lookup must use this input's native MCP registration")
	}
	if _, ok := second.MCP["plugin_test"]; ok || second.RelayHeaders["plugin_test"]["Authorization"] != "Bearer relay-second" ||
		second.RelayServers["plugin_test"].ToolExposure["lookup"] != "direct" {
		t.Fatal("signed plugin relay must use the current grant and retain its tool allowlist")
	}
	if first.HumanTools.Input == second.HumanTools.Input || second.HumanTools.WorkMCPToken != "test-second" ||
		second.HumanTools.WorkMCPURL != "http://manager.invalid/mcp/work-items/ordinary" {
		t.Fatal("latest Work MCP authority was not refreshed in turn configuration")
	}
	cfg.WorkRole = "review"
	_, reviewSignature, err := w.prepareConfiguration(t.Context(), current, turn, cfg, integration.Endpoint{})
	if err != nil {
		t.Fatal(err)
	}
	if secondSignature == reviewSignature {
		t.Fatal("a change of Work tool definitions must still change the signature")
	}
}

func TestCorrectionConfigurationOnlyCarriesHumanReviewTools(t *testing.T) {
	t.Setenv("KIKO_RUNTIME_TEMP_ROOT", t.TempDir())
	w := Worker{Root: t.TempDir(), Backend: &integration.Backend{URL: "http://manager.invalid"}}
	current := repository.Work{Assignment: repository.Assignment{SessionID: NewID()}, WorkspaceID: "workspace", AgentID: "employee", Mode: "task", TaskID: "task", ConversationID: "conversation"}
	for _, role := range []string{"ordinary", "assignee", "review"} {
		cfg := integration.HarnessConfig{SessionID: current.SessionID, WorkspaceID: current.WorkspaceID, AgentID: current.AgentID, Mode: current.Mode, TaskID: current.TaskID, ConversationID: current.ConversationID,
			HumanMCPToken: "human-mcp", HumanToolsToken: "human-local", WorkRole: role, WorkMCPToken: "work-mcp", WorkLocalToken: "work-local", GroupHistoryToken: "history", CapabilityToken: "capability"}
		turn := repository.Turn{Input: repository.Input{ID: "correction", CorrectionOnly: true}}
		out, signature, e := w.prepareConfiguration(t.Context(), current, turn, cfg, integration.Endpoint{URL: "http://runner.invalid", Token: "builtin"})
		if e != nil || signature == "" || !out.PlanOnly || len(out.MCP) != 0 || len(out.Skills) != 0 || out.PolicyExtension != "" || out.HumanToolsExtension == "" || out.WorkMCPRole != "" {
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
	t.Setenv("KIKO_RUNTIME_TEMP_ROOT", t.TempDir())
	w := Worker{Root: t.TempDir(), Backend: &integration.Backend{URL: "http://manager.invalid"}}
	chat := repository.Work{Assignment: repository.Assignment{SessionID: NewID()}, WorkspaceID: "w", AgentID: "a", ConversationID: "conv", Mode: "chat"}
	cfg := integration.HarnessConfig{WorkspaceID: "w", AgentID: "a", SessionID: chat.SessionID, ConversationID: "conv", Mode: "chat", ReportOnly: true, DelegationToken: "chat-scope", HumanToolsToken: "human", HumanMCPToken: "human"}
	turn := repository.Turn{Input: repository.Input{ID: "report"}}
	out, _, err := w.prepareConfiguration(t.Context(), chat, turn, cfg, integration.Endpoint{URL: "http://runner.invalid", Token: "runner"})
	if err != nil {
		t.Fatal(err)
	}
	server, ok := out.MCP["task_delegation"].(map[string]any)
	if !ok || len(out.MCP) != 1 || server["url"] != "http://manager.invalid/mcp/task-delegation?mode=report" || out.HumanToolsExtension != "" {
		t.Fatal("report exposed other tools", out.MCP)
	}
	cfg.ReportOnly = false
	ordinary, _, err := w.prepareConfiguration(t.Context(), chat, turn, cfg, integration.Endpoint{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Instruction != ordinary.Instruction || strings.Contains(out.Instruction, "<system-reminder>") {
		t.Fatal("task report changed the stable system instruction")
	}
}

func TestAttachmentToolUsesStableAssignmentCredentialForConversationTasks(t *testing.T) {
	t.Setenv("KIKO_RUNTIME_TEMP_ROOT", t.TempDir())
	w := Worker{Root: t.TempDir(), Backend: &integration.Backend{URL: "http://manager.invalid"}}
	work := repository.Work{Assignment: repository.Assignment{SessionID: NewID()}, WorkspaceID: "w", AgentID: "a", ConversationID: "conv", TaskID: "task", Mode: "task"}
	cfg := integration.HarnessConfig{WorkspaceID: "w", AgentID: "a", ConversationID: "conv", TaskID: "task", SessionID: work.SessionID, Mode: "task", AttachmentToken: "assignment-token"}
	first, sig, err := w.prepareConfiguration(t.Context(), work, repository.Turn{Input: repository.Input{ID: "first"}}, cfg, integration.Endpoint{})
	if err != nil {
		t.Fatal(err)
	}
	server, ok := first.MCP[attachmentMCPServer].(map[string]any)
	if !ok || server["url"] != "http://manager.invalid/mcp/input-attachments" || server["exposure"] != "direct" {
		t.Fatalf("unexpected attachment MCP: %#v", server)
	}
	headers, ok := server["headers"].(map[string]string)
	if !ok || len(headers) != 1 || headers["Authorization"] != "Bearer assignment-token" {
		t.Fatalf("attachment capability must not capture an Input in its static config: %#v", headers)
	}
	_, next, err := w.prepareConfiguration(t.Context(), work, repository.Turn{Input: repository.Input{ID: "next"}}, cfg, integration.Endpoint{})
	if err != nil || next != sig {
		t.Fatal("next input unnecessarily changes Pi config", err)
	}

	for _, scenario := range []string{"chat", "report", "task_space", "correction", "no_token"} {
		t.Run(scenario, func(t *testing.T) {
			t.Setenv("KIKO_RUNTIME_TEMP_ROOT", t.TempDir())
			wc, cc := work, cfg
			input := repository.Input{ID: "test-input"}
			switch scenario {
			case "chat":
				wc.Mode, cc.Mode = "chat", "chat"
				wc.TaskID, cc.TaskID = "", ""
			case "report":
				cc.ReportOnly = true
			case "task_space":
				wc.ConversationID, cc.ConversationID = "", ""
				wc.TaskSpaceID, cc.TaskSpaceID = "space", "space"
			case "correction":
				input.CorrectionOnly = true
			case "no_token":
				cc.AttachmentToken = ""
			}
			out, _, e := w.prepareConfiguration(t.Context(), wc, repository.Turn{Input: input}, cc, integration.Endpoint{})
			if _, visible := out.MCP[attachmentMCPServer]; e != nil || visible {
				t.Fatal("attachment tool exposed outside regular conversation task", e)
			}
		})
	}
}

func TestChatPassesAttachmentReferencesToTaskWithoutExposingReader(t *testing.T) {
	t.Setenv("KIKO_RUNTIME_TEMP_ROOT", t.TempDir())
	w := Worker{Root: t.TempDir(), Backend: &integration.Backend{URL: "http://manager.invalid"}}
	chat := repository.Work{Assignment: repository.Assignment{SessionID: NewID()}, WorkspaceID: "w", AgentID: "a", ConversationID: "conv", Mode: "chat"}
	cfg := integration.HarnessConfig{WorkspaceID: "w", AgentID: "a", SessionID: chat.SessionID, ConversationID: "conv", Mode: "chat", DelegationToken: "delegation"}
	out, _, err := w.prepareConfiguration(t.Context(), chat, repository.Turn{}, cfg, integration.Endpoint{})
	if err != nil || len(out.MCP) != 1 || out.MCP["task_delegation"] == nil || out.MCP[attachmentMCPServer] != nil ||
		!strings.Contains(out.Instruction, "attachment_ids") || !strings.Contains(out.Instruction, "attachment_paths") {
		t.Fatal("chat should delegate references without being granted an attachment reader", err)
	}
}

func TestStableChatCatalogKeepsCredentialsSeparate(t *testing.T) {
	t.Setenv("KIKO_RUNTIME_TEMP_ROOT", t.TempDir())
	w := Worker{Root: t.TempDir(), Backend: &integration.Backend{URL: "http://manager.invalid"}}
	chat := repository.Work{Assignment: repository.Assignment{SessionID: NewID()}, WorkspaceID: "w", AgentID: "a", ConversationID: "conv", Mode: "chat"}
	cfg := integration.HarnessConfig{WorkspaceID: "w", AgentID: "a", SessionID: chat.SessionID, ConversationID: "conv", Mode: "chat",
		ChatDelegationCatalogToken: "delegate-catalog", DelegationToken: "delegate-live",
		ChatHumanCatalogToken: "human-catalog", HumanMCPToken: "human-live", HumanToolsToken: "human-local"}
	turn := repository.Turn{Input: repository.Input{ID: "this-input"}}
	for _, tc := range []struct {
		name, delegation, human string
		report, private         bool
	}{
		{name: "private", delegation: "delegate-live", human: "human-live", private: true},
		{name: "report", delegation: "delegate-live", human: "human-catalog", report: true, private: true},
		{name: "confirmation-only", delegation: "delegate-catalog", human: "human-live", private: true},
		{name: "group", delegation: "delegate-live"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := cfg
			c.ReportOnly = tc.report
			if tc.name == "confirmation-only" {
				c.DelegationToken = ""
			}
			if !tc.private {
				c.ChatHumanCatalogToken, c.HumanMCPToken, c.HumanToolsToken = "", "", ""
			}
			out, _, err := w.prepareConfiguration(t.Context(), chat, turn, c, integration.Endpoint{})
			if err != nil {
				t.Fatal(err)
			}
			delegation := out.MCP["task_delegation"].(map[string]any)
			expect := "http://manager.invalid/mcp/task-delegation?catalog=stable"
			if tc.report {
				expect += "&mode=report"
			}
			if delegation["url"] != expect {
				t.Fatal("stable catalog did not retain report routing restriction")
			}
			assertCatalogHeaders(t, delegation, tc.delegation, turn.Input.ID)
			human, exists := out.MCP[humanMCPServer].(map[string]any)
			if exists != tc.private {
				t.Fatal("human tool family depends on conversation, not current turn")
			}
			if exists {
				assertCatalogHeaders(t, human, tc.human, turn.Input.ID)
			}
		})
	}
}

func assertCatalogHeaders(t *testing.T, m map[string]any, token, input string) {
	t.Helper()
	h := m["headers"].(map[string]string)
	if strings.HasSuffix(token, "-catalog") {
		if h["X-Kiko-Chat-Catalog"] != token || h["X-Kiko-Catalog-Input"] != input || h["Authorization"] != "" {
			t.Fatal("schema-only credential was used as callable authorization")
		}
	} else if h["Authorization"] != "Bearer "+token || h["X-Kiko-Chat-Catalog"] != "" {
		t.Fatal("existing authorization was not kept separate from catalog")
	}
}

func TestAssistantTurnCredentialsDoNotReplacePi(t *testing.T) {
	t.Setenv("KIKO_RUNTIME_TEMP_ROOT", t.TempDir())
	worker := Worker{Root: t.TempDir(), Backend: &integration.Backend{URL: "http://manager.invalid"}}
	current := repository.Work{Assignment: repository.Assignment{SessionID: NewID()}, WorkspaceID: "workspace", AgentID: "employee", Mode: "task", ConversationID: "conversation"}
	cfg := integration.HarnessConfig{SessionID: current.SessionID, WorkspaceID: current.WorkspaceID, AgentID: current.AgentID,
		Mode: current.Mode, ConversationID: current.ConversationID, AssistantToolSet: "demo"}
	var stable string
	for _, step := range []struct {
		input, management, demo string
		report                  bool
	}{
		{input: "initial-no-grant"},
		{input: "first", management: "management-1", demo: "demo-1"},
		{input: "second", management: "management-2", demo: "demo-2"},
		{input: "demo-only", demo: "demo-3"},
		{input: "revoked"},
		{input: "pure-report", management: "stale-management", demo: "stale-demo", report: true},
	} {
		cfg.EmployeeManagementToken, cfg.AssistantMCPToken, cfg.ReportOnly = step.management, step.demo, step.report
		out, signature, err := worker.prepareConfiguration(t.Context(), current, repository.Turn{Input: repository.Input{ID: step.input}}, cfg, integration.Endpoint{})
		if err != nil || signature == "" || out.HumanToolsExtension == "" {
			t.Fatalf("%s: configuration unusable: %v", step.input, err)
		}
		if stable != "" && stable != signature {
			t.Fatalf("%s: per-turn credential changed the native Pi signature", step.input)
		}
		stable = signature
		if _, exists := out.MCP["employee_management"]; exists {
			t.Fatal("static Pi config contains employee management")
		}
		if _, exists := out.MCP["assistant_demo"]; exists {
			t.Fatal("static Pi config contains assistant demo")
		}
		management, demo := step.management, step.demo
		if step.report {
			management, demo = "", ""
		}
		if out.HumanTools.Mode != "task" || out.HumanTools.Input != step.input ||
			out.HumanTools.EmployeeManagementToken != management || out.HumanTools.AssistantDemoToken != demo {
			t.Fatalf("%s: wrong per-turn Assistant authorization", step.input)
		}
		if (management != "" || demo != "") != (out.HumanTools.AssistantMCPBaseURL == "http://manager.invalid/mcp/assistant") {
			t.Fatalf("%s: wrong per-turn Assistant endpoint", step.input)
		}
	}
}

func TestAssistantTurnCredentialsNotExposedToChatOrCorrection(t *testing.T) {
	t.Setenv("KIKO_RUNTIME_TEMP_ROOT", t.TempDir())
	worker := Worker{Root: t.TempDir(), Backend: &integration.Backend{URL: "http://manager.invalid"}}
	for _, mode := range []string{"task", "chat"} {
		current := repository.Work{Assignment: repository.Assignment{SessionID: NewID()}, WorkspaceID: "workspace", AgentID: "employee", Mode: mode, ConversationID: "conversation"}
		cfg := integration.HarnessConfig{SessionID: current.SessionID, WorkspaceID: current.WorkspaceID, AgentID: current.AgentID,
			Mode: mode, ConversationID: current.ConversationID, EmployeeManagementToken: "fixture", AssistantToolSet: "demo", AssistantMCPToken: "fixture-demo",
			HumanToolsToken: "fixture-human"}
		turn := repository.Turn{Input: repository.Input{ID: "bound-input", CorrectionOnly: mode == "task"}}
		out, _, err := worker.prepareConfiguration(t.Context(), current, turn, cfg, integration.Endpoint{})
		if err != nil || out.HumanTools.EmployeeManagementToken != "" || out.HumanTools.AssistantDemoToken != "" || out.HumanTools.AssistantMCPBaseURL != "" {
			t.Fatalf("%s: Assistant authorization escaped ordinary task scope: %v", mode, err)
		}
	}
}

func TestAssistantTurnSetupErrorIsNotSuccessfulModelTurn(t *testing.T) {
	e := promptSetupFailure(map[string]any{"type": "extension_error", "event": "before_agent_start", "error": "[kiko_assistant_mcp_unavailable]"})
	if !errors.Is(e, promptSetupError("assistant_mcp_unavailable")) || promptErrorKind(e) != "assistant_mcp_unavailable" {
		t.Fatalf("wrong Assistant startup outcome: %v", e)
	}
	if e := promptSetupFailure(map[string]any{"type": "message_update", "event": "before_agent_start", "error": "[kiko_assistant_mcp_unavailable]"}); e != nil {
		t.Fatal("ordinary message content was treated as a trusted failure marker")
	}
}
