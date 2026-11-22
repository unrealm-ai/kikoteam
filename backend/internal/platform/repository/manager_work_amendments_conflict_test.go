package repository

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/mcp/taskdelegation"
	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type amendmentConflictFixture struct {
	t         *testing.T
	db        *sql.DB
	manager   *manager.Service
	store     *managerrepo.Store
	tool      manager.Delegation
	chat      manager.DelegationIdentity
	workspace string
	actor     string
	item      string
	task      string
	turn      string
	first     string
}

func newAmendmentConflictFixture(t *testing.T) amendmentConflictFixture {
	t.Helper()
	db, m, _, w, actor, agent, _ := managerFixture(t)
	ctx := t.Context()
	store := m.Store.(*managerrepo.Store)
	f := amendmentConflictFixture{t: t, db: db, manager: m, store: store, workspace: w, actor: actor}
	f.tool = manager.Delegation{Manager: m, Store: store, Secret: strings.Repeat("t", 40)}
	engine := manager.NewID()
	f.check(store.RegisterEngine(ctx, manager.Engine{ID: engine, HostID: "amendment-fixture", Endpoint: "http://127.0.0.1:1", Capacity: 16}))
	c, e := m.CreateConversation(ctx, w, actor, manager.CreateConversation{AgentID: agent})
	f.check(e)
	req := manager.CreateSession{ConversationID: c.ID, Mode: "chat"}
	req.Input = &struct {
		Text string `json:"text"`
	}{Text: "Add connection pool check"}
	s, _, e := m.CreateSession(ctx, w, actor, "amendment-start", req)
	f.check(e)
	f.first = *s.InitialInputID
	f.chat, f.turn = f.start(s.ID, f.first, engine)

	taskOut, e := f.call("create_session", manager.DelegationArgs{Instruction: "prepare checklist"})
	f.check(e)
	root := taskOut["session_id"].(string)
	f.task = taskOut["task_id"].(string)
	var initial, profile string
	f.check(db.QueryRow(`SELECT s.initial_input_id,a.model_profile_id FROM manager_sessions s JOIN platform_agents a ON a.id=s.agent_id WHERE s.id=$1`, root).Scan(&initial, &profile))
	b, e := (&service.AgentService{Store: NewAgentStore(db)}).Create(ctx, w, actor, service.AgentCreate{Name: "Checklist reviewer", ModelProfileID: profile})
	f.check(e)
	owner, rootTurn := f.start(root, initial, engine)
	out, e := store.WorkTool(ctx, manager.WorkScope{Identity: owner, InputID: initial}, "handoff", manager.WorkArguments{
		OperationID: "amendment-test-handoff", TargetEmployeeID: b.ID, Title: "Checklist", Goal: "Prelaunch checklist",
		Criteria: "Check free disk space", Summary: "Prepare a checklist",
	})
	f.check(e)
	receipt, e := json.Marshal(out)
	f.check(e)
	var boundary struct {
		ID string `json:"id"`
	}
	f.check(json.Unmarshal(receipt, &boundary))
	if boundary.ID == "" {
		t.Fatal("missing handoff boundary", string(receipt))
	}
	reviewFinishTurn(t, db, root, initial, rootTurn)
	reviewExec(t, db, `UPDATE manager_work_boundaries SET state='safe',fenced_at=now() WHERE id=$1`, boundary.ID)
	f.check(store.ReconcileWorkItems(ctx))
	f.check(db.QueryRow(`SELECT id FROM manager_work_items WHERE root_session_id=$1 AND status='working'`, root).Scan(&f.item))
	return f
}

func (f amendmentConflictFixture) check(e error) {
	f.t.Helper()
	if e != nil {
		f.t.Fatal(e)
	}
}

func (f amendmentConflictFixture) start(session, input, engine string) (manager.DelegationIdentity, string) {
	f.t.Helper()
	id := manager.DelegationIdentity{SessionID: session, EngineID: engine, AssignmentID: manager.NewID(), Generation: 1}
	reviewExec(f.t, f.db, `UPDATE manager_sessions SET engine_id=$2,assignment_id=$3,generation=1 WHERE id=$1`, session, engine, id.AssignmentID)
	return id, reviewStartTurn(f.t, f.db, f.workspace, session, input, id.AssignmentID, id.Generation)
}

func (f amendmentConflictFixture) call(name string, args manager.DelegationArgs) (map[string]any, error) {
	f.t.Helper()
	ctx := f.t.Context()
	scope, err := f.store.DelegationSource(ctx, f.chat)
	if err != nil {
		return nil, err
	}
	v, err := f.tool.Call(ctx, scope, name, args)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(v)
	f.check(err)
	var output map[string]any
	f.check(json.Unmarshal(data, &output))
	return output, nil
}

func (f amendmentConflictFixture) mcpCall(name string, args any) map[string]any {
	f.t.Helper()
	h, err := taskdelegation.Handler(&f.tool, nil)
	f.check(err)
	token, err := manager.SignDelegation(f.tool.Secret, f.chat)
	f.check(err)
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": args}})
	f.check(err)
	req := httptest.NewRequest(http.MethodPost, "/mcp/task-delegation", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var result struct {
		Result struct {
			Content map[string]any `json:"structuredContent"`
			IsError bool           `json:"isError"`
		}
	}
	f.check(json.Unmarshal(rec.Body.Bytes(), &result))
	if rec.Code != http.StatusOK || result.Result.IsError || result.Result.Content == nil {
		f.t.Fatalf("unexpected MCP reply: %d %s", rec.Code, rec.Body.String())
	}
	return result.Result.Content
}

func (f amendmentConflictFixture) next(key, text string) string {
	f.t.Helper()
	input, _, err := f.manager.SubmitInput(f.t.Context(), f.workspace, f.actor, f.chat.SessionID, key, text)
	f.check(err)
	reviewExec(f.t, f.db, `UPDATE manager_inputs SET status='started',turn_id=$2,context_state='committed' WHERE id=$1`, input.ID, f.turn)
	return input.ID
}

func (f amendmentConflictFixture) basis(key, text string) manager.DelegationArgs {
	f.t.Helper()
	out, err := f.call("work_item_get_current", manager.DelegationArgs{TaskID: f.task})
	f.check(err)
	item := out["item"].(map[string]any)
	version, err := strconv.ParseInt(item["requirements_version"].(string), 10, 64)
	f.check(err)
	return manager.DelegationArgs{
		WorkItemID: f.item, AmendmentBasis: out["amendment_basis"].(string), RequirementsVersion: version,
		RequestKey: key, Content: text,
	}
}

func (f amendmentConflictFixture) assertApplied(version int, source string, args manager.DelegationArgs) string {
	f.t.Helper()
	out, err := f.call("work_item_amend_requirements", args)
	f.check(err)
	var gotSource, gotInput, content string
	f.check(f.db.QueryRow(`SELECT source_input_id,input_id,content FROM manager_work_amendments WHERE work_item_id=$1 AND requirements_version=$2`, f.item, version).Scan(&gotSource, &gotInput, &content))
	if source != gotSource || out["input_id"] != gotInput || content != args.Content || out["delivery"] != "queued" {
		f.t.Fatalf("wrong amendment receipt or source: %v; source=%s, want %s", out, gotSource, source)
	}
	return gotInput
}

func TestWorkAmendmentBindsOriginalInputAndRebasesOnce(t *testing.T) {
	f := newAmendmentConflictFixture(t)
	a := f.basis("pool", "Add connection pool check")
	bSource := f.next("slow", "Also check slow queries")
	b := f.basis("slow", "Also check slow queries")
	f.next("progress", "How's it going?")
	f.assertApplied(2, f.first, a)

	conflict := f.mcpCall("work_item_amend_requirements", b)
	if conflict["status"] != "version_conflict" || conflict["applied"] != false ||
		conflict["requirements_version"] != "2" || conflict["original_message"] != b.Content ||
		!strings.Contains(conflict["item"].(map[string]any)["criteria"].(string), a.Content) {
		t.Fatal("conflict lacked original input and latest standards", conflict)
	}
	b.RequirementsVersion, b.AmendmentBasis = 2, conflict["amendment_basis"].(string)
	changed := b
	changed.Content = "different amendment"
	_, err := f.call("work_item_amend_requirements", changed)
	assertChannelError(t, err, "work_amendment_basis_invalid")
	initial := f.assertApplied(3, bSource, b)
	if replay := f.assertApplied(3, bSource, b); replay != initial {
		t.Fatal("retry queued another amendment")
	}

	f.next("conflicting", "Drop the disk check")
	c := f.basis("remove-disk", "Drop the disk check")
	winner := f.next("winner", "Keep the disk check")
	d := f.basis("keep-disk", "Keep the disk check")
	f.assertApplied(4, winner, d)
	out, err := f.call("work_item_amend_requirements", c)
	f.check(err)
	if out["applied"] != false || !strings.Contains(out["item"].(map[string]any)["criteria"].(string), d.Content) {
		t.Fatal("conflicting request was silently applied", out)
	}
	c.AmendmentBasis, c.RequirementsVersion = out["amendment_basis"].(string), 4
	lastSource := f.next("another", "Check replication lag")
	last := f.basis("replication", "Check replication lag")
	f.assertApplied(5, lastSource, last)
	_, err = f.call("work_item_amend_requirements", c)
	assertChannelError(t, err, "work_version_changed")
	var count int
	f.check(f.db.QueryRow(`SELECT count(*) FROM manager_work_amendments WHERE work_item_id=$1`, f.item).Scan(&count))
	if count != 4 {
		t.Fatal("conflict or replay changed amendment count", count)
	}
}

func TestWorkAmendmentRechecksOriginalScopeAndClosure(t *testing.T) {
	f := newAmendmentConflictFixture(t)
	a := f.basis("pool", "Add connection pool check")
	reviewExec(t, f.db, `UPDATE manager_work_items SET status='completed' WHERE id=$1`, f.item)
	_, err := f.call("work_item_amend_requirements", a)
	assertChannelError(t, err, "work_amendment_closed")
	reviewExec(t, f.db, `UPDATE manager_work_items SET status='working' WHERE id=$1`, f.item)
	reviewFinishTurn(t, f.db, f.chat.SessionID, f.first, f.turn)
	next, _, err := f.manager.SubmitInput(t.Context(), f.workspace, f.actor, f.chat.SessionID, "new-turn", "How's it going?")
	f.check(err)
	reviewStartTurn(t, f.db, f.workspace, f.chat.SessionID, next.ID, f.chat.AssignmentID, f.chat.Generation)
	_, err = f.call("work_item_amend_requirements", a)
	assertChannelError(t, err, "work_amendment_source_unavailable")
	var n int
	f.check(f.db.QueryRow(`SELECT count(*) FROM manager_work_amendments WHERE work_item_id=$1`, f.item).Scan(&n))
	if n != 0 {
		t.Fatal("closed or old source was applied", n)
	}
}
