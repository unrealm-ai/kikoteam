package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	managercontroller "github.com/unrealm-ai/kikoteam/backend/internal/manager/controller"
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/mcp/taskspacehistory"
	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	platform "github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestTaskSpaceHistoryMCPPaginationAndExecutionAuthority(t *testing.T) {
	db, m, _, workspace, actor, agent, _ := managerFixture(t)
	store := m.Store.(*managerrepo.Store)
	space := historyTestSpace(t, m, workspace, actor, agent, "history-space", nil)
	otherSpace := historyTestSpace(t, m, workspace, actor, agent, "history-other-space", nil)
	source := historyTestTask(t, m, workspace, actor, agent, space.ID, "history-source")
	target := historyTestTask(t, m, workspace, actor, agent, space.ID, "history-target")
	other := historyTestTask(t, m, workspace, actor, agent, otherSpace.ID, "history-other")
	reviewExec(t, db, `UPDATE manager_tasks SET created_at='2026-11-19 UTC' WHERE id IN ($1,$2)`, source.TaskID, target.TaskID)
	id, _ := startHistoryTestTask(t, store, workspace, source.ID, *source.InitialInputID)
	targetID, turn := startHistoryTestTask(t, store, workspace, target.ID, *target.InitialInputID)
	reviewExec(t, db, `INSERT INTO manager_session_events(workspace_id,session_id,seq,event) VALUES
 ($1,$2,101,'{"type":"agent.tool_result","name":"lookup","status":"completed","input":"private-arguments","output":"private-output"}'),
 ($1,$2,102,'{"type":"agent.thinking","content":[{"type":"text","text":"private-thinking"}]}'),
 ($1,$2,103,'{"type":"agent.message","status":"completed","content":[{"type":"text","text":"finished "},{"type":"text","text":"result"}]}')`, workspace, target.ID)
	reviewExec(t, db, `UPDATE manager_sessions SET last_event_seq=103 WHERE id=$1`, target.ID)

	secret := strings.Repeat("h", 40)
	mux := http.NewServeMux()
	if err := taskspacehistory.Register(mux, &manager.TaskSpaceHistory{Store: store, CursorSecret: []byte(secret)}, secret, nil); err != nil {
		t.Fatal(err)
	}
	(&managercontroller.Server{Service: m, Engines: store, EngineToken: secret, ResolveModelKey: func(context.Context, string, string, manager.Configuration) (string, error) {
		return "fixture-model-key", nil
	}}).Routes(mux)
	token := historyTestHarness(t, mux, secret, id).TaskSpaceHistoryToken
	sourceIdentity, sourceToken := id, token
	if token == "" {
		t.Fatal("harness did not issue a TaskSpace history token")
	}
	post := func(method, token, input, cookie string, params any) *httptest.ResponseRecorder {
		t.Helper()
		body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPost, "/mcp/task-space-history", strings.NewReader(string(body)))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-Kiko-Task-Space-Input", input)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		r.Header.Set("MCP-Protocol-Version", "2025-11-25")
		if cookie != "" {
			r.Header.Set("Cookie", cookie)
		}
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, r)
		return res
	}
	call := func(name string, args map[string]any, wantError string) json.RawMessage {
		t.Helper()
		res := post("tools/call", token, id.InputID, "", map[string]any{"name": "task_space_history_" + name, "arguments": args})
		var rpc struct {
			Error  *json.RawMessage
			Result struct {
				IsError           bool
				StructuredContent json.RawMessage
			}
		}
		if err := json.Unmarshal(res.Body.Bytes(), &rpc); err != nil || res.Code != 200 || (rpc.Result.IsError || rpc.Error != nil) != (wantError != "") || (wantError != "any-error" && !strings.Contains(res.Body.String(), wantError)) {
			t.Fatalf("call %s: %d %s, expected error %q", name, res.Code, res.Body, wantError)
		}
		return rpc.Result.StructuredContent
	}
	list := post("tools/list", token, id.InputID, "", map[string]any{})
	if list.Code != 200 || !strings.Contains(list.Body.String(), `"name":"task_space_history_list_tasks"`) || !strings.Contains(list.Body.String(), `"name":"task_space_history_get_task"`) || !strings.Contains(list.Body.String(), `"name":"task_space_history_save_summary"`) {
		t.Fatalf("tool discovery: %d %s", list.Code, list.Body)
	}
	foreign, err := manager.SignDelegation(secret, id.DelegationIdentity)
	if err != nil {
		t.Fatal(err)
	}
	for _, res := range []*httptest.ResponseRecorder{
		post("tools/list", token, manager.NewID(), "", map[string]any{}),
		post("tools/list", foreign, id.InputID, "", map[string]any{}),
		post("tools/list", token, id.InputID, "session=browser", map[string]any{}),
	} {
		if res.Code != 401 {
			t.Fatalf("mismatched credential accepted: %d %s", res.Code, res.Body)
		}
	}
	var first, second manager.Page[manager.TaskSpaceHistorySummary]
	historyTestDecode(t, call("list_tasks", map[string]any{"limit": 1}, ""), &first)
	if len(first.Items) != 1 || first.NextCursor == nil {
		t.Fatalf("first page: %+v", first)
	}
	historyTestDecode(t, call("list_tasks", map[string]any{"limit": 1, "cursor": *first.NextCursor}, ""), &second)
	if len(second.Items) != 1 || second.NextCursor != nil || first.Items[0].TaskID == second.Items[0].TaskID {
		t.Fatalf("second page: %+v", second)
	}
	historyTestSummaryLifecycle(t, db, targetID, turn, *target.TaskID, call, func(source manager.TaskSpaceHistoryIdentity) {
		id = source
		token = historyTestHarness(t, mux, secret, id).TaskSpaceHistoryToken
	}, func() { id, token = sourceIdentity, sourceToken })
	call("get_task", map[string]any{"task_id": *other.TaskID}, "resource_not_found")
	call("get_task", map[string]any{"task_id": *target.TaskID, "cursor": "legacy"}, "cursor")
	call("list_tasks", map[string]any{"task_space_id": otherSpace.ID}, "task_space_id")
	reviewExec(t, db, `UPDATE manager_sessions SET stop_revision=stop_revision+1 WHERE id=$1`, source.ID)
	call("list_tasks", map[string]any{}, "task_space_history_forbidden")
}

func historyTestSummaryLifecycle(t *testing.T, db *sql.DB, target manager.TaskSpaceHistoryIdentity, turn, task string, call func(string, map[string]any, string) json.RawMessage, use func(manager.TaskSpaceHistoryIdentity), restore func()) {
	t.Helper()
	args := map[string]any{"task_id": task}
	check := func(progress, result bool) {
		t.Helper()
		raw := call("get_task", args, "")
		var payload struct {
			Task     manager.TaskSpaceHistorySummary
			Progress *struct {
				Text   string
				TurnID string `json:"turn_id"`
			} `json:"progress_summary"`
			Result *struct {
				Text   string
				TurnID string `json:"turn_id"`
			} `json:"result_summary"`
		}
		historyTestDecode(t, raw, &payload)
		if payload.Task.TaskID != task || payload.Task.LatestTurn == nil || payload.Task.LatestTurn.ID != turn || payload.Task.LatestInput == nil {
			t.Fatalf("missing latest turn facts: %s", raw)
		}
		if (payload.Progress != nil) != progress || (payload.Result != nil) != result {
			t.Fatalf("summary visibility: %s", raw)
		}
		if progress && (payload.Progress.Text != "curated progress" || payload.Progress.TurnID != turn) || result && (payload.Result.Text != "curated result" || payload.Result.TurnID != turn) {
			t.Fatalf("summary text or provenance: %s", raw)
		}
		for _, forbidden := range []string{`"events"`, `"attachments"`, `"tool_calls"`, "private-", "finished result", "run history fixture"} {
			if strings.Contains(string(raw), forbidden) {
				t.Fatalf("raw history leaked %q: %s", forbidden, raw)
			}
		}
	}
	check(false, false)
	use(target)
	call("save_summary", map[string]any{"task_id": manager.NewID(), "progress": "spoof"}, "task_id")
	call("save_summary", map[string]any{"progress": strings.Repeat("改", 601)}, "any-error")
	call("save_summary", map[string]any{"result": strings.Repeat("改", 1601)}, "any-error")
	var saved struct{ Saved bool }
	historyTestDecode(t, call("save_summary", map[string]any{"progress": "curated progress", "result": "curated result"}, ""), &saved)
	if !saved.Saved {
		t.Fatal("save was not acknowledged")
	}
	check(true, false)
	reviewExec(t, db, `UPDATE manager_turns SET status='completed',has_unknown_tool_results=true,result_summary='finished result',finished_at=now() WHERE id=$1`, turn)
	reviewExec(t, db, `UPDATE manager_sessions SET active_turn_id=NULL WHERE id=$1`, target.SessionID)
	reviewExec(t, db, `UPDATE manager_inputs SET status='processed',finished_at=now() WHERE id=$1`, target.InputID)
	call("save_summary", map[string]any{"progress": "after completion"}, "task_space_history_forbidden")
	call("get_task", args, "task_space_history_forbidden")
	restore()
	check(true, false)
	reviewExec(t, db, `UPDATE manager_turns SET has_unknown_tool_results=false WHERE id=$1`, turn)
	check(true, true)
}

func TestTaskSpaceHistorySharesActualSpaceAcrossSources(t *testing.T) {
	store, m, workspace, actor, agent, trigger, credential := triggerFixture(t)
	ctx := context.Background()
	key := "shared-history-business-key"
	space := historyTestSpace(t, m, workspace, actor, agent, "history-trigger-space", &key)
	manual := historyTestTask(t, m, workspace, actor, agent, space.ID, "history-manual-task")
	other, err := store.SaveTrigger(ctx, workspace, actor, agent, "", manager.TriggerWrite{Name: "Other history trigger", Kind: trigger.Kind, Config: trigger.Config, AllowedActions: trigger.AllowedActions})
	if err != nil {
		t.Fatal(err)
	}
	otherCredential, err := store.CreateTriggerCredential(ctx, workspace, actor, agent, other.ID)
	if err != nil {
		t.Fatal(err)
	}
	other, err = store.SaveTrigger(ctx, workspace, actor, agent, other.ID, manager.TriggerWrite{Name: other.Name, Version: other.Version, Kind: other.Kind, Enabled: true, Config: other.Config, AllowedActions: other.AllowedActions})
	if err != nil {
		t.Fatal(err)
	}
	create := func(binding manager.Trigger, cred manager.TriggerCredential, event string) manager.TriggerReceipt {
		t.Helper()
		receipt, e := signedTrigger(t, store, binding, cred, manager.TriggerCommand{Version: 1, EventID: event, Action: "create_task", BusinessKey: &key, Description: "history task"})
		if e != nil || receipt.TaskSpaceID != space.ID {
			t.Fatalf("trigger did not join space: %+v %v", receipt, e)
		}
		return receipt
	}
	old := create(trigger, credential, "history-old")
	current := create(trigger, credential, "history-current")
	foreign := create(other, otherCredential, "history-foreign")
	scheduled := historyScheduledTask(t, store, workspace, actor, agent, key, space.ID)
	outsider := historyOtherWorkspaceTask(t, m, store, actor, key)
	id, _ := startHistoryTestTask(t, store, workspace, current.SessionID, current.InputID)
	history := manager.TaskSpaceHistory{Store: store, CursorSecret: []byte(strings.Repeat("h", 40))}
	manualID, manualTurn := startHistoryTestTask(t, store, workspace, manual.ID, *manual.InitialInputID)
	if saved, e := history.Save(ctx, manualID, manager.TaskSpaceHistorySaveArgs{Progress: "manual source summary"}); e != nil || !saved.Saved {
		t.Fatal(saved, e)
	}
	detail, e := history.Get(ctx, id, manager.TaskSpaceHistoryDetailArgs{TaskID: *manual.TaskID})
	if e != nil || detail.ProgressSummary == nil || detail.ProgressSummary.Text != "manual source summary" || detail.ProgressSummary.TurnID != manualTurn || detail.ProgressSummary.UpdatedAt.IsZero() {
		t.Fatalf("trigger read manual task summary: %+v %v", detail, e)
	}
	page, err := history.List(ctx, id, manager.TaskSpaceHistoryArgs{})
	if err != nil || len(page.Items) != 5 {
		t.Fatalf("trigger task list: %+v %v", page, err)
	}
	for _, task := range []string{old.TaskID, *manual.TaskID, foreign.TaskID, scheduled} {
		if _, err = history.Get(ctx, id, manager.TaskSpaceHistoryDetailArgs{TaskID: task}); err != nil {
			t.Fatalf("same TaskSpace scope denied %s: %v", task, err)
		}
	}
	if _, err = history.Get(ctx, id, manager.TaskSpaceHistoryDetailArgs{TaskID: outsider}); err == nil {
		t.Fatal("same business key in a foreign workspace granted access")
	}
	reviewExec(t, store.DB, `UPDATE manager_triggers SET enabled=false WHERE id=$1`, trigger.ID)
	if _, err = history.List(ctx, id, manager.TaskSpaceHistoryArgs{}); err == nil {
		t.Fatal("disabled trigger retained history access")
	}
}

func historyTestDecode(t *testing.T, raw json.RawMessage, dest any) {
	t.Helper()
	if err := json.Unmarshal(raw, dest); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
}

func historyTestHarness(t *testing.T, mux *http.ServeMux, secret string, id manager.TaskSpaceHistoryIdentity) manager.HarnessConfig {
	t.Helper()
	q := url.Values{"engine_id": {id.EngineID}, "assignment_id": {id.AssignmentID}, "generation": {strconv.FormatInt(id.Generation, 10)}, "input_id": {id.InputID}}
	req := httptest.NewRequest(http.MethodGet, "/api/internal/v1/sessions/"+id.SessionID+"/harness-config?"+q.Encode(), nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	if res.Code != 200 {
		t.Fatalf("harness configuration: %d %s", res.Code, res.Body)
	}
	var cfg manager.HarnessConfig
	historyTestDecode(t, res.Body.Bytes(), &cfg)
	return cfg
}

func historyTestSpace(t *testing.T, m *manager.Service, workspace, actor, agent, key string, businessKey *string) manager.TaskSpace {
	t.Helper()
	space, _, err := m.CreateTaskSpace(context.Background(), workspace, actor, key, manager.TaskSpaceCreate{AgentID: agent, BusinessKey: businessKey})
	if err != nil {
		t.Fatal(err)
	}
	return space
}

func historyTestTask(t *testing.T, m *manager.Service, workspace, actor, agent, space, key string) manager.Session {
	t.Helper()
	q := manager.CreateSession{TaskSpaceID: space, AgentID: agent, Mode: "task"}
	q.Input = &struct {
		Text string `json:"text"`
	}{Text: "run history fixture"}
	session, _, err := m.CreateSession(context.Background(), workspace, actor, key, q)
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func startHistoryTestTask(t *testing.T, store *managerrepo.Store, workspace, session, input string) (manager.TaskSpaceHistoryIdentity, string) {
	t.Helper()
	id := manager.TaskSpaceHistoryIdentity{InputID: input, DelegationIdentity: manager.DelegationIdentity{SessionID: session, EngineID: manager.NewID(), AssignmentID: manager.NewID(), Generation: 1}}
	if err := store.RegisterEngine(context.Background(), manager.Engine{ID: id.EngineID, HostID: "history-" + session, Endpoint: "http://127.0.0.1:1", Capacity: 10}); err != nil {
		t.Fatal(err)
	}
	turn := manager.NewID()
	reviewExec(t, store.DB, `UPDATE manager_sessions SET engine_id=$2,assignment_id=$3,generation=1 WHERE id=$1`, session, id.EngineID, id.AssignmentID)
	reviewExec(t, store.DB, `INSERT INTO manager_turns(id,workspace_id,session_id,input_id,turn_no,assignment_id,generation,status)VALUES($1,$2,$3,$4,1,$5,1,'running')`, turn, workspace, session, input, id.AssignmentID)
	reviewExec(t, store.DB, `UPDATE manager_sessions SET active_turn_id=$2 WHERE id=$1`, session, turn)
	reviewExec(t, store.DB, `UPDATE manager_inputs SET status='started',turn_id=$2 WHERE id=$1`, input, turn)
	return id, turn
}

func historyScheduledTask(t *testing.T, store *managerrepo.Store, w, actor, agent, key, space string) string {
	t.Helper()
	ctx := context.Background()
	raw, err := json.Marshal(map[string]any{"cron": "* * * * *", "timezone": "Asia/Shanghai", "description": "scheduled history task", "businessKey": map[string]string{"mode": "fixed", "value": key}})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := store.SaveTrigger(ctx, w, actor, agent, "", manager.TriggerWrite{Name: "History schedule", Kind: "schedule", Enabled: true, Config: raw, AllowedActions: []string{"create_task"}})
	if err != nil {
		t.Fatal(err)
	}
	due := time.Now().UTC().Truncate(time.Minute).Add(-time.Minute)
	reviewExec(t, store.DB, `UPDATE manager_triggers SET next_due=$2 WHERE id=$1`, rule.ID, due)
	result, err := store.TickTriggerSchedule(ctx, rule.ID, manager.TriggerScheduleTick{Version: rule.Version, ExpectedDue: due, RecoveryStartedAt: due.Add(-time.Minute)})
	if err != nil || result.Receipt == nil || result.Receipt.TaskSpaceID != space {
		t.Fatalf("scheduled activity did not join space: %+v %v", result, err)
	}
	return result.Receipt.TaskID
}
func historyOtherWorkspaceTask(t *testing.T, m *manager.Service, store *managerrepo.Store, actor, key string) string {
	t.Helper()
	ctx := context.Background()
	ws, e := (&platform.WorkspaceService{Store: NewWorkspaceStore(store.DB)}).Create(ctx, actor, "second history workspace", " ")
	if e != nil {
		t.Fatal(e)
	}
	models := &platform.ModelService{Store: NewModelStore(store.DB), Prober: modelProbeStub{}, Secret: []byte(strings.Repeat("m", 32)), NormalizeEndpoint: func(_ string, u string) (string, error) { return u, nil }}
	model := saveTestModel(t, models, ws.ID, actor)
	a, e := (&platform.AgentService{Store: NewAgentStore(store.DB)}).Create(ctx, ws.ID, actor, platform.AgentCreate{Name: "Other", ModelProfileID: model.ID})
	if e != nil {
		t.Fatal(e)
	}
	space := historyTestSpace(t, m, ws.ID, actor, a.ID, "cross-workspace-history", &key)
	task := historyTestTask(t, m, ws.ID, actor, a.ID, space.ID, "cross-workspace-task")
	return *task.TaskID
}
