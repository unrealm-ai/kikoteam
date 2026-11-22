package repository

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func TestTurnSupplementReceiptsUseNativeEvidenceAndSelectedResult(t *testing.T) {
	db, m, d, scope := plannedFixture(t)
	ctx := context.Background()
	w, actor := scope.WorkspaceID, scope.ActorID
	created, err := d.Call(ctx, scope, "create_session", manager.DelegationArgs{Instruction: "initial A"})
	if err != nil {
		t.Fatal(err)
	}
	source := created.(map[string]any)
	task, session, initial := *source["task_id"].(*string), source["session_id"].(string), *source["input_id"].(*string)
	add := func(key, mode string) manager.Input {
		t.Helper()
		input, _, e := m.SubmitInputMode(ctx, w, actor, session, key, "do not include body "+key, mode)
		if e != nil {
			t.Fatal(e)
		}
		return input
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := db.ExecContext(ctx, q, args...); e != nil {
			t.Fatal(e)
		}
	}
	newTurn := func(input string, n int) string {
		t.Helper()
		turn := manager.NewID()
		exec(`INSERT INTO manager_turns(id,workspace_id,session_id,input_id,turn_no,assignment_id,generation,status,result_summary,finished_at)
			VALUES($1,$2,$3,$4,$5,$6,1,'completed','fixture result',now())`, turn, w, session, input, n, scope.Identity.AssignmentID)
		return turn
	}
	joined := func(id, turn, batch, state string) {
		t.Helper()
		exec(`UPDATE manager_inputs SET status='processed',turn_id=$2,batch_input_id=$3,context_state=$4,
			started_at=now(),finished_at=now(),context_committed_at=CASE WHEN $4='committed' THEN now() END WHERE id=$1`, id, turn, batch, state)
	}
	first := newTurn(initial, 1)
	joined(initial, first, initial, "committed")
	history := add("history", "steer")
	b := add("steer-b", "steer")
	uncertain := add("steer-uncertain", "steer")
	c := add("queue-c", "queue")
	exec(`UPDATE manager_inputs SET source_kind='system',trigger_agent_loop=false WHERE id=$1`, history.ID)
	joined(history.ID, first, b.ID, "committed")
	joined(b.ID, first, b.ID, "committed")
	joined(uncertain.ID, first, uncertain.ID, "unknown")
	mcp := func(turn, status string) []manager.SupplementReceipt {
		t.Helper()
		raw, e := d.Call(ctx, scope, "get_session", manager.DelegationArgs{SessionID: session, TurnID: turn})
		if e != nil {
			t.Fatal(e)
		}
		out := raw.(map[string]any)
		summary := out["summary"].(manager.DelegatedTaskSummary)
		selected := out["turn"].(*manager.Turn)
		if summary.Result == nil || summary.Result.TurnID != selected.ID || summary.Result.TurnStatus != "completed" || summary.Result.Preview != "fixture result" {
			t.Fatalf("summary does not describe selected turn: %+v", summary)
		}
		if summary.Status != status {
			t.Fatalf("turn result obscured current status %q: %+v", status, summary)
		}
		v := out["supplement_receipts"].([]manager.SupplementReceipt)
		data, e := json.Marshal(v)
		if e != nil || strings.Contains(string(data), "do not include body") || out["supplement_receipts_truncated"] != false {
			t.Fatalf("bad projected MCP metadata: %s (%v)", data, e)
		}
		return v
	}
	checkFirst := func(v []manager.SupplementReceipt, state string) {
		t.Helper()
		if len(v) != 2 || v[0].InputID != b.ID || v[0].InputSeq != b.Seq || v[0].ContextState == nil || *v[0].ContextState != "committed" ||
			v[1].InputID != uncertain.ID || v[1].InputSeq != uncertain.Seq || v[1].ContextState == nil || *v[1].ContextState != state {
			t.Fatalf("wrong selected-Turn supplements: %+v", v)
		}
	}
	checkFirst(mcp(first, "queued"), "unknown")
	reports := func(turn string) []manager.TaskReportEntry {
		t.Helper()
		page, e := m.ListTaskReports(ctx, w, actor, task, "", manager.ReportFilter{Turn: turn})
		if e != nil {
			t.Fatal(e)
		}
		return page.Items
	}
	r := reports(first)
	if len(r) != 1 || r[0].SourceInputID != initial || r[0].Status != "pending" || r[0].Truncated {
		t.Fatal(r)
	}
	checkFirst(r[0].Receipts, "unknown")
	// Simulate a later recovery of the native receipt. The original report
	// identity/contents remain immutable, but scoped queries see new evidence.
	exec(`UPDATE manager_inputs SET context_state='committed',context_committed_at=now() WHERE id=$1`, uncertain.ID)
	second := newTurn(c.ID, 2)
	joined(c.ID, second, c.ID, "committed")
	exec(`UPDATE manager_inputs SET report_enabled=false WHERE id=$1`, c.ID)
	checkFirst(mcp(first, "idle"), "committed")
	checkFirst(reports(first)[0].Receipts, "committed")
	if v := mcp("", "idle"); len(v) != 0 {
		t.Fatal(v)
	}
	if r = reports(second); len(r) != 1 || r[0].Status != "not_enabled" || r[0].Receipts == nil || len(r[0].Receipts) != 0 {
		t.Fatal(r)
	}
	cancelled := add("cancelled-without-turn", "queue")
	exec(`UPDATE manager_inputs SET status='canceled',finished_at=now() WHERE id=$1`, cancelled.ID)
	r = reports("")
	if len(r) != 3 || r[0].SourceInputID != cancelled.ID || r[0].SourceTurnID != nil || r[0].Receipts == nil || len(r[0].Receipts) != 0 {
		t.Fatal(r)
	}
	// Exercise bounded read and metadata ordering without generating extra
	// outbox work. These represent prior native misses, not applied inputs.
	exec(`INSERT INTO manager_inputs(id,workspace_id,session_id,submitted_by,admission_epoch,seq,text,status,turn_id,batch_input_id,
		delivery_mode,source_kind,trigger_agent_loop,context_state,started_at,finished_at)
		SELECT g.id,a.workspace_id,a.session_id,a.submitted_by,a.admission_epoch,g.seq,'excluded text','processed',$2,g.id,
		'steer','user',true,'absent',now(),now()
		FROM manager_inputs a CROSS JOIN (SELECT gen_random_uuid() id,1000+n seq FROM generate_series(1,105) n) g WHERE a.id=$1`, initial, first)
	r = reports(first)
	if len(r) != 1 || len(r[0].Receipts) != 100 || !r[0].Truncated || r[0].Receipts[0].InputID != b.ID ||
		r[0].Receipts[99].InputSeq != 1098 || r[0].Receipts[99].ContextState == nil || *r[0].Receipts[99].ContextState != "absent" {
		t.Fatalf("incorrect bounded result: %+v", r)
	}
	bad := scope
	bad.Identity.SessionID = manager.NewID()
	if _, err = d.Call(ctx, bad, "get_session", manager.DelegationArgs{SessionID: session, TurnID: first}); err == nil {
		t.Fatal("wrong source chat learned supplement metadata")
	}
	if _, err = d.Call(ctx, scope, "get_session", manager.DelegationArgs{SessionID: session, TurnID: manager.NewID()}); err == nil {
		t.Fatal("foreign Turn accepted")
	}
}
