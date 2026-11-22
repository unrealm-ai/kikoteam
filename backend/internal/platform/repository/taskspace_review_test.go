package repository

import (
	"context"
	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"testing"
)

func TestTaskSpaceIndependentReviewRunningRevokeFence(t *testing.T) {
	db, m, _, w, actor, agent, _ := managerFixture(t)
	ctx := context.Background()
	store := m.Store.(*managerrepo.Store)
	space, _, err := m.CreateTaskSpace(ctx, w, actor, "space", manager.TaskSpaceCreate{AgentID: agent})
	if err != nil {
		t.Fatal(err)
	}
	session := taskSpaceSession(t, m, w, actor, agent, space.ID)
	engine := manager.Engine{ID: manager.NewID(), HostID: "harness-host", Endpoint: "http://localhost:9000", Capacity: 8}
	if err = store.RegisterEngine(ctx, engine); err != nil {
		t.Fatal(err)
	}
	if err = store.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	assignments, err := store.Assignments(ctx, engine.ID)
	if err != nil || len(assignments) != 1 {
		t.Fatal(assignments, err)
	}
	assignment := assignments[0]
	turn := manager.NewID()
	if _, err = db.Exec(`INSERT INTO manager_turns(id,workspace_id,session_id,input_id,turn_no,assignment_id,generation,status) VALUES($1,$2,$3,$4,1,$5,$6,'running')`, turn, w, session.ID, *session.InitialInputID, assignment.AssignmentID, assignment.Generation); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE manager_inputs SET status='started',turn_id=$2 WHERE id=$1`, *session.InitialInputID, turn); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE manager_sessions SET active_turn_id=$2,last_turn_no=1 WHERE id=$1`, session.ID, turn); err != nil {
		t.Fatal(err)
	}
	cfg, who, err := store.HarnessConfig(ctx, engine.ID, session.ID, assignment.AssignmentID, assignment.Generation, *session.InitialInputID)
	if err != nil || who != actor || cfg.TaskSpaceID != space.ID || cfg.TaskID != *session.TaskID || cfg.ConversationID != "" || cfg.WebGroup || cfg.ChannelPrincipalID != nil {
		t.Fatal("owner lost or forged", cfg, who, err)
	}
	if _, _, err = store.HarnessConfig(ctx, engine.ID, session.ID, assignment.AssignmentID, assignment.Generation+1, *session.InitialInputID); err == nil {
		t.Fatal("stale generation accepted")
	}
	if _, err = m.GrantTaskSpaceAgent(ctx, w, actor, space.ID, agent, manager.TaskSpaceGrant{Version: space.Version, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.HarnessConfig(ctx, engine.ID, session.ID, assignment.AssignmentID, assignment.Generation, *session.InitialInputID); err == nil {
		t.Fatal("revoked space grant retained runtime")
	}
	var stop, applied int64
	if err = db.QueryRow(`SELECT stop_revision,applied_stop_revision FROM manager_sessions WHERE id=$1`, session.ID).Scan(&stop, &applied); err != nil || stop <= applied {
		t.Fatal("running revocation lacked pending stop fence", stop, applied, err)
	}
	if err = store.Release(ctx, engine.ID, session.ID, assignment.AssignmentID, assignment.Generation); err == nil {
		t.Fatal("released before running turn settled")
	}
	if _, err = m.ControlSession(ctx, w, actor, session.ID, false); err != nil {
		t.Fatal("owner cannot stop revoked running task", err)
	}
	if _, err = m.ControlSession(ctx, w, actor, session.ID, true); err == nil {
		t.Fatal("archived unresolved running task")
	}
	current, err := m.TaskSpace(ctx, w, actor, space.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.GrantTaskSpaceAgent(ctx, w, actor, space.ID, agent, manager.TaskSpaceGrant{Version: current.Version, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.HarnessConfig(ctx, engine.ID, session.ID, assignment.AssignmentID, assignment.Generation, *session.InitialInputID); err == nil {
		t.Fatal("regrant revived running tool authority")
	}
	if _, _, err = m.SubmitInput(ctx, w, actor, session.ID, "retry-old", "should reject"); err == nil {
		t.Fatal("regrant revived prior task")
	}

}
