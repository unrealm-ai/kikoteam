package repository

import (
	"context"
	"database/sql"
	"errors"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"testing"
)

func TestArchiveRejectsBusyConversationAndSession(t *testing.T) {
	for _, mode := range []string{"chat", "task"} {
		for _, state := range []struct{ name, update string }{
			{"running", "active_turn_id='00000000-0000-4000-8000-000000000001'"},
			{"background", "background_process_count=1"},
			{"unknown", "background_process_count=NULL"},
			{"blocked", "block_reason='sandbox_activity_unknown'"},
			{"stopping", "stop_revision=1"},
			{"queued", ""},
		} {
			t.Run(mode+"/"+state.name, func(t *testing.T) {
				db, s, _, w, actor, agent, _ := managerFixture(t)
				ctx := context.Background()
				c, err := s.CreateConversation(ctx, w, actor, manager.CreateConversation{AgentID: agent})
				if err != nil {
					t.Fatal(err)
				}
				if mode == "task" {
					if _, _, err = s.CreateSession(ctx, w, actor, "", manager.CreateSession{ConversationID: c.ID, Mode: "chat"}); err != nil {
						t.Fatal(err)
					}
				}
				session, _, err := s.CreateSession(ctx, w, actor, "", manager.CreateSession{ConversationID: c.ID, Mode: mode})
				if err != nil {
					t.Fatal(err)
				}
				if state.name == "running" {
					input, _, e := s.SubmitInput(ctx, w, actor, session.ID, "running-input", "start")
					if e != nil {
						t.Fatal(e)
					}
					_, e = db.ExecContext(ctx, `INSERT INTO manager_turns(id,workspace_id,session_id,input_id,turn_no,assignment_id,generation,status) VALUES('00000000-0000-4000-8000-000000000001',$1,$2,$3,1,'00000000-0000-4000-8000-000000000002',1,'running')`, w, session.ID, input.ID)
					if e != nil {
						t.Fatal(e)
					}
				}
				if state.name == "running" {
					if _, err = db.ExecContext(ctx, `UPDATE manager_inputs SET status='started',started_at=now(),turn_id='00000000-0000-4000-8000-000000000001' WHERE session_id=$1`, session.ID); err != nil {
						t.Fatal(err)
					}
				}
				if state.update != "" {
					_, err = db.ExecContext(ctx, "UPDATE manager_sessions SET "+state.update+" WHERE id=$1", session.ID)
				} else {
					_, _, err = s.SubmitInput(ctx, w, actor, session.ID, "queued-test", "pending")
				}
				if err != nil {
					t.Fatal(err)
				}
				_, err = s.ArchiveConversation(ctx, w, actor, c.ID)
				assertArchiveConflict(t, err, "conversation_busy")
				_, err = s.ControlSession(ctx, w, actor, session.ID, true)
				assertArchiveConflict(t, err, "session_busy")
				var lifecycle string
				var revision, stop, seq int64
				if err = db.QueryRowContext(ctx, `SELECT lifecycle,revision,stop_revision,last_event_seq FROM manager_sessions WHERE id=$1`, session.ID).Scan(&lifecycle, &revision, &stop, &seq); err != nil {
					t.Fatal(err)
				}
				expectedStop := int64(0)
				if state.name == "stopping" {
					expectedStop = 1
				}
				if lifecycle != "active" || revision != 1 || stop != expectedStop {
					t.Fatalf("rejected archive mutated session: %s %d %d", lifecycle, revision, stop)
				}
				got, err := s.Store.Conversation(ctx, w, actor, c.ID)
				if err != nil || got.Lifecycle != "active" {
					t.Fatalf("parent=%+v err=%v", got, err)
				}
				if _, err = db.ExecContext(ctx, `UPDATE manager_sessions SET active_turn_id=NULL,background_process_count=0,block_reason=NULL,applied_stop_revision=stop_revision WHERE id=$1`, session.ID); err != nil {
					t.Fatal(err)
				}
				if _, err = s.ControlSession(ctx, w, actor, session.ID, false); err != nil {
					t.Fatal(err)
				}
				if _, err = s.ArchiveConversation(ctx, w, actor, c.ID); err != nil {
					t.Fatal(err)
				}
				if _, err = s.ArchiveConversation(ctx, w, actor, c.ID); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
func assertArchiveConflict(t *testing.T, err error, code string) {
	t.Helper()
	var e *manager.Error
	if !errors.As(err, &e) || e.Code != code || e.Status != 409 {
		t.Fatalf("want %s/409 got %v", code, err)
	}
}

// Wait for real Engine evidence, rather than assuming a completed Turn means
// stop acknowledgement, background work and remote calls are all settled.
func waitConversationQuiescent(t *testing.T, ctx context.Context, db *sql.DB, id string, wait func(func() bool)) {
	t.Helper()
	wait(func() bool {
		var busy bool
		err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM manager_sessions s WHERE conversation_id=$1 AND (active_turn_id IS NOT NULL OR COALESCE(background_process_count,1)<>0 OR (block_reason IS NOT NULL AND block_reason<>'remote_tool_outcome_unknown') OR stop_revision>applied_stop_revision OR EXISTS(SELECT 1 FROM manager_inputs i WHERE i.session_id=s.id AND i.status='queued')))`, id).Scan(&busy)
		if err != nil {
			t.Fatal(err)
		}
		return !busy
	})
}

func TestArchivePreservesUnknownRemoteOutcome(t *testing.T) {
	db, s, _, w, actor, agent, _ := managerFixture(t)
	ctx := context.Background()
	c, err := s.CreateConversation(ctx, w, actor, manager.CreateConversation{AgentID: agent})
	if err != nil {
		t.Fatal(err)
	}
	v, _, err := s.CreateSession(ctx, w, actor, "", manager.CreateSession{ConversationID: c.ID, Mode: "task"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE manager_sessions SET block_reason='remote_tool_outcome_unknown' WHERE id=$1`, v.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ArchiveConversation(ctx, w, actor, c.ID); err != nil {
		t.Fatal(err)
	}
	parent, err := s.Store.Conversation(ctx, w, actor, c.ID)
	if err != nil || !parent.CleanupPending {
		t.Fatalf("unknown outcome falsely reported cleaned: %+v %v", parent, err)
	}
	got, err := s.Store.Session(ctx, w, actor, v.ID)
	if err != nil || got.Lifecycle != "archived" || got.Execution.BlockReason == nil || *got.Execution.BlockReason != "remote_tool_outcome_unknown" {
		t.Fatalf("lost unknown evidence %+v %v", got, err)
	}
}

func TestArchiveContinuesPreviouslyAcceptedParent(t *testing.T) {
	db, s, _, w, actor, agent, _ := managerFixture(t)
	ctx := context.Background()
	c, err := s.CreateConversation(ctx, w, actor, manager.CreateConversation{AgentID: agent})
	if err != nil {
		t.Fatal(err)
	}
	v, _, err := s.CreateSession(ctx, w, actor, "", manager.CreateSession{ConversationID: c.ID, Mode: "task"})
	if err != nil {
		t.Fatal(err)
	}
	// An older version accepted parent archive before child cleanup completed.
	if _, err = db.ExecContext(ctx, `UPDATE manager_conversations SET lifecycle='archived',archived_at=now() WHERE id=$1`, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE manager_sessions SET background_process_count=1 WHERE id=$1`, v.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ArchiveConversation(ctx, w, actor, c.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.Store.Session(ctx, w, actor, v.ID)
	if err != nil || got.Lifecycle != "archived" || got.Execution.BackgroundProcessCount == nil || *got.Execution.BackgroundProcessCount != 1 {
		t.Fatalf("legacy cleanup lost activity: %+v %v", got, err)
	}
}
