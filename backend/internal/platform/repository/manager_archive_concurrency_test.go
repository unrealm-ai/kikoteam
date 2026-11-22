package repository

import (
	"context"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"testing"
	"time"
)

// Exercise the Engine session-lock/event-write order while the real Manager
// archive use case is demonstrably blocked behind that lock. This does not
// simulate all Engine work or reproduce every possible interleaving.
func TestArchiveWaitsForSessionEventTransaction(t *testing.T) {
	db, s, _, w, actor, agent, _ := managerFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c, err := s.CreateConversation(ctx, w, actor, manager.CreateConversation{AgentID: agent})
	if err != nil {
		t.Fatal(err)
	}
	session, _, err := s.CreateSession(ctx, w, actor, "", manager.CreateSession{ConversationID: c.ID, Mode: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Preserve the test failure; rollback after commit is harmless.
	var pid int
	if err = tx.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	var id string
	if err = tx.QueryRowContext(ctx, `SELECT id FROM manager_sessions WHERE id=$1 FOR UPDATE`, session.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, e := s.ArchiveConversation(ctx, w, actor, c.ID); done <- e }()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var blocked bool
		if err = db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, pid).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case e := <-done:
			t.Fatalf("archive did not wait for held session: %v", e)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
	var seq int64
	if err = tx.QueryRowContext(ctx, `UPDATE manager_sessions SET last_event_seq=last_event_seq+1 WHERE id=$1 RETURNING last_event_seq`, session.ID).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO manager_session_events(workspace_id,session_id,seq,event) VALUES($1,$2,$3,'{"type":"session.updated"}')`, w, session.ID, seq); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	got, err := s.Store.Conversation(ctx, w, actor, c.ID)
	if err != nil || got.Lifecycle != "archived" {
		t.Fatalf("archive result=%+v err=%v", got, err)
	}
}

// Match Claim's parent SHARE -> child UPDATE ordering, and verify archive
// observes a newly started turn after waiting for that transaction to commit.
func TestArchiveRacesWithClaim(t *testing.T) {
	db, s, _, w, actor, agent, _ := managerFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c, err := s.CreateConversation(ctx, w, actor, manager.CreateConversation{AgentID: agent})
	if err != nil {
		t.Fatal(err)
	}
	session, _, err := s.CreateSession(ctx, w, actor, "", manager.CreateSession{ConversationID: c.ID, Mode: "task"})
	if err != nil {
		t.Fatal(err)
	}
	input, _, err := s.SubmitInput(ctx, w, actor, session.ID, "claim-input", "start")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Preserve test error.
	var pid int
	var lifecycle string
	if err = tx.QueryRowContext(ctx, `SELECT pg_backend_pid(),lifecycle FROM manager_conversations WHERE id=$1 FOR SHARE`, c.ID).Scan(&pid, &lifecycle); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, e := s.ArchiveConversation(ctx, w, actor, c.ID); done <- e }()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var blocked bool
		if err = db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, pid).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case e := <-done:
			t.Fatalf("archive bypassed claim: %v", e)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO manager_turns(id,workspace_id,session_id,input_id,turn_no,assignment_id,generation,status) VALUES('00000000-0000-4000-8000-000000000001',$1,$2,$3,1,'00000000-0000-4000-8000-000000000002',1,'running')`, w, session.ID, input.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE manager_inputs SET status='started',started_at=now() WHERE id=$1`, input.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE manager_sessions SET active_turn_id='00000000-0000-4000-8000-000000000001' WHERE id=$1`, session.ID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	assertArchiveConflict(t, <-done, "conversation_busy")
	got, err := s.Store.Conversation(ctx, w, actor, c.ID)
	if err != nil || got.Lifecycle != "active" {
		t.Fatalf("parent=%+v err=%v", got, err)
	}
	// Reverse ordering: once archive commits, Claim's parent predicate denies a start.
	if _, err = db.ExecContext(ctx, `UPDATE manager_sessions SET active_turn_id=NULL WHERE id=$1`, session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ArchiveConversation(ctx, w, actor, c.ID); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `SELECT lifecycle FROM manager_conversations WHERE id=$1 FOR SHARE`, c.ID).Scan(&lifecycle); err != nil || lifecycle != "archived" {
		t.Fatalf("claim parent=%s err=%v", lifecycle, err)
	}
	if _, _, err = s.SubmitInput(ctx, w, actor, session.ID, "late-input", "late"); err == nil {
		t.Fatal("archived conversation admitted input")
	}
}
