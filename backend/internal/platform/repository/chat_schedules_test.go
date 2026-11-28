package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/controller"
	repo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	platform "github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestChatScheduleIdleOccurrenceAndOrdinaryReport(t *testing.T) {
	db, m, d, scope := plannedFixture(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Minute).Add(time.Minute)
	const content = "在任务结果里提醒用户：该喝水了。由平台回告原对话，不发送外部消息。"
	args := manager.DelegationArgs{Instruction: content, Schedule: &manager.ChatScheduleRule{Frequency: "once", At: &at}}
	raw, err := d.Call(ctx, scope, "create_scheduled_task", args)
	if err != nil {
		t.Fatal(err)
	}
	plan := raw.(manager.ChatSchedule)
	raw, err = d.Call(ctx, scope, "create_scheduled_task", args)
	if err != nil || raw.(manager.ChatSchedule).ID != plan.ID {
		t.Fatal(raw, err)
	}
	var sourceTurn string
	if err = db.QueryRow(`SELECT active_turn_id FROM manager_sessions WHERE id=$1`, scope.Identity.SessionID).Scan(&sourceTurn); err != nil {
		t.Fatal(err)
	}

	// Prove no scheduled work runs synchronously and the source chat can be idle.
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM manager_tasks WHERE source_chat_session_id=$1`, scope.Identity.SessionID).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	reviewFinishTurn(t, db, scope.Identity.SessionID, scope.InputID, sourceTurn)
	due := time.Now().UTC().Truncate(time.Minute).Add(-time.Minute)
	if _, err = db.Exec(`UPDATE manager_chat_schedules SET next_due=$2,rule=jsonb_set(rule,'{at}',to_jsonb($2::timestamptz)) WHERE id=$1`, plan.ID, due); err != nil {
		t.Fatal(err)
	}
	hubToken := strings.Repeat("h", 40)
	mux := http.NewServeMux()
	(&controller.ChannelServer{Manager: m, Store: d.Store.(*repo.Store), Token: hubToken}).Routes(mux)
	post := func(path string, body any) *httptest.ResponseRecorder {
		b, e := json.Marshal(body)
		if e != nil {
			t.Error(e)
		}
		r := httptest.NewRequest("POST", path, bytes.NewReader(b))
		r.Header.Set("Authorization", "Bearer "+hubToken)
		r.Header.Set("Content-Type", "application/json")
		out := httptest.NewRecorder()
		mux.ServeHTTP(out, r)
		return out
	}
	for _, include := range []bool{false, true} {
		path := "/api/internal/v1/trigger-schedules/poll"
		if include {
			path += "?include_chat=true"
		}
		poll := post(path, map[string]int{"limit": 100})
		if poll.Code != 200 || strings.Contains(poll.Body.String(), plan.ID) != include {
			t.Fatal(poll.Code, poll.Body.String())
		}
	}
	tick := manager.TriggerScheduleTick{Kind: "chat", Version: 1, ExpectedDue: due, RecoveryStartedAt: due.Add(-time.Minute)}
	var wg sync.WaitGroup
	results := make(chan manager.TriggerScheduleResult, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp := post("/api/internal/v1/trigger-schedules/"+plan.ID+"/tick", tick)
			var r manager.TriggerScheduleResult
			if e := json.Unmarshal(resp.Body.Bytes(), &r); e != nil || resp.Code != 200 {
				t.Errorf("tick HTTP %d: %s (%v)", resp.Code, resp.Body.String(), e)
			}
			results <- r
		}()
	}
	wg.Wait()
	close(results)
	var receipt *manager.TriggerReceipt
	for r := range results {
		if r.Status == "accepted" && receipt == nil {
			receipt = r.Receipt
		} else if r.Status != "stale" {
			t.Fatal(r)
		}
	}
	if receipt == nil {
		t.Fatal("no task was admitted")
	}
	var conversation, source, input, body, status string
	if err = db.QueryRow(`SELECT t.conversation_id,t.source_chat_session_id,t.source_chat_input_id,i.text,p.status FROM manager_chat_schedule_runs run JOIN manager_chat_schedules p ON p.id=run.schedule_id JOIN manager_tasks t ON t.id=run.task_id JOIN manager_sessions s ON s.task_id=t.id JOIN manager_inputs i ON i.id=s.initial_input_id WHERE run.schedule_id=$1`, plan.ID).Scan(&conversation, &source, &input, &body, &status); err != nil || conversation != scope.ConversationID || source != scope.Identity.SessionID || input != scope.InputID || body != content || status != "completed" {
		t.Fatal(conversation, source, input, body, status, err)
	}
	turn := reviewStartTurn(t, db, scope.WorkspaceID, receipt.SessionID, receipt.InputID, manager.NewID(), 1)
	reviewFinishTurn(t, db, receipt.SessionID, receipt.InputID, turn)
	reviewExec(t, db, `UPDATE manager_turns SET result_summary='该喝水了。' WHERE id=$1`, turn)
	managed, err := m.ManagedChatSchedules(ctx, scope.WorkspaceID, scope.ActorID, scope.AgentID)
	if err != nil || len(managed) != 1 || len(managed[0].Runs) != 1 || managed[0].Runs[0].Status != "accepted" ||
		managed[0].Runs[0].TaskID == nil || *managed[0].Runs[0].TaskID != receipt.TaskID || managed[0].Runs[0].ExecutionStatus != "completed" {
		t.Fatal(managed, err)
	}
	report, err := d.Store.(*repo.Store).ReportTaskResult(ctx, manager.TaskResult{TurnID: turn})
	if err != nil || report.TargetInputID == nil {
		t.Fatal(report, err)
	}
	replayed, err := d.Store.(*repo.Store).ReportTaskResult(ctx, manager.TaskResult{TurnID: turn})
	if err != nil || replayed.TargetInputID == nil || *replayed.TargetInputID != *report.TargetInputID {
		t.Fatal(replayed, err)
	}
	if err = db.QueryRow(`SELECT session_id,text FROM manager_inputs WHERE id=$1`, *report.TargetInputID).Scan(&source, &body); err != nil || source != scope.Identity.SessionID || !strings.Contains(body, "该喝水了") {
		t.Fatal(source, body, err)
	}
}

func TestChatScheduleCancelAndRevocation(t *testing.T) {
	db, m, d, scope := plannedFixture(t)
	ctx := context.Background()
	raw, err := d.Call(ctx, scope, "create_scheduled_task", manager.DelegationArgs{Instruction: "run someday", Schedule: &manager.ChatScheduleRule{Frequency: "daily", Times: []string{"09:00"}}})
	if err != nil {
		t.Fatal(err)
	}
	plan := raw.(manager.ChatSchedule)
	listed, err := d.Call(ctx, scope, "list_scheduled_tasks", manager.DelegationArgs{ScheduleID: plan.ID})
	if err != nil || len(listed.([]manager.ChatSchedule)) != 1 {
		t.Fatal(listed, err)
	}
	raw, err = d.Call(ctx, scope, "update_scheduled_task", manager.DelegationArgs{ScheduleID: plan.ID, Schedule: &manager.ChatScheduleRule{Frequency: "weekly", Times: []string{"16:00"}, Weekdays: []int{1, 3}}})
	if err != nil || raw.(manager.ChatSchedule).Rule.Frequency != "weekly" {
		t.Fatal(raw, err)
	}
	raw, err = d.Call(ctx, scope, "update_scheduled_task", manager.DelegationArgs{ScheduleID: plan.ID, Cancel: true})
	if err != nil || raw.(manager.ChatSchedule).Status != "canceled" {
		t.Fatal(raw, err)
	}
	raw, err = d.Call(ctx, scope, "create_scheduled_task", manager.DelegationArgs{Instruction: "check access", Schedule: &manager.ChatScheduleRule{Frequency: "daily", Times: []string{"09:00"}}})
	if err != nil {
		t.Fatal(err)
	}
	revoked := raw.(manager.ChatSchedule)
	due := time.Now().UTC().Truncate(time.Minute).Add(-time.Minute)
	if _, err = db.Exec(`UPDATE manager_chat_schedules SET next_due=$2 WHERE id=$1`, revoked.ID, due); err != nil {
		t.Fatal(err)
	}
	var turn string
	if err = db.QueryRow(`SELECT active_turn_id FROM manager_sessions WHERE id=$1`, scope.Identity.SessionID).Scan(&turn); err != nil {
		t.Fatal(err)
	}
	reviewFinishTurn(t, db, scope.Identity.SessionID, scope.InputID, turn)
	if _, err = m.ArchiveConversation(ctx, scope.WorkspaceID, scope.ActorID, scope.ConversationID); err != nil {
		t.Fatal(err)
	}
	result, err := m.TickChatSchedule(ctx, revoked.ID, manager.TriggerScheduleTick{Kind: "chat", Version: 1, ExpectedDue: due, RecoveryStartedAt: due.Add(-time.Minute)})
	if err != nil || result.Status != "skipped" || result.Receipt != nil {
		t.Fatal(result, err)
	}
	var reason string
	if err = db.QueryRow(`SELECT reason FROM manager_chat_schedule_runs WHERE schedule_id=$1 AND task_id IS NULL`, revoked.ID).Scan(&reason); err != nil || !strings.Contains(reason, "archived") && !strings.Contains(reason, "revoked") {
		t.Fatal(reason, err)
	}
}

func TestChatScheduleManagementPermissionAndPause(t *testing.T) {
	db, m, d, scope := plannedFixture(t)
	ctx := t.Context()
	raw, err := d.Call(ctx, scope, "create_scheduled_task", manager.DelegationArgs{Instruction: "unchanged task goal", Schedule: &manager.ChatScheduleRule{Frequency: "daily", Times: []string{"09:00"}}})
	if err != nil {
		t.Fatal(err)
	}
	id := raw.(manager.ChatSchedule).ID
	member, maintainer, admin := manager.NewID(), manager.NewID(), manager.NewID()
	for actor, role := range map[string]string{member: "member", maintainer: "member", admin: "admin"} {
		insertWorkspaceAccount(t, db, actor, actor+"@example.com")
		reviewExec(t, db, `INSERT INTO platform_members(workspace_id,user_id,role,status)VALUES($1,$2,$3,'active')`, scope.WorkspaceID, actor, role)
	}
	agents := &platform.AgentService{Store: NewAgentStore(db)}
	grants, err := agents.Maintainers(ctx, scope.WorkspaceID, scope.ActorID, scope.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = agents.SaveMaintainers(ctx, scope.WorkspaceID, scope.ActorID, scope.AgentID, platform.EmployeeMaintainersUpdate{Version: grants.Version, People: []platform.SelectionRef{{Kind: "user", Provider: "web", WorkspaceID: scope.WorkspaceID, AgentID: scope.AgentID, ExternalID: maintainer}}}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	(&controller.Server{Service: m, Authenticate: func(_ context.Context, token string) (string, error) { return token, nil }}).Routes(mux)
	path := "/api/v1/workspaces/" + scope.WorkspaceID + "/agents/" + scope.AgentID + "/chat-schedules"
	request := func(actor, method, path, body string) (int, string) {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.AddCookie(&http.Cookie{Name: "kiko_session", Value: actor})
		req.Header.Set("X-CSRF-Protection", "1")
		req.Header.Set("Content-Type", "application/json")
		out := httptest.NewRecorder()
		mux.ServeHTTP(out, req)
		return out.Code, out.Body.String()
	}
	if code, body := request(member, "GET", path, ""); code < 400 || strings.Contains(body, id) {
		t.Fatal("ordinary member saw private plans", code, body)
	}
	if code, body := request(member, "PATCH", path+"/"+id, `{"version":"1","enabled":false}`); code < 400 {
		t.Fatal("ordinary member mutated plan", code, body)
	}
	for _, actor := range []string{scope.ActorID, maintainer, admin} {
		if code, body := request(actor, "GET", path, ""); code != 200 || !strings.Contains(body, id) || !strings.Contains(body, `"chat_type":"direct"`) {
			t.Fatal(code, body)
		}
	}
	if code, body := request(maintainer, "PATCH", path+"/"+id, `{"version":"1","enabled":false}`); code != 200 || !strings.Contains(body, `"status":"paused"`) {
		t.Fatal(code, body)
	}
	if code, body := request(admin, "PATCH", path+"/"+id, `{"version":"1","enabled":true}`); code != 409 {
		t.Fatal("stale update was accepted", code, body)
	}
	if code, body := request(admin, "PATCH", path+"/"+id, `{"version":"2","enabled":true,"schedule":{"frequency":"monthly","times":["16:00"],"month_days":[-1]}}`); code != 200 || !strings.Contains(body, `"status":"active"`) || !strings.Contains(body, "unchanged task goal") {
		t.Fatal(code, body)
	}
	var actor, source string
	if err = db.QueryRow(`SELECT actor_id,source_session_id FROM manager_chat_schedules WHERE id=$1`, id).Scan(&actor, &source); err != nil || actor != scope.ActorID || source != scope.Identity.SessionID {
		t.Fatal(actor, source, err)
	}
}

func TestChatScheduleRecoveryAndStaleTicks(t *testing.T) {
	db, m, d, scope := plannedFixture(t)
	ctx := t.Context()
	raw, err := d.Call(ctx, scope, "create_scheduled_task", manager.DelegationArgs{Instruction: "recover scheduled work", Schedule: &manager.ChatScheduleRule{Frequency: "daily", Times: []string{"09:00"}}})
	if err != nil {
		t.Fatal(err)
	}
	plan := raw.(manager.ChatSchedule)
	due := time.Now().UTC().Truncate(time.Minute).Add(-2 * time.Minute)
	reviewExec(t, db, `UPDATE manager_chat_schedules SET next_due=$2 WHERE id=$1`, plan.ID, due)
	tick := manager.TriggerScheduleTick{Kind: "chat", Version: 1, ExpectedDue: due, RecoveryStartedAt: due.Add(time.Minute)}
	result, err := m.TickChatSchedule(ctx, plan.ID, tick)
	if err != nil || result.Status != "skipped" || result.Receipt != nil {
		t.Fatal(result, err)
	}
	rows, err := m.ManagedChatSchedules(ctx, scope.WorkspaceID, scope.ActorID, scope.AgentID)
	if err != nil || len(rows) != 1 || len(rows[0].Runs) != 1 || rows[0].Runs[0].Reason != "missed_while_offline" ||
		!rows[0].Runs[0].Due.Equal(due) || rows[0].Runs[0].TaskID != nil || rows[0].NextDue == nil || !rows[0].NextDue.After(tick.RecoveryStartedAt) {
		t.Fatal(rows, err)
	}
	reviewExec(t, db, `UPDATE manager_chat_schedules SET next_due=$2 WHERE id=$1`, plan.ID, due.Add(time.Minute))
	tick.ExpectedDue = due.Add(time.Minute)
	paused := false
	updated, err := m.UpdateManagedChatSchedule(ctx, scope.WorkspaceID, scope.ActorID, scope.AgentID, plan.ID, manager.ManagedChatSchedulePatch{Version: 1, Enabled: &paused})
	if err != nil || updated.Status != "paused" || len(updated.Runs) != 1 {
		t.Fatal(updated, err)
	}
	result, err = m.TickChatSchedule(ctx, plan.ID, tick)
	if err != nil || result.Status != "stale" {
		t.Fatal(result, err)
	}
	active := true
	updated, err = m.UpdateManagedChatSchedule(ctx, scope.WorkspaceID, scope.ActorID, scope.AgentID, plan.ID, manager.ManagedChatSchedulePatch{Version: 2, Enabled: &active, Rule: &manager.ChatScheduleRule{Frequency: "daily", Times: []string{"16:30"}}})
	if err != nil || updated.Status != "active" || updated.NextDue == nil || !updated.NextDue.After(tick.ExpectedDue) || len(updated.Runs) != 1 {
		t.Fatal(updated, err)
	}
	result, err = m.TickChatSchedule(ctx, plan.ID, tick)
	if err != nil || result.Status != "stale" {
		t.Fatal(result, err)
	}
}

func TestChatSchedulePlatformDefaultAndExplicitTimezone(t *testing.T) {
	db, m, d, scope := plannedFixture(t)
	ctx := t.Context()
	store := m.Store.(*repo.Store)
	reviewExec(t, db, `UPDATE platform_accounts SET is_platform_admin=true WHERE id=$1`, scope.ActorID)
	settings := &TimezoneSettingsStore{DB: db}
	if _, err := settings.SaveAdmin(ctx, scope.ActorID, 0, "America/New_York"); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := store.HarnessConfig(ctx, scope.Identity.EngineID, scope.Identity.SessionID, scope.Identity.AssignmentID, scope.Identity.Generation, scope.InputID)
	if err != nil || cfg.ScheduleTimezone != "America/New_York" {
		t.Fatal(cfg.ScheduleTimezone, err)
	}
	for _, testcase := range []struct{ zone, want string }{{"", "America/New_York"}, {"Asia/Tokyo", "Asia/Tokyo"}} {
		raw, e := d.Call(ctx, scope, "create_scheduled_task", manager.DelegationArgs{Instruction: "run daily", Schedule: &manager.ChatScheduleRule{Frequency: "daily", Times: []string{"09:00"}, Timezone: testcase.zone}})
		if e != nil || raw.(manager.ChatSchedule).Rule.Timezone != testcase.want {
			t.Fatal(raw, e)
		}
	}
	if _, err = settings.SaveAdmin(ctx, scope.ActorID, 1, "UTC"); err != nil {
		t.Fatal(err)
	}
	rows, err := m.ManagedChatSchedules(ctx, scope.WorkspaceID, scope.ActorID, scope.AgentID)
	if err != nil || len(rows) != 2 {
		t.Fatal(rows, err)
	}
	seen := map[string]bool{}
	for _, row := range rows {
		seen[row.Rule.Timezone] = true
	}
	if !seen["America/New_York"] || !seen["Asia/Tokyo"] {
		t.Fatal(rows)
	}
}
