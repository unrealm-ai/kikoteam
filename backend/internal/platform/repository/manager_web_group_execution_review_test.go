package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	controller "github.com/unrealm-ai/kikoteam/backend/internal/manager/controller"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestWebGroupHarnessPromptsUseEachTaskActor(t *testing.T) {
	db, m, store, workspace, owner, employee, group := webMessageFixture(t)
	ctx := context.Background()
	store.SnapshotCapabilities = SnapshotCapabilities
	member := manager.NewID()
	insertWorkspaceAccount(t, db, member, "prompt-member@example.test")
	reviewExec(t, db, `INSERT INTO platform_members(workspace_id,user_id,role,status) VALUES($1,$2,'member','active')`, workspace, member)
	reviewExec(t, db, `INSERT INTO platform_web_group_members(group_id,workspace_id,user_id,role,status) VALUES($1,$2,$3,'member','active')`, group, workspace, member)
	runtime := runtimeCapabilityServices(t, db)
	skill := publishRuntimeDirectory(t, ctx, runtime.Skills, workspace, owner, service.Skill{}, "group")
	actors := []string{owner, member}
	packageNames := []string{"Owner-only package", "Member-only package"}
	for n, actor := range actors {
		scope := service.PackageConversationScope{
			Private: service.PackageConversationRule{Mode: "inherit_with_blacklist"},
			Group:   service.PackageConversationRule{Enabled: true, Mode: "allowlist", Allow: []service.UsageSelector{{Channel: "web", Kind: "user", ID: actor}}},
		}
		_, err := runtime.Packages.Create(ctx, workspace, owner, employee, service.PackageCreate{
			Name: packageNames[n], Description: packageNames[n] + " scope", ConversationScope: &scope,
			Skills: []service.PackageSkill{{SkillID: skill.ID, VersionID: skill.CurrentVersion.ID}},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	engine, secret := manager.NewID(), strings.Repeat("p", 40)
	if err := store.RegisterEngine(ctx, manager.Engine{ID: engine, HostID: "prompt-test", Endpoint: "http://127.0.0.1:1", Capacity: 10}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	(&controller.Server{Service: m, Engines: store, Runtime: runtime, EngineToken: secret, ResolveModelKey: func(_ context.Context, _, _ string, _ manager.Configuration) (string, error) {
		return "fixture-key", nil
	}}).Routes(mux)
	start := func(session, input string) (manager.DelegationIdentity, string, string) {
		t.Helper()
		if err := store.Dispatch(ctx); err != nil {
			t.Fatal(err)
		}
		id := manager.DelegationIdentity{SessionID: session, EngineID: engine}
		if err := db.QueryRowContext(ctx, `SELECT assignment_id,generation FROM manager_sessions WHERE id=$1`, session).Scan(&id.AssignmentID, &id.Generation); err != nil {
			t.Fatal(err)
		}
		turn := reviewStartTurn(t, db, workspace, session, input, id.AssignmentID, id.Generation)
		path := fmt.Sprintf("/api/internal/v1/sessions/%s/harness-config?engine_id=%s&assignment_id=%s&generation=%d&input_id=%s", session, engine, id.AssignmentID, id.Generation, input)
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("Authorization", "Bearer "+secret)
		out := httptest.NewRecorder()
		mux.ServeHTTP(out, r)
		var cfg manager.HarnessConfig
		if err := json.Unmarshal(out.Body.Bytes(), &cfg); err != nil || out.Code != http.StatusOK {
			t.Fatal(out.Code, out.Body.String(), err)
		}
		return id, turn, cfg.Configuration.Instruction
	}
	d := &manager.Delegation{Manager: m, Store: store}
	var sharedChat, sharedPrompt string
	for n, actor := range actors {
		msg, _, err := m.PostWebGroupMessage(ctx, workspace, actor, group, "package-prompt", manager.WebGroupPost{Text: "prepare my report", MentionAgentIDs: []string{employee}})
		if err != nil {
			t.Fatal(err)
		}
		if err = store.ProcessWebGroupInbox(ctx); err != nil {
			t.Fatal(err)
		}
		var chat, input string
		if err = db.QueryRowContext(ctx, `SELECT i.session_id,i.id FROM manager_web_group_inbox n JOIN manager_inputs i ON i.id=n.input_id WHERE n.message_id=$1`, msg.ID).Scan(&chat, &input); err != nil {
			t.Fatal(err)
		}
		id, turn, prompt := start(chat, input)
		if n == 0 {
			sharedChat, sharedPrompt = chat, prompt
		}
		if chat != sharedChat || prompt != sharedPrompt || !strings.Contains(prompt, `名称 "Worker"`) || strings.Contains(prompt, "本任务能力包") {
			t.Fatal("group chat inherited actor-specific configuration", prompt)
		}
		scope, err := store.DelegationSource(ctx, id)
		if err != nil || scope.ActorID != actor {
			t.Fatal("wrong delegation actor", err)
		}
		result, err := d.Call(ctx, scope, "create_session", manager.DelegationArgs{Instruction: "Read fixture data"})
		if err != nil {
			t.Fatal(err)
		}
		task := result.(map[string]any)["session_id"].(string)
		var taskInput string
		if err = db.QueryRowContext(ctx, `SELECT initial_input_id FROM manager_sessions WHERE id=$1`, task).Scan(&taskInput); err != nil {
			t.Fatal(err)
		}
		reviewFinishTurn(t, db, chat, input, turn)
		_, taskTurn, taskPrompt := start(task, taskInput)
		if !strings.Contains(taskPrompt, `名称 "`+packageNames[n]+`"`) || !strings.Contains(taskPrompt, `说明 "`+packageNames[n]+` scope"`) || strings.Contains(taskPrompt, packageNames[1-n]) {
			t.Fatal("wrong task package context", taskPrompt)
		}
		reviewFinishTurn(t, db, task, taskInput, taskTurn)
	}
}

// This is a real PostgreSQL/service integration test. Persisted synthetic turns
// stand in for the external model; no provider, tool, or sandbox is contacted.
func TestWebGroupDelegationReportReview(t *testing.T) {
	for _, revoke := range []string{"none", "member", "employee"} {
		t.Run(revoke, func(t *testing.T) { reviewWebGroupDelegation(t, revoke) })
	}
}
func reviewWebGroupDelegation(t *testing.T, revoke string) {
	t.Helper()
	db, m, s, w, actor, agent, group := webMessageFixture(t)
	ctx := context.Background()
	s.SnapshotCapabilities = SnapshotCapabilities
	reviewExec(t, db, `UPDATE platform_agents SET usage_config=jsonb_set(usage_config,'{access}','{"mode":"allowlist","group_members_enabled":true,"allow":[],"deny":[]}'::jsonb) WHERE id=$1`, agent)
	var direct bool
	if err := db.QueryRow(`SELECT platform_agent_web_allowed($1,$2,$3)`, w, agent, actor).Scan(&direct); err != nil || direct {
		t.Fatal("fixture must deny private access", direct, err)
	}
	msg, _, err := m.PostWebGroupMessage(ctx, w, actor, group, "review-trigger", manager.WebGroupPost{Text: "delegate safely", MentionAgentIDs: []string{agent}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ProcessWebGroupInbox(ctx); err != nil {
		t.Fatal(err)
	}
	var chat, input string
	if err = db.QueryRow(`SELECT i.session_id,i.id FROM manager_web_group_inbox n JOIN manager_inputs i ON i.id=n.input_id WHERE n.message_id=$1`, msg.ID).Scan(&chat, &input); err != nil {
		t.Fatal(err)
	}
	engine := manager.NewID()
	if err = s.RegisterEngine(ctx, manager.Engine{ID: engine, HostID: "review", Endpoint: "http://127.0.0.1:1", Capacity: 10}); err != nil {
		t.Fatal(err)
	}
	if err = s.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	identity := manager.DelegationIdentity{SessionID: chat, EngineID: engine}
	if err = db.QueryRow(`SELECT assignment_id,generation FROM manager_sessions WHERE id=$1`, chat).Scan(&identity.AssignmentID, &identity.Generation); err != nil {
		t.Fatal(err)
	}
	chatTurn := reviewStartTurn(t, db, w, chat, input, identity.AssignmentID, identity.Generation)
	secret := strings.Repeat("r", 40)
	token, err := manager.SignDelegation(secret, identity)
	if err != nil {
		t.Fatal(err)
	}
	delegate := manager.Delegation{Manager: m, Store: s, Secret: secret}
	scope, err := delegate.Authorize(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	value, err := delegate.Call(ctx, scope, "create_session", manager.DelegationArgs{Instruction: "read only local test"})
	if err != nil {
		t.Fatal("B-enabled group delegation without private permission", err)
	}
	task := value.(map[string]any)["session_id"].(string)
	var taskInput string
	if err = db.QueryRow(`SELECT initial_input_id FROM manager_sessions WHERE id=$1`, task).Scan(&taskInput); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Session(ctx, w, actor, task); err == nil {
		t.Fatal("public task leak")
	}
	reviewRejectSSE(t, m, w, actor, chat)
	reviewFinishTurn(t, db, chat, input, chatTurn)
	if err = s.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	var assignment string
	var generation int64
	if err = db.QueryRow(`SELECT assignment_id,generation FROM manager_sessions WHERE id=$1`, task).Scan(&assignment, &generation); err != nil {
		t.Fatal(err)
	}
	taskTurn := reviewStartTurn(t, db, w, task, taskInput, assignment, generation)
	if _, _, err = s.HarnessConfig(ctx, engine, task, assignment, generation, taskInput); err != nil {
		t.Fatal("task config blocked", err)
	}
	if revoke == "member" {
		reviewExec(t, db, `UPDATE platform_web_group_members SET status='removed',admission_epoch=admission_epoch+1 WHERE group_id=$1 AND user_id=$2`, group, actor)
	}
	if revoke == "employee" {
		reviewExec(t, db, `UPDATE platform_web_group_agents SET status='removed',admission_epoch=admission_epoch+1 WHERE group_id=$1 AND agent_id=$2`, group, agent)
	}
	if revoke != "none" {
		if _, _, err = s.HarnessConfig(ctx, engine, task, assignment, generation, taskInput); err == nil {
			t.Fatal("revocation did not fence runtime")
		}
	}
	reviewFinishTurn(t, db, task, taskInput, taskTurn)
	report, err := s.ReportTaskResult(ctx, manager.TaskResult{TurnID: taskTurn})
	if err != nil {
		t.Fatal(err)
	}
	if revoke != "none" {
		if report.Status != "suppressed" || report.TargetInputID != nil {
			t.Fatal("late report leaked", report)
		}
		if revoke == "member" {
			if _, err = m.WebGroupMessages(ctx, w, actor, group, 0, 100); err == nil {
				t.Fatal("exited member opened group")
			}
		}
		return
	}
	if report.Status != "admitted" || report.TargetInputID == nil {
		t.Fatal(report)
	}
	repeated, err := s.ReportTaskResult(ctx, manager.TaskResult{TurnID: taskTurn})
	if err != nil || repeated.ID != report.ID {
		t.Fatal("report not idempotent", err)
	}
	reportTurn := reviewStartTurn(t, db, w, chat, *report.TargetInputID, identity.AssignmentID, identity.Generation)
	reviewFinishTurn(t, db, chat, *report.TargetInputID, reportTurn)
	for range 2 {
		if err = s.MaterializeWebGroupReplies(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err = db.QueryRow(`SELECT count(*) FROM manager_web_group_messages WHERE origin_input_id=$1`, *report.TargetInputID).Scan(&n); err != nil || n != 1 {
		t.Fatal("task-report chat reply missing or duplicated", n, err)
	}
}
func reviewExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatal(err)
	}
}
func reviewStartTurn(t *testing.T, db *sql.DB, w, session, input, assignment string, generation int64) string {
	t.Helper()
	id := manager.NewID()
	reviewExec(t, db, `INSERT INTO manager_turns(id,workspace_id,session_id,input_id,turn_no,assignment_id,generation,status) SELECT $1,$2,$3,$4,last_turn_no+1,$5,$6,'running' FROM manager_sessions WHERE id=$3`, id, w, session, input, assignment, generation)
	reviewExec(t, db, `UPDATE manager_inputs SET status='started',turn_id=$2,context_state='committed' WHERE id=$1`, input, id)
	reviewExec(t, db, `UPDATE manager_sessions SET active_turn_id=$2,last_turn_no=last_turn_no+1 WHERE id=$1`, session, id)
	return id
}
func reviewFinishTurn(t *testing.T, db *sql.DB, session, input, turn string) {
	t.Helper()
	reviewExec(t, db, `UPDATE manager_turns SET status='completed',result_summary='mock task finished',finished_at=now() WHERE id=$1`, turn)
	reviewExec(t, db, `UPDATE manager_inputs SET status='processed',finished_at=now() WHERE id=$1`, input)
	reviewExec(t, db, `UPDATE manager_sessions SET active_turn_id=NULL,background_process_count=0 WHERE id=$1`, session)
}
func reviewRejectSSE(t *testing.T, m *manager.Service, w, actor, session string) {
	t.Helper()
	mux := http.NewServeMux()
	(&controller.Server{Service: m, Authenticate: func(context.Context, string) (string, error) { return actor, nil }}).Routes(mux)
	req := httptest.NewRequest("GET", "/api/v1/workspaces/"+w+"/sessions/"+session+"/events/stream", nil)
	req.AddCookie(&http.Cookie{Name: "kiko_session", Value: "test"})
	out := httptest.NewRecorder()
	mux.ServeHTTP(out, req)
	if out.Code != 404 {
		t.Fatalf("group internal SSE exposed: %d %s", out.Code, out.Body.String())
	}
}
