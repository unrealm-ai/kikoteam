package pi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

var nativeAssistantFixtureTools = map[string][]string{
	"platform": {"get_employee", "list_employee_resources", "update_employee"},
	"demo": {"get_order", "get_logistics", "get_support_history", "get_meeting_notes", "get_release_plan", "list_project_chat",
		"get_incident_logs", "get_incident_metrics", "get_release_changes", "get_oncall_runbook", "rollback_release"},
}

func TestUpstreamAssistantMCPRefreshUsesSamePiAndCurrentGrants(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "node_modules/.bin/pi")
	if _, err = os.Stat(binary); err != nil {
		t.Skip("run npm ci in agent-engine to install pinned upstream Pi")
	}
	var mu sync.Mutex
	var calls []string
	var modelInputs []string
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Role    string
				Content any
			}
			Tools []struct{ Function struct{ Name string } }
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		var phase string
		for _, m := range req.Messages {
			if m.Role == "user" {
				phase = assistantFixtureInputText(m.Content)
			}
		}
		if req.Messages[len(req.Messages)-1].Role != "user" {
			writeAssistantFixtureResponse(t, w, map[string]any{"role": "assistant", "content": "done"}, "stop")
			return
		}
		mu.Lock()
		modelInputs = append(modelInputs, phase)
		mu.Unlock()
		var haveManagement, haveDemo, haveCreate bool
		var toolCalls []any
		for _, tool := range req.Tools {
			name := tool.Function.Name
			if strings.HasPrefix(name, "mcp__employee_management__") {
				haveManagement = true
			}
			if strings.HasPrefix(name, "mcp__assistant_demo__") {
				haveDemo = true
			}
			haveCreate = haveCreate || name == "mcp__employee_management__create_employee"
			if name == "mcp__employee_management__get_employee" || name == "mcp__assistant_demo__get_order" {
				args := fixtureJSON(t, map[string]string{"fixture_input": phase})
				toolCalls = append(toolCalls, map[string]any{"index": len(toolCalls), "id": fmt.Sprintf("%s-%d", phase, len(toolCalls)),
					"type": "function", "function": map[string]any{"name": name, "arguments": string(args)}})
			}
		}
		expectManagement := phase == "one" || phase == "two" || phase == "restored"
		expectDemo := phase == "one" || phase == "two" || phase == "demo-only"
		if haveManagement != expectManagement || haveDemo != expectDemo || haveCreate != (phase == "restored") {
			t.Errorf("%s: model saw management=%v demo=%v create=%v", phase, haveManagement, haveDemo, haveCreate)
		}
		if len(toolCalls) == 0 {
			writeAssistantFixtureResponse(t, w, map[string]any{"role": "assistant", "content": "done"}, "stop")
			return
		}
		writeAssistantFixtureResponse(t, w, map[string]any{"role": "assistant", "tool_calls": toolCalls}, "tool_calls")
	}))
	defer model.Close()

	mcp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		family := filepath.Base(r.URL.Path)
		input, auth := r.Header.Get("X-Kiko-Assistant-Input"), r.Header.Get("Authorization")
		if !slices.Contains([]string{"platform", "demo"}, family) || auth != "Bearer "+family+"-"+input || input == "denied" {
			w.WriteHeader(403)
			return
		}
		// This fixture is a stateless streamable HTTP server. Pi's
		// optional GET event stream and DELETE teardown need no body.
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var msg struct {
			Method string
			ID     json.RawMessage
			Params struct {
				ProtocolVersion string
				Arguments       map[string]string
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		var data any
		switch msg.Method {
		case "initialize":
			data = map[string]any{"protocolVersion": msg.Params.ProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}},
				"serverInfo": map[string]string{"name": family, "version": "1.0.0"}}
		case "tools/list":
			names := nativeAssistantFixtureTools[family]
			if family == "platform" && input == "restored" {
				names = append(slices.Clone(names), "create_employee", "list_employees")
			}
			items := make([]any, 0, len(names))
			for _, name := range names {
				items = append(items, map[string]any{"name": name, "inputSchema": map[string]any{"type": "object",
					"properties": map[string]any{"fixture_input": map[string]string{"type": "string"}}}})
			}
			data = map[string]any{"tools": items}
		case "tools/call":
			if msg.Params.Arguments["fixture_input"] != input {
				t.Error("a tool call used credentials from a different prompt")
			}
			mu.Lock()
			calls = append(calls, family+":"+input)
			mu.Unlock()
			data = map[string]any{"content": []any{map[string]string{"type": "text", "text": "fixture"}}}
		default:
			w.WriteHeader(202)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": data}); err != nil {
			t.Error(err)
		}
	}))
	defer mcp.Close()

	dir := t.TempDir()
	cfg := Config{Directory: filepath.Join(dir, "native"), RuntimeDirectory: filepath.Join(dir, "work"), PiCommand: binary,
		Launcher: filepath.Join(root, "scripts/pi.sh"), HumanToolsExtension: filepath.Join(root, "extensions/human-tools.mjs"),
		Model:      Model{Protocol: "chat_completions", BaseURL: model.URL + "/v1", ID: "fixture-model", Key: "fixture"},
		HumanTools: HumanToolsConfig{Mode: "task"}}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	if err = cfg.PrepareHumanTools(); err != nil {
		t.Fatal(err)
	}
	client, err := Start(ctx, cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close() //nolint:errcheck // Explicit test teardown.
	for _, step := range []struct {
		input            string
		management, demo bool
	}{{input: "initial"}, {"one", true, true}, {"two", true, true}, {"demo-only", false, true}, {input: "no-grants"}, {"restored", true, false}, {"denied", false, true}, {input: "after-denied"}} {
		cfg.HumanTools = HumanToolsConfig{Input: step.input, Mode: "task", AssistantMCPBaseURL: mcp.URL}
		if step.management {
			cfg.HumanTools.EmployeeManagementToken = "platform-" + step.input
		}
		if step.demo {
			cfg.HumanTools.AssistantDemoToken = "demo-" + step.input
		}
		if err := cfg.PrepareHumanTools(); err != nil {
			t.Fatal(err)
		}
		var setupFailed bool
		reason, err := client.Prompt(ctx, step.input, nil, func(u Update) {
			if u["type"] == "extension_error" && u["error"] == "[kiko_assistant_mcp_unavailable]" {
				setupFailed = true
			}
		})
		if step.input == "denied" {
			if !setupFailed {
				t.Fatalf("failed discovery produced no trusted setup error: %s %v", reason, err)
			}
			continue
		}
		if reason != "end_turn" || err != nil || setupFailed {
			t.Fatalf("%s: prompt %s setupFailure=%v err=%v", step.input, reason, setupFailed, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	slices.Sort(calls)
	if want := []string{"demo:demo-only", "demo:one", "demo:two", "platform:one", "platform:restored", "platform:two"}; !slices.Equal(calls, want) {
		t.Fatalf("wrong native calls after rotation: %v", calls)
	}
	if want := []string{"initial", "one", "two", "demo-only", "no-grants", "restored", "after-denied"}; !slices.Equal(modelInputs, want) {
		t.Fatalf("wrong model requests after rotation: %v", modelInputs)
	}
}

func assistantFixtureInputText(content any) string {
	if text, ok := content.(string); ok {
		return text
	}
	if blocks, ok := content.([]any); ok && len(blocks) > 0 {
		if block, ok := blocks[0].(map[string]any); ok {
			text, _ := block["text"].(string)
			return text
		}
	}
	return ""
}

func writeAssistantFixtureResponse(t *testing.T, w http.ResponseWriter, delta map[string]any, reason string) {
	t.Helper()
	w.Header().Set("Content-Type", "text/event-stream")
	for _, chunk := range []struct {
		delta  map[string]any
		finish any
	}{{delta, nil}, {map[string]any{}, reason}} {
		value := fixtureJSON(t, map[string]any{"id": "assistant-test", "object": "chat.completion.chunk",
			"choices": []any{map[string]any{"index": 0, "delta": chunk.delta, "finish_reason": chunk.finish}}})
		if _, err := fmt.Fprintf(w, "data: %s\n\n", value); err != nil {
			t.Error(err)
			return
		}
	}
	if _, err := fmt.Fprint(w, "data: [DONE]\n\n"); err != nil {
		t.Error(err)
	}
}
