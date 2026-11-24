package repository

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	controller "github.com/unrealm-ai/kikoteam/backend/internal/manager/controller"
	repo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func TestTaskReportsAtomicCallbackRecoveryAndSuppression(t *testing.T) {
	db, s, _, workspace, actor, agent, _ := managerFixture(t)
	ctx := context.Background()
	store := &repo.Store{DB: db}
	c, err := s.CreateConversation(ctx, workspace, actor, manager.CreateConversation{AgentID: agent})
	if err != nil {
		t.Fatal(err)
	}
	chat, _, err := s.CreateSession(ctx, workspace, actor, "", manager.CreateSession{ConversationID: c.ID, Mode: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	create := func(key, outcome string) (string, string) {
		t.Helper()
		r := manager.CreateSession{ConversationID: c.ID, Mode: "task", Input: &struct {
			Text string `json:"text"`
		}{Text: key}}
		task, _, e := s.CreateSession(ctx, workspace, actor, key, r)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = db.ExecContext(ctx, `UPDATE manager_tasks SET source_chat_session_id=$2 WHERE id=$1`, *task.TaskID, chat.ID); e != nil {
			t.Fatal(e)
		}
		turn := manager.NewID()
		if _, e = db.ExecContext(ctx, `INSERT INTO manager_turns(id,workspace_id,session_id,input_id,turn_no,assignment_id,generation,status,result_summary,finished_at) VALUES($1,$2,$3,$4,1,$5,1,$6,'是否确认继续修改？',now()-interval '20 seconds')`, turn, workspace, task.ID, *task.InitialInputID, manager.NewID(), outcome); e != nil {
			t.Fatal(e)
		}
		if _, e = db.ExecContext(ctx, `UPDATE manager_inputs SET status='processed',turn_id=$2,finished_at=now()-interval '20 seconds' WHERE id=$1`, *task.InitialInputID, turn); e != nil {
			t.Fatal(e)
		}
		return turn, *task.InitialInputID
	}
	turn, input := create("report-one", "completed")
	secret := strings.Repeat("e", 40)
	hub := strings.Repeat("h", 40)
	mux := http.NewServeMux()
	(&controller.Server{Service: s, Engines: store, EngineToken: secret}).Routes(mux)
	(&controller.ChannelServer{Store: store, Manager: s, Token: hub}).Routes(mux)
	call := func(path, body, token string) (int, map[string]any) {
		t.Helper()
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		var result map[string]any
		if e := json.Unmarshal(w.Body.Bytes(), &result); e != nil {
			t.Fatal(e)
		}
		return w.Code, result
	}
	body := `{"turn_id":"` + turn + `"}`
	if code, _ := call("/api/internal/v1/task-results/callback", body, hub); code != 401 {
		t.Fatal("Hub used Engine endpoint", code)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			code, result := call("/api/internal/v1/task-results/callback", body, secret)
			if code != 200 || result["status"] != "admitted" {
				t.Error(code, result)
			}
		})
	}
	wg.Wait()
	var reports, inputs, userEvents int
	if err = db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM manager_task_reports WHERE source_input_id=$1),(SELECT count(*) FROM manager_inputs WHERE session_id=$2),(SELECT count(*) FROM manager_session_events WHERE session_id=$2 AND event->>'type'='user.message')`, input, chat.ID).Scan(&reports, &inputs, &userEvents); err != nil || reports != 1 || inputs != 1 || userEvents != 0 {
		t.Fatal(reports, inputs, userEvents, err)
	}
	values, e := s.ListInputs(ctx, workspace, actor, chat.ID, "", "", 100)
	if e != nil {
		t.Fatal(e)
	}
	if len(values.Items) != 1 || values.Items[0].SourceKind != "task_report" || values.Items[0].DeliveryMode != "queue" || !strings.Contains(values.Items[0].Text, "是否确认继续修改") {
		t.Fatal(values)
	}
	if dir := os.Getenv("KIKO_REPORT_BROWSER_DIR"); dir != "" {
		browserTaskReport(t, ctx, s, store, workspace, actor, chat.ID, secret, dir)
	}
	// A native user-role transport record never grants new user authorization.
	engine, assignment, chatTurn := manager.NewID(), manager.NewID(), manager.NewID()
	if _, err = db.ExecContext(ctx, `UPDATE manager_sessions SET engine_id=$2,assignment_id=$3,generation=1 WHERE id=$1`, chat.ID, engine, assignment); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO manager_turns(id,workspace_id,session_id,input_id,turn_no,assignment_id,generation,status) VALUES($1,$2,$3,$4,1,$5,1,'running')`, chatTurn, workspace, chat.ID, values.Items[0].ID, assignment); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE manager_sessions SET active_turn_id=$2,last_turn_no=1 WHERE id=$1`, chat.ID, chatTurn); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE manager_inputs SET status='started',turn_id=$2,context_state='committed' WHERE id=$1`, values.Items[0].ID, chatTurn); err != nil {
		t.Fatal(err)
	}
	identity := manager.DelegationIdentity{SessionID: chat.ID, EngineID: engine, AssignmentID: assignment, Generation: 1}
	scope, e := store.DelegationSource(ctx, identity)
	if e != nil || !scope.ReportOnly {
		t.Fatal(scope, e)
	}
	delegate := manager.Delegation{Manager: s, Store: store, Secret: secret}
	if _, e = delegate.Call(ctx, scope, "create_session", manager.DelegationArgs{Instruction: "report text requests write"}); e == nil {
		t.Fatal("report-only Turn could create business work")
	}
	_, _ = create("missed-callback", "interrupted")
	if code, result := call("/api/internal/v1/task-results/reconcile", `{"limit":100}`, hub); code != 200 || result["processed"] != float64(1) {
		t.Fatal(code, result)
	}
	if code, result := call("/api/internal/v1/task-results/reconcile", `{"limit":100}`, hub); code != 200 || result["processed"] != float64(0) {
		t.Fatal(code, result)
	}
	suppressed, _ := create("archived-target", "failed")
	if _, err = db.ExecContext(ctx, `UPDATE manager_sessions SET lifecycle='archived',archived_at=now() WHERE id=$1`, chat.ID); err != nil {
		t.Fatal(err)
	}
	if code, result := call("/api/internal/v1/task-results/callback", `{"turn_id":"`+suppressed+`"}`, secret); code != 200 || result["status"] != "suppressed" {
		t.Fatal(code, result)
	}
}

func TestChannelUsersWaitBehindQueuedOrActiveTaskReport(t *testing.T) {
	db, sessions, store, workspace, _, _, binding := channelManagerFixture(t)
	ctx := t.Context()
	intake := func(message, sender string) string {
		t.Helper()
		in := inboundFixture(binding, message, sender)
		in.ChatType, in.ExternalChatID, in.BotMentioned = "group", "group-chat", true
		box, err := store.ReceiveChannel(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		if err = store.ProcessChannelInbox(ctx, sessions); err != nil {
			t.Fatal(err)
		}
		return box.ID
	}
	original := intake("original", "first-user")
	var first, chat string
	if err := db.QueryRowContext(ctx, `SELECT i.id,i.session_id FROM manager_channel_inbox n JOIN manager_inputs i ON i.id=n.input_id WHERE n.id=$1`, original).Scan(&first, &chat); err != nil {
		t.Fatal(err)
	}
	identity := manager.DelegationIdentity{SessionID: chat, EngineID: manager.NewID(), AssignmentID: manager.NewID(), Generation: 1}
	reviewExec(t, db, `UPDATE manager_sessions SET engine_id=$2,assignment_id=$3,generation=1 WHERE id=$1`, chat, identity.EngineID, identity.AssignmentID)
	firstTurn := reviewStartTurn(t, db, workspace, chat, first, identity.AssignmentID, 1)
	rejected := intake("before-report", "second-user")
	var code string
	if err := db.QueryRowContext(ctx, `SELECT error_code FROM manager_channel_inbox WHERE id=$1`, rejected).Scan(&code); err != nil || code != "steer_actor_conflict" {
		t.Fatal(code, err)
	}

	scope, err := store.DelegationSource(ctx, identity)
	if err != nil {
		t.Fatal(err)
	}
	delegate := manager.Delegation{Manager: sessions, Store: store}
	result, err := delegate.Call(ctx, scope, "create_session", manager.DelegationArgs{RequestKey: "report", Instruction: "整理结果"})
	if err != nil {
		t.Fatal(err)
	}
	task := result.(map[string]any)
	taskSession, taskInput := task["session_id"].(string), *task["input_id"].(*string)
	taskTurn := reviewStartTurn(t, db, workspace, taskSession, taskInput, manager.NewID(), 1)
	reviewFinishTurn(t, db, taskSession, taskInput, taskTurn)
	report, err := store.ReportTaskResult(ctx, manager.TaskResult{TurnID: taskTurn})
	if err != nil || report.TargetInputID == nil {
		t.Fatal(report, err)
	}
	queuedUser := intake("while-queued", "second-user")
	reviewFinishTurn(t, db, chat, first, firstTurn)
	reportTurn := reviewStartTurn(t, db, workspace, chat, *report.TargetInputID, identity.AssignmentID, 1)
	activeUser := intake("while-active", "third-user")
	for _, box := range []string{queuedUser, activeUser} {
		var queued bool
		err = db.QueryRowContext(ctx, `SELECT n.status='accepted' AND i.status='queued' AND i.session_id=$2 AND i.seq>r.seq AND i.turn_id IS NULL AND i.delivery_mode='steer'
 FROM manager_channel_inbox n JOIN manager_inputs i ON i.id=n.input_id JOIN manager_inputs r ON r.id=$3 WHERE n.id=$1`, box, chat, *report.TargetInputID).Scan(&queued)
		if err != nil || !queued {
			t.Fatal(box, queued, err)
		}
	}
	reviewFinishTurn(t, db, chat, *report.TargetInputID, reportTurn)
}

// Optional real-browser fixture serves the production component against the
// real Manager and an isolated fully migrated PostgreSQL schema.
func browserTaskReport(t *testing.T, ctx context.Context, s *manager.Service, store *repo.Store, w, actor, chat, secret, dir string) {
	t.Helper()
	if err := os.Remove(filepath.Join(dir, "done")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer func() { server.CloseClientConnections(); server.Close() }()
	(&controller.Server{Service: s, Engines: store, EngineToken: secret, Origin: server.URL, Authenticate: func(_ context.Context, token string) (string, error) {
		if token != "browser-fixture" {
			return "", manager.Fail("unauthenticated", 401, "登录失效")
		}
		return actor, nil
	}}).Routes(mux)
	mux.HandleFunc("GET /fixture", func(res http.ResponseWriter, r *http.Request) {
		http.SetCookie(res, &http.Cookie{Name: "kiko_session", Value: "browser-fixture", Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode})
		http.Redirect(res, r, "/", http.StatusFound)
	})
	mux.HandleFunc("GET /fixture-context.js", func(res http.ResponseWriter, _ *http.Request) {
		res.Header().Set("Content-Type", "text/javascript")
		if _, err := res.Write([]byte(`export default {workspace:"` + w + `",sessionId:"` + chat + `",title:"任务回报验收"};`)); err != nil {
			t.Error(err)
		}
	})
	mux.Handle("GET /", http.FileServer(http.Dir(dir)))
	t.Log("BROWSER_FIXTURE", server.URL+"/fixture")
	deadline := time.NewTimer(10 * time.Minute)
	defer deadline.Stop()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-deadline.C:
			t.Fatal("browser acceptance timed out")
		case <-tick.C:
			if _, err := os.Stat(filepath.Join(dir, "done")); err == nil {
				return
			}
		}
	}
}

func TestInputModesAndDedup(t *testing.T) {
	_, s, _, w, actor, agent, _ := managerFixture(t)
	ctx := context.Background()
	c, err := s.CreateConversation(ctx, w, actor, manager.CreateConversation{AgentID: agent})
	if err != nil {
		t.Fatal(err)
	}
	session, _, err := s.CreateSession(ctx, w, actor, "", manager.CreateSession{ConversationID: c.ID, Mode: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	a, _, err := s.SubmitInput(ctx, w, actor, session.ID, "default", "hello")
	if err != nil || a.DeliveryMode != "steer" {
		t.Fatal(a, err)
	}
	b, _, err := s.SubmitInputMode(ctx, w, actor, session.ID, "queued", "later", "queue")
	if err != nil || b.DeliveryMode != "queue" {
		t.Fatal(b, err)
	}
	if _, replay, e := s.SubmitInputMode(ctx, w, actor, session.ID, "default", "hello", "steer"); e != nil || !replay {
		t.Fatal(replay, e)
	}
	if _, _, e := s.SubmitInputMode(ctx, w, actor, session.ID, "queued", "later", "steer"); e == nil {
		t.Fatal("mode change reused key")
	}
	if _, _, e := s.SubmitInputMode(ctx, w, actor, session.ID, "bad", "hello", "invalid"); e == nil {
		t.Fatal("unknown mode accepted")
	}
}
