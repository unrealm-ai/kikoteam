package repository

import (
	"context"
	"database/sql"
	delegation "github.com/unrealm-ai/kikoteam/backend/internal/manager/mcp/taskdelegation"
	repository "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func plannedFixture(t *testing.T) (*sql.DB, *manager.Service, *manager.Delegation, manager.DelegationScope) {
	t.Helper()
	db, s, _, w, actor, agent, _ := managerFixture(t)
	ctx := context.Background()
	store := &repository.Store{DB: db}
	c, err := s.CreateConversation(ctx, w, actor, manager.CreateConversation{AgentID: agent})
	if err != nil {
		t.Fatal(err)
	}
	req := manager.CreateSession{ConversationID: c.ID, Mode: "chat"}
	req.Input = &struct {
		Text string `json:"text"`
	}{Text: "delegate"}
	source, _, err := s.CreateSession(ctx, w, actor, "source-input", req)
	if err != nil {
		t.Fatal(err)
	}
	var initialInstruction string
	if err = db.QueryRowContext(ctx, `SELECT configuration->>'instruction' FROM manager_sessions WHERE id=$1`, source.ID).Scan(&initialInstruction); err != nil || initialInstruction != manager.PlatformChatInstruction {
		t.Fatalf("new chat did not snapshot platform policy: %v", err)
	}
	engine := manager.NewID()
	if err = store.RegisterEngine(ctx, manager.Engine{ID: engine, HostID: "test-host", Endpoint: "http://127.0.0.1:9001", Capacity: 20}); err != nil {
		t.Fatal(err)
	}
	if err = store.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	identity := manager.DelegationIdentity{SessionID: source.ID, EngineID: engine}
	if err = db.QueryRowContext(ctx, `SELECT assignment_id,generation FROM manager_sessions WHERE id=$1`, source.ID).Scan(&identity.AssignmentID, &identity.Generation); err != nil {
		t.Fatal(err)
	}
	turn := manager.NewID()
	if _, err = db.ExecContext(ctx, `INSERT INTO manager_turns(id,workspace_id,session_id,input_id,turn_no,assignment_id,generation,status) VALUES($1,$2,$3,$4,1,$5,$6,'running')`, turn, w, source.ID, *source.InitialInputID, identity.AssignmentID, identity.Generation); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE manager_inputs SET status='started',turn_id=$2 WHERE id=$1`, *source.InitialInputID, turn); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE manager_sessions SET active_turn_id=$2,last_turn_no=1 WHERE id=$1`, source.ID, turn); err != nil {
		t.Fatal(err)
	}
	scope, err := store.DelegationSource(ctx, identity)
	if err != nil {
		t.Fatal(err)
	}
	return db, s, &manager.Delegation{Manager: s, Store: store}, scope
}
func TestPlannedTaskAtomicStartAndCancellation(t *testing.T) {
	db, m, d, scope := plannedFixture(t)
	ctx := context.Background()
	create := manager.DelegationArgs{Instruction: "initial requirements"}
	raw, err := d.Call(ctx, scope, "create_todo", create)
	if err != nil {
		t.Fatal(err)
	}
	v := raw.(manager.PlannedTask)
	if v.State != "planned" || v.Version != 1 {
		t.Fatal(v)
	}
	if _, _, err = m.SubmitInput(ctx, scope.WorkspaceID, scope.ActorID, v.SessionID, "bypass", "must not run"); err == nil {
		t.Fatal("direct Input bypass")
	}
	if _, err = d.Call(ctx, scope, "stop_session", manager.DelegationArgs{SessionID: v.SessionID}); err == nil {
		t.Fatal("stop falsely cancels todo")
	}
	raw, err = d.Call(ctx, scope, "supplement_todo", manager.DelegationArgs{SessionID: v.SessionID, Instruction: "more context", ExpectedVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	v = raw.(manager.PlannedTask)
	if v.Version != 2 || v.Instruction != "initial requirements\n\nmore context" {
		t.Fatal(v)
	}
	var count int
	if err = db.QueryRow(`SELECT count(*)FROM manager_inputs WHERE session_id=$1`, v.SessionID).Scan(&count); err != nil || count != 0 {
		t.Fatal("todo executed", count, err)
	}
	raw, err = d.Call(ctx, scope, "get_session", manager.DelegationArgs{SessionID: v.SessionID})
	if err != nil || raw.(manager.PlannedTask).Instruction != v.Instruction {
		t.Fatal("missing requirements", raw, err)
	}
	if summary := raw.(manager.PlannedTask).Summary; summary == nil || summary.Status != "planned" || summary.Result != nil {
		t.Fatalf("unstarted todo has an incorrect summary: %+v", summary)
	}
	args := manager.DelegationArgs{SessionID: v.SessionID, ExpectedVersion: 2}
	var wg sync.WaitGroup
	errs := make(chan error, 5)
	for range 5 {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := d.Call(ctx, scope, "start_todo", args); errs <- e }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	if err = db.QueryRow(`SELECT count(*)FROM manager_inputs WHERE session_id=$1`, v.SessionID).Scan(&count); err != nil || count != 1 {
		t.Fatal("start duplicated", count, err)
	}
	agents := &service.AgentService{Store: NewAgentStore(db)}
	employee, err := agents.Get(ctx, scope.WorkspaceID, scope.ActorID, scope.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	newName, newRules := "Renamed after start", "New employee rules"
	if _, err = agents.Update(ctx, scope.WorkspaceID, scope.ActorID, scope.AgentID, service.AgentPatch{Version: employee.Version, Name: &newName, Instruction: &newRules}); err != nil {
		t.Fatal(err)
	}
	store := d.Store.(*repository.Store)
	if err = store.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	var assignment, input, engine string
	var generation int64
	if err = db.QueryRowContext(ctx, `SELECT engine_id,assignment_id,generation,initial_input_id FROM manager_sessions WHERE id=$1`, v.SessionID).Scan(&engine, &assignment, &generation, &input); err != nil {
		t.Fatal(err)
	}
	reviewStartTurn(t, db, scope.WorkspaceID, v.SessionID, input, assignment, generation)
	cfg, _, err := store.HarnessConfig(ctx, engine, v.SessionID, assignment, generation, input)
	if err != nil || !cfg.TodoStarted || cfg.Employee.Name != employee.Name || cfg.Configuration.Instruction != employee.Instruction {
		t.Fatalf("started todo prompt facts lost frozen decision or employee profile: %v", err)
	}
	if _, err = d.Call(ctx, scope, "cancel_todo", manager.DelegationArgs{SessionID: v.SessionID, ExpectedVersion: 3}); err == nil {
		t.Fatal("cancel_todo affected running work")
	}
	raw, err = d.Call(ctx, scope, "create_todo", manager.DelegationArgs{Instruction: "cancel later"})
	if err != nil {
		t.Fatal(err)
	}
	other := raw.(manager.PlannedTask)
	raw, err = d.Call(ctx, scope, "cancel_todo", manager.DelegationArgs{SessionID: other.SessionID, ExpectedVersion: 1})
	if err != nil || raw.(manager.PlannedTask).State != "canceled" {
		t.Fatal(raw, err)
	}
	if _, err = d.Call(ctx, scope, "start_todo", manager.DelegationArgs{SessionID: other.SessionID, ExpectedVersion: 2}); err == nil {
		t.Fatal("canceled todo revived")
	}
	if _, err = db.Exec(`INSERT INTO manager_inputs(id,workspace_id,session_id,submitted_by,seq,text)VALUES(gen_random_uuid(),$1,$2,$3,1,'bypass')`, scope.WorkspaceID, other.SessionID, scope.ActorID); err == nil {
		t.Fatal("SQL bypass")
	}
}

func TestPlannedTaskRacesRollbackAndRevocation(t *testing.T) {
	db, m, d, scope := plannedFixture(t)
	ctx := context.Background()
	for n, pair := range [][2]string{{"start_todo", "cancel_todo"}, {"start_todo", "supplement_todo"}} {
		suffix := string(rune('a' + n))
		raw, e := d.Call(ctx, scope, "create_todo", manager.DelegationArgs{Instruction: "keep requirements " + suffix})
		if e != nil {
			t.Fatal(e)
		}
		v := raw.(manager.PlannedTask)
		errs := make(chan error, 2)
		var wg sync.WaitGroup
		for _, action := range pair {
			wg.Add(1)
			go func(action string) {
				defer wg.Done()
				_, err := d.Call(ctx, scope, action, manager.DelegationArgs{SessionID: v.SessionID, ExpectedVersion: 1, Instruction: "addition"})
				errs <- err
			}(action)
		}
		wg.Wait()
		close(errs)
		wins := 0
		for err := range errs {
			if err == nil {
				wins++
			}
		}
		if wins != 1 {
			t.Fatal("race did not have one winner", wins)
		}
	}
	raw, e := d.Call(ctx, scope, "create_todo", manager.DelegationArgs{Instruction: "retry requirements"})
	if e != nil {
		t.Fatal(e)
	}
	v := raw.(manager.PlannedTask)
	replayed, e := d.Call(ctx, scope, "create_todo", manager.DelegationArgs{Instruction: "retry requirements"})
	if e != nil || !replayed.(manager.PlannedTask).Duplicate || replayed.(manager.PlannedTask).TaskID != v.TaskID {
		t.Fatal("exact retry did not reuse the todo", replayed, e)
	}
	changed, e := d.Call(ctx, scope, "create_todo", manager.DelegationArgs{Instruction: "changed"})
	if e != nil || changed.(manager.PlannedTask).TaskID == v.TaskID {
		t.Fatal("different arguments were collapsed", changed, e)
	}
	if _, e = db.Exec(`UPDATE platform_model_profiles SET enabled=false,is_default=false WHERE workspace_id=$1`, scope.WorkspaceID); e != nil {
		t.Fatal(e)
	}
	if _, e = d.Call(ctx, scope, "start_todo", manager.DelegationArgs{SessionID: v.SessionID, ExpectedVersion: 1}); e == nil {
		t.Fatal("started without model")
	}
	var state string
	var count int
	if e = db.QueryRow(`SELECT planning_state,(SELECT count(*)FROM manager_inputs WHERE session_id=$2)FROM manager_tasks WHERE id=$1`, v.TaskID, v.SessionID).Scan(&state, &count); e != nil || state != "planned" || count != 0 {
		t.Fatal("failed start not atomic", state, count, e)
	}
	if _, e = db.Exec(`UPDATE platform_agents SET usage_config=jsonb_set(usage_config,'{access}','{"mode":"allowlist","group_members_enabled":false,"allow":[],"deny":[]}'::jsonb)WHERE id=$1`, scope.AgentID); e != nil {
		t.Fatal(e)
	}
	if _, e = d.Call(ctx, scope, "create_todo", manager.DelegationArgs{Instruction: "retry requirements"}); e == nil {
		t.Fatal("replay bypassed revocation")
	}
	if _, _, e = m.SubmitInput(ctx, scope.WorkspaceID, scope.ActorID, v.SessionID, "forged", "unauthorized"); e == nil {
		t.Fatal("revoked input")
	}
}

func TestDelegationFiltersAndEvidenceReceipts(t *testing.T) {
	db, _, d, scope := plannedFixture(t)
	ctx := context.Background()
	raw, err := d.Call(ctx, scope, "create_todo", manager.DelegationArgs{Instruction: "plan"})
	if err != nil {
		t.Fatal(err)
	}
	plan := raw.(manager.PlannedTask)
	raw, err = d.Call(ctx, scope, "list_sessions", manager.DelegationArgs{PlanningState: "planned"})
	if err != nil || len(raw.(manager.Page[manager.Session]).Items) != 1 {
		t.Fatal(raw, err)
	}
	_, err = d.Call(ctx, scope, "start_todo", manager.DelegationArgs{SessionID: plan.SessionID, ExpectedVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	args := manager.DelegationArgs{SessionID: plan.SessionID, Instruction: "extra"}
	raw, err = d.Call(ctx, scope, "supplement_task", args)
	if err != nil {
		t.Fatal(err)
	}
	input := raw.(manager.Input)
	if input.Receipt == nil || input.Receipt.Delivery != "pending" || input.Receipt.InputRevision != 2 || input.Receipt.Application != "unconfirmed" {
		t.Fatal(input)
	}
	raw, err = d.Call(ctx, scope, "supplement_task", args)
	if err != nil || !raw.(manager.Input).Receipt.Duplicate {
		t.Fatal("lost receipt dedup", raw, err)
	}
	args.Instruction = "second supplement"
	if next, e := d.Call(ctx, scope, "supplement_task", args); e != nil || next.(manager.Input).Seq != 3 {
		t.Fatal("distinct append missing", next, e)
	}
	for _, state := range []string{"unknown", "committed", "absent"} {
		if _, err = db.Exec(`UPDATE manager_inputs SET context_state=$2 WHERE id=$1`, input.ID, state); err != nil {
			t.Fatal(err)
		}
		raw, err = d.Call(ctx, scope, "get_session", manager.DelegationArgs{SessionID: plan.SessionID, InputID: input.ID})
		if err != nil {
			t.Fatal(err)
		}
		receipt := raw.(map[string]any)["input"].(*manager.Input).Receipt
		expected := map[string]string{"unknown": "unknown", "committed": "received", "absent": "not_received"}[state]
		if receipt.Delivery != expected || receipt.Application != "unconfirmed" {
			t.Fatal("overclaimed receipt", receipt)
		}
	}
	raw, err = d.Call(ctx, scope, "list_sessions", manager.DelegationArgs{PlanningState: "planned"})
	if err != nil || len(raw.(manager.Page[manager.Session]).Items) != 0 {
		t.Fatal(raw, err)
	}
	raw, err = d.Call(ctx, scope, "list_sessions", manager.DelegationArgs{ExecutionState: "queued", PlanningState: "started"})
	if err != nil || len(raw.(manager.Page[manager.Session]).Items) != 1 {
		t.Fatal(raw, err)
	}
}

func TestPlannedTaskMCPProtocolAndRemovedTargetTool(t *testing.T) {
	db, _, d, scope := plannedFixture(t)
	d.Secret = strings.Repeat("s", 40)
	token, err := manager.SignDelegation(d.Secret, scope.Identity)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := delegation.Handler(d, nil)
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, params string) string {
		t.Helper()
		r := httptest.NewRequest("POST", "http://localhost/mcp/task-delegation", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"`+method+`","params":`+params+`}`))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		r.Header.Set("MCP-Protocol-Version", "2025-11-25")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	list := call("tools/list", `{}`)
	if strings.Contains(list, "task_delegation_list_targets") || strings.Contains(list, `"session_id"`) || !strings.Contains(list, "task_delegation_cancel_todo") {
		t.Fatal("wrong tool surface", list)
	}
	result := call("tools/call", `{"name":"task_delegation_create_todo","arguments":{"instruction":"deferred"}}`)
	if strings.Contains(result, `"isError":true`) || !strings.Contains(result, "planned") || strings.Contains(result, `"accepted":true`) {
		t.Fatal(result)
	}
	var task string
	if err = db.QueryRow(`SELECT id FROM manager_tasks WHERE source_chat_session_id=$1`, scope.Identity.SessionID).Scan(&task); err != nil {
		t.Fatal(err)
	}
	result = call("tools/call", `{"name":"task_delegation_start_todo","arguments":{"task_id":"`+task+`","expected_version":"1"}}`)
	if strings.Contains(result, `"isError":true`) || !strings.Contains(result, "started") {
		t.Fatal(result)
	}
}

func TestPlannedTaskSameSourceDifferentActorCannotMutateOrRead(t *testing.T) {
	db, m, d, scope := plannedFixture(t)
	ctx := context.Background()
	raw, err := d.Call(ctx, scope, "create_todo", manager.DelegationArgs{Instruction: "creator-only requirements"})
	if err != nil {
		t.Fatal(err)
	}
	plan := raw.(manager.PlannedTask)
	other := manager.NewID()
	insertWorkspaceAccount(t, db, other, other+"@example.com")
	if _, err = db.Exec(`INSERT INTO platform_members(workspace_id,user_id,role,status)VALUES($1,$2,'member','active')`, scope.WorkspaceID, other); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE manager_inputs SET submitted_by=$2 WHERE id=$1`, scope.InputID, other); err != nil {
		t.Fatal(err)
	}
	var originalTitle *string
	var titleRevision int64
	if err = db.QueryRow(`SELECT title,revision FROM manager_sessions WHERE id=$1`, plan.SessionID).Scan(&originalTitle, &titleRevision); err != nil {
		t.Fatal(err)
	}
	newTitle := "unauthorized rename"
	if _, err = m.RenameSession(ctx, scope.WorkspaceID, other, plan.SessionID, &newTitle, titleRevision); err == nil {
		t.Fatal("other actor renamed private todo through public API")
	}
	if _, err = m.ControlSession(ctx, scope.WorkspaceID, other, plan.SessionID, true); err == nil {
		t.Fatal("other actor archived private todo through public API")
	}
	var unchanged bool
	if err = db.QueryRow(`SELECT title IS NOT DISTINCT FROM $2::text AND revision=$3 AND lifecycle='active' FROM manager_sessions WHERE id=$1`, plan.SessionID, originalTitle, titleRevision).Scan(&unchanged); err != nil || !unchanged {
		t.Fatal("rejected public mutation changed todo", unchanged, err)
	}
	current, err := d.Store.DelegationSource(ctx, scope.Identity)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"get_session", "start_todo", "cancel_todo", "supplement_task"} {
		if _, err = d.Call(ctx, current, op, manager.DelegationArgs{SessionID: plan.SessionID, ExpectedVersion: 1, RequestKey: op, Instruction: "not mine"}); err == nil {
			t.Fatal("other actor accessed planned task", op)
		}
	}
	raw, err = d.Call(ctx, current, "list_sessions", manager.DelegationArgs{PlanningState: "planned"})
	if err != nil || len(raw.(manager.Page[manager.Session]).Items) != 0 {
		t.Fatal("other actor listed private todo", raw, err)
	}
}

func TestArchivedTodoDoesNotAdvertiseStart(t *testing.T) {
	_, m, d, scope := plannedFixture(t)
	ctx := context.Background()
	raw, err := d.Call(ctx, scope, "create_todo", manager.DelegationArgs{Instruction: "archive later"})
	if err != nil {
		t.Fatal(err)
	}
	v := raw.(manager.PlannedTask)
	if _, err = m.ControlSession(ctx, scope.WorkspaceID, scope.ActorID, v.SessionID, true); err != nil {
		t.Fatal(err)
	}
	raw, err = d.Call(ctx, scope, "get_session", manager.DelegationArgs{SessionID: v.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	archived := raw.(manager.PlannedTask)
	if archived.Lifecycle != "archived" || archived.AvailableActions["start"] || archived.AvailableActions["cancel_todo"] || archived.AvailableActions["supplement"] {
		t.Fatal(archived)
	}
	if _, err = d.Call(ctx, scope, "start_todo", manager.DelegationArgs{SessionID: v.SessionID, ExpectedVersion: 1}); err == nil {
		t.Fatal("archived task started")
	}
}
