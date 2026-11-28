package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	managercontroller "github.com/unrealm-ai/kikoteam/backend/internal/manager/controller"
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/mcp/workitems"
	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func TestReportedProgressMCPHistoryAndTaskReads(t *testing.T) {
	db, m, _, w, actor, agent, _ := managerFixture(t)
	store := m.Store.(*managerrepo.Store)
	space := historyTestSpace(t, m, w, actor, agent, "progress", nil)
	task := historyTestTask(t, m, w, actor, agent, space.ID, "progress-run")
	id, turn := startHistoryTestTask(t, store, w, task.ID, *task.InitialInputID)
	secret := strings.Repeat("p", 40)
	mux := http.NewServeMux()
	if err := workitems.RegisterProgress(mux, store, secret); err != nil {
		t.Fatal(err)
	}
	(&managercontroller.Server{Service: m, Engines: store, EngineToken: secret, ResolveModelKey: func(context.Context, string, string, manager.Configuration) (string, error) {
		return "fixture-key", nil
	}}).Routes(mux)
	token := historyTestHarness(t, mux, secret, id).TaskProgressToken
	if token == "" {
		t.Fatal("missing input-scoped task progress credential")
	}
	update := func(body string, input string) (int, string) {
		req := httptest.NewRequest(http.MethodPost, "/mcp/task-progress", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"update_task_progress","arguments":`+body+`}}`))
		req.Header.Set(manager.ProgressInputHeader, input)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("MCP-Protocol-Version", "2025-11-25")
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, req)
		return res.Code, res.Body.String()
	}
	first := `{"base_plan_version":"0","summary":"正在读取资料","steps":[{"id":"read","label":"读取文件","status":"in_progress"}]}`
	if status, _ := update(first, manager.NewID()); status != 401 {
		t.Fatalf("foreign input: %d", status)
	}
	receipts := make(chan string, 2)
	for range 2 {
		go func() { _, raw := update(first, id.InputID); receipts <- raw }()
	}
	a, b := <-receipts, <-receipts
	if a != b || !strings.Contains(a, `"event_seq"`) {
		t.Fatalf("duplicate receipts differ: %s / %s", a, b)
	}
	ctx := t.Context()
	history, err := m.ListEvents(ctx, w, actor, task.ID, turn, manager.ProgressEvent, "", 50)
	if err != nil || len(history.Items) != 1 || bytes.Contains(history.Items[0], []byte("request_digest")) {
		t.Fatalf("history: %+v %v", history, err)
	}
	if _, raw := update(`{"base_plan_version":"0","summary":"stale"}`, id.InputID); !strings.Contains(raw, "progress_conflict") {
		t.Fatal(raw)
	}
	if _, raw := update(`{"base_plan_version":"1","summary":"资料已就绪"}`, id.InputID); !strings.Contains(raw, `"plan_version":"2"`) {
		t.Fatal(raw)
	}
	if _, raw := update(first, id.InputID); raw != a {
		t.Fatalf("late replay: %s", raw)
	}
	events, _, err := m.StreamEvents(ctx, w, actor, task.ID, turn, manager.ProgressEvent, *history.ResumeCursor)
	if err != nil || len(events) != 1 {
		t.Fatalf("resume: %d, %v", len(events), err)
	}
	var progress manager.ReportedProgress
	if err = json.Unmarshal(events[0], &progress); err != nil || progress.PlanVersion != "2" || len(progress.Steps) != 1 {
		t.Fatalf("%s %v", events[0], err)
	}
	detail, err := store.Task(ctx, w, actor, *task.TaskID)
	if err != nil || detail.ReportedProgress == nil || detail.ReportedProgress.Seq != progress.Seq || detail.Progress == nil {
		t.Fatalf("detail snapshot: %+v %v", detail, err)
	}
	tasks, err := store.Tasks(ctx, w, actor, manager.TaskFilter{TaskSpace: space.ID, Limit: 10})
	if err != nil || len(tasks) != 1 || tasks[0].ReportedProgress.Summary != progress.Summary {
		t.Fatalf("list: %+v %v", tasks, err)
	}
	if _, err = store.Task(ctx, w, manager.NewID(), *task.TaskID); err == nil {
		t.Fatal("unauthorized task read")
	}
	reviewExec(t, db, `UPDATE manager_sessions SET stop_revision=stop_revision+1 WHERE id=$1`, task.ID)
	if _, raw := update(`{"base_plan_version":"2","summary":"late"}`, id.InputID); !strings.Contains(raw, `"isError":true`) {
		t.Fatal(raw)
	}
	reviewExec(t, db, `UPDATE manager_sessions SET active_turn_id=NULL,applied_stop_revision=stop_revision WHERE id=$1`, task.ID)
	reviewExec(t, db, `UPDATE manager_turns SET status='completed',finished_at=now() WHERE id=$1`, turn)
	reviewExec(t, db, `UPDATE manager_inputs SET status='processed',finished_at=now() WHERE id=$1`, id.InputID)
	next, _, err := m.SubmitInput(ctx, w, actor, task.ID, "progress-new-input", "继续执行")
	if err != nil {
		t.Fatal(err)
	}
	nextTurn := manager.NewID()
	reviewExec(t, db, `INSERT INTO manager_turns(id,workspace_id,session_id,input_id,turn_no,assignment_id,generation,status) VALUES($1,$2,$3,$4,2,$5,1,'running')`, nextTurn, w, task.ID, next.ID, id.AssignmentID)
	reviewExec(t, db, `UPDATE manager_sessions SET active_turn_id=$2 WHERE id=$1`, task.ID, nextTurn)
	detail, err = store.Task(ctx, w, actor, *task.TaskID)
	if err != nil || detail.ReportedProgress != nil {
		t.Fatalf("previous turn displayed as current: %+v %v", detail.ReportedProgress, err)
	}
}
