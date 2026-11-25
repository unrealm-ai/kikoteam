package repository

import (
	"context"
	"strings"
	"testing"
)

func TestHumanRevisionPreservesGateAfterCorrection(t *testing.T) {
	t.Run("legacy backend correction keeps approval gate", func(t *testing.T) { testHumanContinuation(t, "revise", false) })
	t.Run("approval resumes with frozen scope", func(t *testing.T) { testHumanContinuation(t, "approve", false) })
	t.Run("changed scope prevents resume", func(t *testing.T) { testHumanContinuation(t, "approve", true) })
}

func testHumanContinuation(t *testing.T, decision string, changeScope bool) {
	t.Helper()
	db, store, work := triggerFixture(t)
	ctx := context.Background()
	original, queued, resume, request, originalTurn := fixtureID(40), fixtureID(41), fixtureID(42), fixtureID(43), fixtureID(44)
	triggerInsert(t, db, original, 1, true, "user")
	triggerInsert(t, db, queued, 2, true, "user")
	claimExec(t, db, `INSERT INTO manager_turns(id,workspace_id,session_id,input_id,turn_no,assignment_id,generation,status,finished_at)
 VALUES($1,$2,$3,$4,1,$5,1,'completed',now())`, originalTurn, work.WorkspaceID, work.SessionID, original, work.AssignmentID)
	claimExec(t, db, `UPDATE manager_inputs SET status='processed',turn_id=$2 WHERE id=$1`, original, originalTurn)
	claimExec(t, db, `INSERT INTO manager_human_requests(id,workspace_id,agent_id,session_id,turn_id,input_id,authority_input_id,recipient,rules_version,question,stop_revision,fenced_at,state)
 VALUES($1,$2,$3,$4,$5,$6,$6,jsonb_build_object('provider','web','kind','user','workspace_id',$2::uuid::text,'agent_id',$3::uuid::text,'external_id',$7::text),
 1,'{"question":"确认发布？","proposedAction":"发布 A","impactScope":"仅测试环境","completionCriteria":["只发布 A"]}',0,now(),'pending')`,
		request, work.WorkspaceID, work.AgentID, work.SessionID, originalTurn, original, fixtureID(2))
	claimExec(t, db, `INSERT INTO manager_inputs(id,workspace_id,session_id,submitted_by,admission_epoch,seq,text,source_kind,delivery_mode,human_request_id)
 VALUES($1,$2,$3,$4,1,3,'修订','human_confirmation','queue',$5)`, resume, work.WorkspaceID, work.SessionID, fixtureID(2), request)
	state := "approved"
	if decision == "revise" {
		state = "revised"
	}
	claimExec(t, db, `UPDATE manager_human_requests SET state=$3,answer=jsonb_build_object('decision',$4::text,'text','发布计划'),
 answered_at=now(),resume_input_id=$2 WHERE id=$1`, request, resume, state, decision)
	claimExec(t, db, `UPDATE manager_sessions SET last_turn_no=1,required_actions=jsonb_build_array(jsonb_build_object('type','human_confirmation','request_id',$2::text,'turn_id',$3::text,'message','等待确认')) WHERE id=$1`, work.SessionID, request, originalTurn)
	claimExec(t, db, `UPDATE manager_human_requests r SET continuation_context=manager_human_continuation_context(r,s,
 CASE WHEN $2::text='approved' THEN r.answer END) FROM manager_sessions s WHERE r.id=$1 AND s.id=r.session_id`, request, state)
	if changeScope {
		claimExec(t, db, `UPDATE manager_sessions SET configuration=configuration||'{"changed":true}' WHERE id=$1`, work.SessionID)
		turn, err := store.Claim(ctx, work, fixtureID(45))
		if err != nil || turn != nil {
			t.Fatalf("changed scope started a turn: %+v (%v)", turn, err)
		}
		var reason, message string
		if err = db.QueryRow(`SELECT i.rejection_reason,s.required_actions->0->>'message'
 FROM manager_inputs i JOIN manager_sessions s ON s.id=i.session_id WHERE i.id=$1 AND i.status='rejected'`, resume).
			Scan(&reason, &message); err != nil || reason != "human_task_scope_changed" || !strings.Contains(message, "重新发起确认") {
			t.Fatal(reason, message, err)
		}
		return
	}
	turn, err := store.Claim(ctx, work, fixtureID(45))
	if err != nil || turn == nil || turn.Input.ID != resume || turn.Input.CorrectionOnly != (decision == "revise") {
		t.Fatalf("expected the corresponding continuation turn, got %+v: %v", turn, err)
	}
	if decision == "approve" {
		for _, field := range []string{`"decision": "approve"`, `"version": "1"`, `"questionDigest"`, "发布 A", "仅测试环境", "只发布 A", `"text": "发布计划"`} {
			if !strings.Contains(turn.Input.Text, field) {
				t.Fatalf("resume prompt omitted %q: %s", field, turn.Input.Text)
			}
		}
		return
	}
	if err = store.Complete(ctx, work, *turn, "completed", "", "只输出草案"); err != nil {
		t.Fatal(err)
	}
	turn, err = store.Claim(ctx, work, fixtureID(46))
	if err != nil || turn != nil {
		t.Fatalf("unapproved queue started after correction: %+v (%v)", turn, err)
	}
	var status, message string
	if err = db.QueryRow(`SELECT i.status,s.required_actions->0->>'message' FROM manager_inputs i JOIN manager_sessions s ON s.id=i.session_id WHERE i.id=$1`,
		queued).Scan(&status, &message); err != nil || status != "queued" || !strings.Contains(message, "未获批准") {
		t.Fatal(status, message, err)
	}
}
