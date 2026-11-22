package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type runtimeRequest struct {
	Messages []struct {
		Role    string `json:"role"`
		Content any    `json:"content"`
	} `json:"messages"`
	Tools []struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	} `json:"tools"`
}

func runtimeSystemPrompt(body runtimeRequest) string {
	for _, message := range body.Messages {
		if message.Role == "system" || message.Role == "developer" {
			return fmt.Sprint(message.Content)
		}
	}
	return ""
}

func employeePromptModelReply(t *testing.T, w http.ResponseWriter, body runtimeRequest) bool {
	t.Helper()
	if len(body.Messages) == 0 {
		return false
	}
	input := fmt.Sprint(body.Messages[len(body.Messages)-1].Content)
	name, description := `名称 "Runtime"`, ""
	switch {
	case strings.Contains(input, "remember amber"):
	case strings.Contains(input, "你是谁？你叫什么？"):
		name = `名称 "Runtime renamed"`
		if !strings.Contains(fmt.Sprint(body.Messages), "remember amber") {
			t.Error("employee rename lost chat history")
		}
	case strings.Contains(input, "task-old-package"):
		description = `名称 "Original package"，说明 "Original scope"`
	case strings.Contains(input, "task-new-package"):
		name, description = `名称 "Runtime renamed"`, `名称 "Updated package"，说明 "Updated scope"`
	default:
		return false
	}
	prompt := runtimeSystemPrompt(body)
	if !strings.HasPrefix(prompt, "你是下方岗位配置中的 AI 员工") || !strings.Contains(prompt, name) || !strings.Contains(prompt, `职责 "Fixture reports"`) || !strings.Contains(prompt, `岗位行为规范 "Fixture employee rules"`) {
		t.Errorf("wrong system prompt for %q: %q", input, prompt)
	}
	if description == "" {
		if !strings.Contains(prompt, "直接用当前配置的员工名称自我介绍") || strings.Contains(prompt, "你是 AI 员工的主对话助手") || strings.Contains(prompt, "本任务能力包") {
			t.Errorf("chat system prompt has the wrong identity policy: %q", prompt)
		}
	} else if !strings.Contains(prompt, description) || !strings.Contains(prompt, "report-skill") {
		t.Errorf("wrong task package or Skill configuration for %q: %q", input, prompt)
	}
	humanTextStream(t, w, "prompt fixture checked")
	return true
}

func testRuntimeEmployeePrompts(t *testing.T, ctx context.Context, runtime *service.RuntimeService, agents *service.AgentService, employee service.Agent, workspace, actor, conversation, chat string, call func(string, string, any, string) map[string]any, wait func(func() bool)) {
	t.Helper()
	skill := publishRuntimeDirectory(t, ctx, runtime.Skills, workspace, actor, service.Skill{}, "prompt")
	pack, err := runtime.Packages.Create(ctx, workspace, actor, employee.ID, service.PackageCreate{Name: "Original package", Description: "Original scope", Skills: []service.PackageSkill{{SkillID: skill.ID, VersionID: skill.CurrentVersion.ID}}})
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/workspaces/" + workspace + "/sessions"
	oldTask := call("POST", base, map[string]any{"conversation_id": conversation, "mode": "task"}, "")["id"].(string)
	newName, newDescription := "Updated package", "Updated scope"
	if _, err = runtime.Packages.Update(ctx, workspace, actor, employee.ID, pack.ID, service.PackagePatch{Version: pack.Version, Name: &newName, Description: &newDescription}); err != nil {
		t.Fatal(err)
	}
	renamed := "Runtime renamed"
	if _, err = agents.Update(ctx, workspace, actor, employee.ID, service.AgentPatch{Version: employee.Version, Name: &renamed}); err != nil {
		t.Fatal(err)
	}
	await := func(id string, count int) {
		t.Helper()
		wait(func() bool {
			turns := call("GET", base+"/"+id+"/turns", nil, "")["items"].([]any)
			return len(turns) == count && turns[count-1].(map[string]any)["status"] == "completed"
		})
		events := call("GET", base+"/"+id+"/events?type=agent.message", nil, "")
		got := 0
		for _, event := range events["items"].([]any) {
			if strings.Contains(fmt.Sprint(event), "prompt fixture checked") {
				got++
			}
		}
		if got != count {
			t.Fatalf("provider did not observe the expected %d system prompts: %v", count, events)
		}
	}
	await(chat, 1)
	call("POST", base+"/"+chat+"/inputs", map[string]string{"text": "你是谁？你叫什么？"}, "employee-renamed")
	await(chat, 2)
	call("POST", base+"/"+oldTask+"/inputs", map[string]string{"text": "task-old-package"}, "task-old-package")
	await(oldTask, 1)
	newTask := call("POST", base, map[string]any{"conversation_id": conversation, "mode": "task", "input": map[string]string{"text": "task-new-package"}}, "task-new-package")["id"].(string)
	await(newTask, 1)
}

func runtimeTaskReply(t *testing.T, w http.ResponseWriter, body runtimeRequest) {
	t.Helper()
	text := ""
	results := 0
	secret := false
	shared := false
	for _, m := range body.Messages {
		v := fmt.Sprint(m.Content)
		if m.Role == "user" {
			text = v
			results = 0
			if strings.Contains(v, "secret-amber") {
				secret = true
			}
		}
		if m.Role == "tool" {
			results++
			if strings.Contains(v, "shared-value") {
				shared = true
			}
		}
	}
	if !strings.Contains(text, "task-") {
		t.Error("chat exposed builtin tools")
	}
	delta := map[string]any{"role": "assistant", "content": fmt.Sprintf("task done;shared=%v;secret=%v", shared, secret)}
	reason := "stop"
	checkBackground := strings.Contains(text, taskBFollowup)
	if results == 0 || (checkBackground && results < 3) {
		suffix := "__bash_start"
		arguments := map[string]any{"command": "sleep 120", "yield_time_ms": 1000}
		if strings.Contains(text, "task-A") {
			arguments["command"] = "printf shared-value > shared.txt; sleep 120"
			arguments["tty"] = true
		}
		if strings.Contains(text, "task-read") {
			suffix = "__read"
			arguments = map[string]any{"path": "shared.txt"}
		}
		if strings.Contains(text, "task-delegated") {
			arguments["command"] = "printf delegated > delegated.txt"
		}
		if checkBackground && results == 1 {
			suffix, arguments = "__bash_list", map[string]any{}
		}
		if checkBackground && results == 2 {
			suffix, arguments = "__work_item_list_handoff_employees", map[string]any{}
		}
		name := ""
		for _, tool := range body.Tools {
			if strings.HasSuffix(tool.Function.Name, suffix) {
				name = tool.Function.Name
			}
		}
		if name == "" {
			t.Errorf("tool %s unavailable", suffix)
			w.WriteHeader(500)
			return
		}
		data, err := json.Marshal(arguments)
		if err != nil {
			t.Error(err)
			return
		}
		delta = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprintf("call-runtime-%d", results), "type": "function", "function": map[string]any{"name": name, "arguments": string(data)}}}}
		reason = "tool_calls"
	}
	w.Header().Set("Content-Type", "text/event-stream")
	for _, v := range []map[string]any{delta, {}} {
		var finish any
		if len(v) == 0 {
			finish = reason
		}
		data, err := json.Marshal(map[string]any{"id": "task-runtime", "object": "chat.completion.chunk", "choices": []any{map[string]any{"index": 0, "delta": v, "finish_reason": finish}}})
		if err != nil {
			t.Error(err)
			return
		}
		if _, err = fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
			return
		}
	}
	if _, err := fmt.Fprint(w, "data: [DONE]\n\n"); err != nil {
		return
	}
}

func runtimeDelegationReply(t *testing.T, w http.ResponseWriter, body runtimeRequest) bool {
	t.Helper()
	text := ""
	results := 0
	target := ""
	for _, m := range body.Messages {
		if m.Role == "user" {
			text = fmt.Sprint(m.Content)
			results = 0
		}
		if m.Role == "tool" {
			results++
			raw := fmt.Sprint(m.Content)
			var out map[string]any
			if json.Unmarshal([]byte(raw), &out) == nil {
				if id, ok := out["session_id"].(string); ok {
					target = id
				}
			}
		}
	}
	if !strings.Contains(text, "delegate-") {
		return false
	}
	delta := map[string]any{"role": "assistant", "content": "delegation query completed"}
	reason := "stop"
	if results == 0 {
		suffix := "task_delegation_create_session"
		args := map[string]any{"instruction": "task-delegated execute a command"}
		switch {
		case strings.Contains(text, "delegate-list"):
			suffix = "task_delegation_list_sessions"
			args = map[string]any{}
		case strings.Contains(text, "delegate-query"):
			suffix = "task_delegation_get_session"
			args = map[string]any{"session_id": target}
		case strings.Contains(text, "delegate-send"):
			suffix = "task_delegation_send_message"
			args = map[string]any{"session_id": target, "text": "task-delegated followup"}
		case strings.Contains(text, "delegate-stop"):
			suffix = "task_delegation_stop_session"
			args = map[string]any{"session_id": target}
		}
		name := ""
		for _, v := range body.Tools {
			if strings.HasSuffix(v.Function.Name, "__"+suffix) {
				name = v.Function.Name
			}
		}
		if name == "" {
			t.Errorf("delegation tool %s missing", suffix)
			w.WriteHeader(500)
			return true
		}
		data, err := json.Marshal(args)
		if err != nil {
			t.Error(err)
			return true
		}
		delta = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": "delegate-call", "type": "function", "function": map[string]any{"name": name, "arguments": string(data)}}}}
		reason = "tool_calls"
	}
	w.Header().Set("Content-Type", "text/event-stream")
	for _, v := range []map[string]any{delta, {}} {
		var finish any
		if len(v) == 0 {
			finish = reason
		}
		data, err := json.Marshal(map[string]any{"id": "delegate", "object": "chat.completion.chunk", "choices": []any{map[string]any{"index": 0, "delta": v, "finish_reason": finish}}})
		if err != nil {
			t.Error(err)
			return true
		}
		if _, err = fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
			return true
		}
	}
	if _, err := fmt.Fprint(w, "data: [DONE]\n\n"); err != nil {
		return true
	}
	return true
}
