package repository

import (
	"context"
	"github.com/jackc/pgx/v5/pgconn"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"testing"
)

// A sequence is deliberately not rolled back. The trigger injects a real PG
// 40P01 once, after admission writes, to test rollback/replay rather than claim
// to reproduce the original intermittent lock cycle.
func TestManagerDeadlockRetryPreservesSingleAdmission(t *testing.T) {
	db, s, _, w, actor, agent, _ := managerFixture(t)
	ctx := context.Background()
	c, err := s.CreateConversation(ctx, w, actor, manager.CreateConversation{AgentID: agent})
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `CREATE SEQUENCE retry_attempt;
 CREATE FUNCTION retry_deadlock() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN IF nextval('retry_attempt')=1 THEN RAISE EXCEPTION 'test deadlock' USING ERRCODE='40P01'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER retry_deadlock BEFORE UPDATE OF last_event_seq ON manager_sessions FOR EACH ROW EXECUTE FUNCTION retry_deadlock();`)
	if err != nil {
		t.Fatal(err)
	}
	req := manager.CreateSession{ConversationID: c.ID, Mode: "task"}
	req.Input = &struct {
		Text string `json:"text"`
	}{Text: "first"}
	session, replayed, err := s.CreateSession(ctx, w, actor, "retry-create", req)
	if err != nil || replayed {
		t.Fatalf("create=%+v replay=%v err=%v", session, replayed, err)
	}
	if _, err = db.ExecContext(ctx, `ALTER SEQUENCE retry_attempt RESTART WITH 1`); err != nil {
		t.Fatal(err)
	}
	input, replayed, err := s.SubmitInput(ctx, w, actor, session.ID, "retry-input", "second")
	if err != nil || replayed || input.Seq != 2 {
		t.Fatalf("input=%+v replay=%v err=%v", input, replayed, err)
	}
	var sessions, tasks, inputs, dedups, events int
	var seq int64
	err = db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM manager_sessions),(SELECT count(*) FROM manager_tasks),(SELECT count(*) FROM manager_inputs),(SELECT count(*) FROM manager_input_dedup),(SELECT count(*) FROM manager_session_events),last_event_seq FROM manager_sessions WHERE id=$1`, session.ID).Scan(&sessions, &tasks, &inputs, &dedups, &events, &seq)
	if err != nil || sessions != 1 || tasks != 1 || inputs != 2 || dedups != 2 || events != 6 || seq != 6 {
		t.Fatalf("counts=%d/%d/%d/%d/%d seq=%d err=%v", sessions, tasks, inputs, dedups, events, seq, err)
	}
	if _, err = db.ExecContext(ctx, `CREATE OR REPLACE FUNCTION retry_deadlock() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM nextval('retry_attempt'); RAISE EXCEPTION 'test deadlock' USING ERRCODE='40P01'; END $$; ALTER SEQUENCE retry_attempt RESTART WITH 1`); err != nil {
		t.Fatal(err)
	}
	input, replayed, err = s.SubmitInput(ctx, w, actor, session.ID, "retry-fail", "must rollback")
	if err == nil || input.ID != "" || replayed {
		t.Fatalf("failed output=%+v replay=%v err=%v", input, replayed, err)
	}
	var attempts int
	if err = db.QueryRowContext(ctx, `SELECT last_value FROM retry_attempt`).Scan(&attempts); err != nil || attempts != 3 {
		t.Fatalf("attempts=%d err=%v", attempts, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM manager_inputs`).Scan(&inputs); err != nil || inputs != 2 {
		t.Fatalf("rolled back inputs=%d err=%v", inputs, err)
	}
}

// The external revocation is intentionally outside the failed transaction:
// retries must reacquire current membership rather than reuse prior authority.
func TestManagerDeadlockRetryRechecksMembership(t *testing.T) {
	db, s, _, w, actor, agent, _ := managerFixture(t)
	ctx := context.Background()
	c, err := s.CreateConversation(ctx, w, actor, manager.CreateConversation{AgentID: agent})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	err = s.Store.WithSession(ctx, manager.Target{Workspace: w, Actor: actor, Conversation: c.ID}, func(manager.SessionTx) error {
		calls++
		if _, e := db.ExecContext(ctx, `DELETE FROM platform_members WHERE workspace_id=$1 AND user_id=$2`, w, actor); e != nil {
			return e
		}
		return &pgconn.PgError{Code: "40P01"}
	})
	if err == nil || calls != 1 {
		t.Fatalf("callback calls=%d err=%v", calls, err)
	}
	if _, err = s.Store.Conversation(ctx, w, actor, c.ID); err == nil {
		t.Fatal("revoked membership retained access")
	}
}
