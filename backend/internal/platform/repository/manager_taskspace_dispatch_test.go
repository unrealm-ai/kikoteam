package repository

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"

	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func taskSpaceSession(t *testing.T, m *manager.Service, w, actor, agent, space string) manager.Session {
	t.Helper()
	req := manager.CreateSession{TaskSpaceID: space, AgentID: agent, Mode: "task"}
	req.Input = &struct {
		Text string `json:"text"`
	}{Text: "standalone work"}
	result, _, err := m.CreateSession(context.Background(), w, actor, manager.NewID(), req)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func TestTaskSpaceSandboxOwnershipAndReclaim(t *testing.T) {
	db, m, _, w, actor, agent, _ := managerFixture(t)
	ctx := context.Background()
	store := m.Store.(*managerrepo.Store)
	space, _, err := m.CreateTaskSpace(ctx, w, actor, "space", manager.TaskSpaceCreate{AgentID: agent})
	if err != nil {
		t.Fatal(err)
	}
	one := taskSpaceSession(t, m, w, actor, agent, space.ID)
	two := taskSpaceSession(t, m, w, actor, agent, space.ID)
	engine := manager.Engine{ID: manager.NewID(), HostID: "taskspace-host", Endpoint: "http://localhost:9000", Capacity: 8}
	if err = store.RegisterEngine(ctx, engine); err != nil {
		t.Fatal(err)
	}
	if err = store.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	boxes, err := store.Sandboxes(ctx, engine.ID)
	if err != nil || len(boxes) != 2 {
		t.Fatal(len(boxes), err)
	}
	for _, box := range boxes {
		if box.ConversationID != "" || box.TaskSpaceID != space.ID || len(box.Sessions) != 1 || box.TaskID == "" || box.TaskID != box.Sessions[0].TaskID || box.Sessions[0].TaskSpaceID != space.ID {
			t.Fatalf("invalid task owner: %+v", box)
		}
	}
	var conversations int
	if err = db.QueryRow(`SELECT count(*) FROM manager_conversations`).Scan(&conversations); err != nil || conversations != 0 {
		t.Fatal("hidden conversation", conversations, err)
	}
	if _, err = db.Exec(`UPDATE manager_sessions SET lifecycle='archived',archived_at=now(),engine_id=NULL,assignment_id=NULL WHERE id=$1`, one.ID); err != nil {
		t.Fatal(err)
	}
	boxes, err = store.Sandboxes(ctx, engine.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, box := range boxes {
		expected := box.TaskID == *one.TaskID
		if box.ReclaimAllowed != expected {
			t.Fatal("sibling affected reclaim", box)
		}
		if box.TaskID == *two.TaskID && box.Sessions[0].EngineID == nil {
			t.Fatal("sibling lost assignment")
		}
	}
}
func TestTaskSpaceStoppedSandboxRestartIsOwnerScoped(t *testing.T) {
	db, m, _, w, actor, agent, _ := managerFixture(t)
	ctx := context.Background()
	store := m.Store.(*managerrepo.Store)
	space, _, err := m.CreateTaskSpace(ctx, w, actor, "space", manager.TaskSpaceCreate{AgentID: agent})
	if err != nil {
		t.Fatal(err)
	}
	first := taskSpaceSession(t, m, w, actor, agent, space.ID)
	_ = taskSpaceSession(t, m, w, actor, agent, space.ID)
	old := manager.Engine{ID: manager.NewID(), HostID: "restart-host", Endpoint: "http://localhost:9000", Capacity: 8}
	if err = store.RegisterEngine(ctx, old); err != nil {
		t.Fatal(err)
	}
	if err = store.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	boxes, err := store.Sandboxes(ctx, old.ID)
	if err != nil {
		t.Fatal(err)
	}
	var stopped string
	for _, box := range boxes {
		if box.TaskID == *first.TaskID {
			stopped = box.ID
		}
	}
	if err = store.SandboxState(ctx, old.ID, stopped, "stopped"); err == nil {
		t.Fatal("stopped with active assignment")
	}
	if _, err = db.Exec(`UPDATE manager_sessions SET engine_id=NULL,assignment_id=NULL WHERE id=$1`, first.ID); err != nil {
		t.Fatal(err)
	}
	// Mirror a pre-upgrade handoff: all executions released but the unused
	// sandbox remained preparing. Register the replacement before the drain.
	otherHost := manager.Engine{ID: manager.NewID(), HostID: "different-host", Endpoint: "http://localhost:9002", Capacity: 8}
	if err = store.RegisterEngine(ctx, otherHost); err != nil {
		t.Fatal(err)
	}
	if err = store.HeartbeatEngine(ctx, old.ID, 1, false); err != nil {
		t.Fatal(err)
	}
	if err = store.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	moved, err := store.Sandboxes(ctx, otherHost.ID)
	if err != nil || len(moved) != 1 || moved[0].ID != stopped {
		t.Fatal("unsafe restart transfer", moved, err)
	}
	if err = store.SandboxState(ctx, old.ID, stopped, "ready"); err == nil {
		t.Fatal("old manager can mutate transferred sandbox")
	}
	if err = store.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	assignments, err := store.Assignments(ctx, otherHost.ID)
	if err != nil || len(assignments) != 1 || assignments[0].SessionID != first.ID {
		t.Fatal("restart lost task identity", assignments, err)
	}
}

func TestDispatchWaitsForRuntimeExitAndTransfersOnce(t *testing.T) {
	db, m, _, w, actor, agent, _ := managerFixture(t)
	ctx := context.Background()
	store := m.Store.(*managerrepo.Store)
	store.RuntimeRoot = t.TempDir()
	space, _, err := m.CreateTaskSpace(ctx, w, actor, "recovery", manager.TaskSpaceCreate{AgentID: agent})
	if err != nil {
		t.Fatal(err)
	}
	task := taskSpaceSession(t, m, w, actor, agent, space.ID)
	old := manager.Engine{ID: manager.NewID(), HostID: "old", Endpoint: "http://localhost:9001", Capacity: 1}
	if err = store.RegisterEngine(ctx, old); err != nil {
		t.Fatal(err)
	}
	if err = store.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(store.RuntimeRoot, "state", "engines", "instances")
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, old.ID+".lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"second", "third"} {
		if err = store.RegisterEngine(ctx, manager.Engine{ID: manager.NewID(), HostID: host, Endpoint: "http://localhost:9002", Capacity: 2}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(`UPDATE manager_engines SET last_seen_at=now()-interval '1 minute' WHERE id=$1`, old.ID); err != nil {
		t.Fatal(err)
	}
	if err = store.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	if pinned, err := store.Assignments(ctx, old.ID); err != nil || len(pinned) != 1 || pinned[0].Generation != 1 {
		t.Fatalf("heartbeat loss displaced a live writer: %+v %v", pinned, err)
	}
	view, err := store.Session(ctx, w, actor, task.ID)
	if err != nil || view.Execution.BlockReason == nil || *view.Execution.BlockReason != "engine_recovery_pending" {
		t.Fatalf("missing recovery state: %+v %v", view.Execution, err)
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			if err := store.Dispatch(ctx); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	var engine, owner, input string
	var generation int
	if err = db.QueryRow(`SELECT s.engine_id,b.manager_engine_id,s.generation,i.status FROM manager_sessions s
		JOIN manager_conversation_sandboxes b ON b.task_id=s.task_id JOIN manager_inputs i ON i.id=s.initial_input_id WHERE s.id=$1`, task.ID).Scan(&engine, &owner, &generation, &input); err != nil || engine == old.ID || owner != engine || generation != 2 || input != "queued" {
		t.Fatalf("engine=%s owner=%s generation=%d input=%s err=%v", engine, owner, generation, input, err)
	}
}

func TestTaskSpaceHarnessUsesRealOwnerAndCurrentGrant(t *testing.T) {
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
}

func TestTaskSpaceDispatchRejectsRevokedCreatorWithoutWorkspaceOwner(t *testing.T) {
	db, m, _, w, actor, agent, _ := managerFixture(t)
	ctx := context.Background()
	store := m.Store.(*managerrepo.Store)
	space, _, err := m.CreateTaskSpace(ctx, w, actor, "space", manager.TaskSpaceCreate{AgentID: agent})
	if err != nil {
		t.Fatal(err)
	}
	session := taskSpaceSession(t, m, w, actor, agent, space.ID)
	if _, err = db.Exec(`UPDATE platform_members SET status='disabled' WHERE workspace_id=$1 AND user_id=$2`, w, actor); err != nil {
		t.Fatal(err)
	}
	if err = store.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	var state string
	if err = db.QueryRow(`SELECT status FROM manager_inputs WHERE id=$1`, *session.InitialInputID).Scan(&state); err != nil || state != "rejected" {
		t.Fatal("revoked input stuck before dispatch", state, err)
	}
	var boxes int
	if err = db.QueryRow(`SELECT count(*) FROM manager_conversation_sandboxes`).Scan(&boxes); err != nil || boxes != 0 {
		t.Fatal("unauthorized sandbox allocated", boxes, err)
	}
}

func TestConversationTasksStillShareOnlyConversationSandbox(t *testing.T) {
	_, m, _, w, actor, agent, _ := managerFixture(t)
	ctx := context.Background()
	store := m.Store.(*managerrepo.Store)
	conversation, err := m.CreateConversation(ctx, w, actor, manager.CreateConversation{AgentID: agent})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		req := manager.CreateSession{ConversationID: conversation.ID, Mode: "task"}
		req.Input = &struct {
			Text string `json:"text"`
		}{Text: "ordinary shared work"}
		if _, _, err = m.CreateSession(ctx, w, actor, manager.NewID(), req); err != nil {
			t.Fatal(err)
		}
	}
	engine := manager.Engine{ID: manager.NewID(), HostID: "conversation-host", Endpoint: "http://localhost:9000", Capacity: 8}
	if err = store.RegisterEngine(ctx, engine); err != nil {
		t.Fatal(err)
	}
	if err = store.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	boxes, err := store.Sandboxes(ctx, engine.ID)
	if err != nil || len(boxes) != 1 {
		t.Fatal("conversation sharing changed", boxes, err)
	}
	box := boxes[0]
	if box.ConversationID != conversation.ID || box.TaskSpaceID != "" || box.TaskID != "" || len(box.Sessions) != 2 {
		t.Fatal("wrong conversation owner", box)
	}
	for _, session := range box.Sessions {
		if session.ConversationID != conversation.ID || session.TaskSpaceID != "" || session.TaskID == "" {
			t.Fatal("lost conversation task provenance", session)
		}
	}
}
