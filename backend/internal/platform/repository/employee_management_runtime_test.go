package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ps "github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

const installFixturePrompt = "GitHub Skill 已验证，帮我把它安装到当前 AI 员工"

func TestEmployeeManagementEngineWebSkillPermission(t *testing.T) {
	if os.Getenv("KIKO_RUNTIME_E2E") != "1" {
		t.Skip("enable real Engine/Pi runtime")
	}
	f := modelProxyFixture(t)
	ownerClient, memberClient := proxyBrowserClient(t), proxyBrowserClient(t)
	owner, w := f.register(t, ownerClient, "management-owner@example.test")
	member, _ := f.register(t, memberClient, "management-user@example.test")
	reviewExec(t, f.db, `INSERT INTO platform_members(workspace_id,user_id,role) VALUES($1,$2,'member')`, w, member)
	model := f.createModel(t, ownerClient, "chat_completions", true)
	employee := f.api(t, ownerClient, "POST", "/workspaces/"+w+"/agents", map[string]string{"name": "物流员工", "platform_model_id": model}, "", 201)["id"].(string)
	reviewExec(t, f.db, "UPDATE platform_credit_wallets SET balance_units=10000000000000 WHERE account_id=$1", owner)
	f.fixtureModel = func(out http.ResponseWriter, body map[string]any) bool {
		return employeeManagementFixtureReply(t, out, body)
	}
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	stop := f.startEngine(t, ctx, root)
	defer stop()
	base := "/workspaces/" + w
	c := f.api(t, memberClient, "POST", base+"/conversations/web-direct", map[string]string{"agent_id": employee}, "", 200)
	// Use an actual Web message as the root; the model fixture drives the normal
	// delegation tool, then interprets the execution Agent's MCP denial.
	chat := f.api(t, memberClient, "POST", base+"/sessions", map[string]any{"mode": "chat", "conversation_id": c["id"], "input": map[string]string{"text": installFixturePrompt}}, ps.NewID(), 201)
	for ctx.Err() == nil {
		var result string
		err = f.db.QueryRowContext(ctx, `SELECT COALESCE(turn.result_summary,'') FROM manager_turns turn JOIN manager_sessions sess ON sess.id=turn.session_id
 JOIN manager_tasks task ON task.id=sess.task_id WHERE task.source_chat_session_id=$1 AND turn.status='completed' ORDER BY turn.finished_at DESC LIMIT 1`, chat["id"]).Scan(&result)
		if err == nil {
			if !strings.Contains(result, "没有权限") {
				t.Fatal("wrong execution result:", result)
			}
			var count int
			if err = f.db.QueryRow(`SELECT count(*) FROM manager_session_events e JOIN manager_sessions s ON s.id=e.session_id JOIN manager_tasks k ON k.id=s.task_id
 WHERE k.source_chat_session_id=$1 AND e.event->>'type'='agent.tool_result' AND (e.event->>'title' LIKE '%list_employee_resources%' OR e.event->>'title' LIKE '%get_employee%')`, chat["id"]).Scan(&count); err != nil || count != 2 {
				t.Fatal("no real management tool result", count, err)
			}
			return
		}
		if !errors.Is(err, sql.ErrNoRows) {
			t.Fatal(err)
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatal("engine did not finish management task:", err)
}

func employeeManagementFixtureReply(t *testing.T, out http.ResponseWriter, body map[string]any) bool {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Error(err)
		return false
	}
	var r runtimeRequest
	if json.Unmarshal(b, &r) != nil || !strings.Contains(string(b), installFixturePrompt) {
		return false
	}
	var delegation, resources, get string
	for _, tool := range r.Tools {
		name := tool.Function.Name
		if strings.HasSuffix(name, "task_delegation_create_session") {
			delegation = name
		}
		if strings.HasSuffix(name, "get_employee") {
			get = name
		}
		if strings.HasSuffix(name, "list_employee_resources") {
			resources = name
		}
	}
	last := fmt.Sprint(r.Messages[len(r.Messages)-1].Content)
	switch {
	case resources != "" && strings.Contains(last, "employee_management_forbidden"):
		humanTextStream(t, out, "你没有权限安装这个 Skill，请联系员工管理员。")
	case get != "" && r.Messages[len(r.Messages)-1].Role == "tool":
		humanToolStream(t, out, []string{get}, []any{map[string]string{"config_key": "capability_packages"}})
	case resources != "":
		humanToolStream(t, out, []string{resources}, []any{map[string]string{"resource_type": "skill", "name": "GitHub logistics"}})
	case delegation != "" && r.Messages[len(r.Messages)-1].Role == "user":
		if strings.Contains(last, "任务") && strings.Contains(last, "没有权限") {
			humanTextStream(t, out, "没有权限安装这个 Skill，请联系员工管理员。")
			return true
		}
		humanToolStream(t, out, []string{delegation}, []any{map[string]any{"title": "安装 Skill 权限测试", "instruction": installFixturePrompt}})
	default:
		humanTextStream(t, out, "我会先核对安装权限。")
	}
	return true
}
