package repository

import (
	"context"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"testing"
)

func TestTaskSpaceRuntimeRejectsSpoofedAndRevokedScope(t *testing.T) {
	db, m, _, w, actor, employee, _ := webMessageFixture(t)
	ctx := context.Background()
	space, _, err := m.CreateTaskSpace(ctx, w, actor, "runtime-space", manager.TaskSpaceCreate{AgentID: employee})
	if err != nil {
		t.Fatal(err)
	}
	session, _, err := m.CreateSession(ctx, w, actor, "runtime-task", manager.CreateSession{TaskSpaceID: space.ID, AgentID: employee, Mode: "task", Input: &struct {
		Text string `json:"text"`
	}{Text: "task"}})
	if err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := db.Exec(q, args...); e != nil {
			t.Fatal(e)
		}
	}
	var input string
	if err = db.QueryRow(`SELECT id FROM manager_inputs WHERE session_id=$1`, session.ID).Scan(&input); err != nil {
		t.Fatal(err)
	}
	turn := service.NewID()
	exec(`INSERT INTO manager_turns(id,workspace_id,session_id,input_id,turn_no,assignment_id,generation,status)VALUES($1,$2,$3,$4,1,$5,1,'running')`, turn, w, session.ID, input, service.NewID())
	exec(`UPDATE manager_sessions SET active_turn_id=$2 WHERE id=$1`, session.ID, turn)
	exec(`UPDATE manager_inputs SET status='started',turn_id=$2 WHERE id=$1`, input, turn)
	identity := service.TaskSpaceExecution{TaskSpaceID: space.ID, InputID: input}
	check := func(g service.TaskSpaceExecution, a, emp string, running bool) error {
		tx, e := db.BeginTx(ctx, nil)
		if e != nil {
			t.Fatal(e)
		}
		defer func() { _ = tx.Rollback() }() //nolint:errcheck // Read-only test transaction.
		return (&modelTx{tx: tx, workspace: w}).AuthorizeTaskSpaceExecution(ctx, g, a, emp, running)
	}
	if err = check(identity, actor, employee, true); err != nil {
		t.Fatal(err)
	}
	// Model credential resolution has no separate employee argument; the running
	// Input must still bind the actual granted employee in the database.
	if err = check(identity, actor, "", true); err != nil {
		t.Fatal("input-bound model access rejected", err)
	}
	if check(service.TaskSpaceExecution{TaskSpaceID: space.ID}, actor, "", false) == nil {
		t.Fatal("unbound snapshot admitted")
	}
	for name, g := range map[string]service.TaskSpaceExecution{"missing input": {TaskSpaceID: space.ID}, "wrong input": {TaskSpaceID: space.ID, InputID: service.NewID()}, "wrong space": {TaskSpaceID: service.NewID(), InputID: input}} {
		t.Run(name, func(t *testing.T) {
			if check(g, actor, employee, true) == nil {
				t.Fatal("spoofed scope admitted")
			}
		})
	}
	if check(identity, service.NewID(), employee, true) == nil || check(identity, actor, service.NewID(), true) == nil {
		t.Fatal("foreign identity admitted")
	}
	if err = check(service.TaskSpaceExecution{TaskSpaceID: space.ID}, actor, employee, false); err != nil {
		t.Fatal("snapshot denied", err)
	}
	exec(`UPDATE manager_sessions SET stop_revision=stop_revision+1 WHERE id=$1`, session.ID)
	if check(identity, actor, employee, true) == nil {
		t.Fatal("stopped window admitted")
	}
	exec(`UPDATE manager_sessions SET applied_stop_revision=stop_revision WHERE id=$1`, session.ID)
	exec(`UPDATE manager_task_space_agents SET status='removed',admission_epoch=admission_epoch+1 WHERE task_space_id=$1`, space.ID)
	if check(identity, actor, employee, true) == nil {
		t.Fatal("removed grant admitted")
	}
	exec(`UPDATE manager_task_space_agents SET status='active',admission_epoch=admission_epoch+1 WHERE task_space_id=$1`, space.ID)
	if check(identity, actor, employee, true) == nil {
		t.Fatal("regrant revived old session")
	}
}
