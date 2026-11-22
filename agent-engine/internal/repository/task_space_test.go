package repository

import (
	"context"
	"testing"
)

func TestTaskSpaceClaimIsolationAndRevocation(t *testing.T) {
	db, store, _ := triggerFixture(t)
	ctx := context.Background()
	ws, actor, agent, space := fixtureID(1), fixtureID(2), fixtureID(4), fixtureID(80)
	claimExec(t, db, `INSERT INTO manager_task_spaces(id,workspace_id,created_by)VALUES($1,$2,$3)`, space, ws, actor)
	claimExec(t, db, `INSERT INTO manager_task_space_agents(task_space_id,workspace_id,agent_id)VALUES($1,$2,$3)`, space, ws, agent)
	var works []Work
	for n := 0; n < 2; n++ {
		task, session, input := fixtureID(81+n*3), fixtureID(82+n*3), fixtureID(83+n*3)
		claimExec(t, db, `INSERT INTO manager_tasks(id,workspace_id,task_space_id,agent_id)VALUES($1,$2,$3,$4)`, task, ws, space, agent)
		claimExec(t, db, `INSERT INTO manager_sessions(id,workspace_id,agent_id,task_space_id,task_space_agent_epoch,task_id,created_by,mode,configuration,engine_id,assignment_id,generation,last_input_seq)VALUES($1,$2,$3,$4,1,$5,$6,'task','{}',$7,$8,1,1)`, session, ws, agent, space, task, actor, store.EngineID, fixtureID(11))
		claimExec(t, db, `INSERT INTO manager_inputs(id,workspace_id,session_id,submitted_by,admission_epoch,seq,text)VALUES($1,$2,$3,$4,1,1,'space task')`, input, ws, session, actor)
		w, err := store.Refresh(ctx, Assignment{SessionID: session, AssignmentID: fixtureID(11), Generation: 1})
		if err != nil {
			t.Fatal(err)
		}
		if w.ConversationID != "" || w.TaskSpaceID != space || w.TaskID != task || w.ReportToChat != nil {
			t.Fatal("incorrect owner or implicit report", w)
		}
		works = append(works, w)
	}
	first, err := store.Claim(ctx, works[0], fixtureID(90))
	if err != nil || first == nil {
		t.Fatal(first, err)
	}
	second, err := store.Claim(ctx, works[1], fixtureID(91))
	if err != nil || second == nil {
		t.Fatal("independent task blocked", second, err)
	}
	if first.Input.ID == second.Input.ID {
		t.Fatal("task input crossed")
	}
	for n, w := range works {
		turn := first
		if n == 1 {
			turn = second
		}
		if err := store.Complete(ctx, w, *turn, "completed", "", "done"); err != nil {
			t.Fatal(err)
		}
	}
	w := works[0]
	claimExec(t, db, `UPDATE manager_sessions SET last_input_seq=2 WHERE id=$1`, w.SessionID)
	claimExec(t, db, `INSERT INTO manager_inputs(id,workspace_id,session_id,submitted_by,admission_epoch,seq,text)VALUES($1,$2,$3,$4,1,2,'old permission')`, fixtureID(92), ws, w.SessionID, actor)
	claimExec(t, db, `UPDATE manager_task_space_agents SET admission_epoch=admission_epoch+2 WHERE task_space_id=$1`, space)
	got, err := store.Claim(ctx, w, fixtureID(93))
	if err != nil || got != nil {
		t.Fatal("regranted space revived old task", got, err)
	}
	var status string
	if err = db.QueryRow(`SELECT status FROM manager_inputs WHERE id=$1`, fixtureID(92)).Scan(&status); err != nil || status != "rejected" {
		t.Fatal(status, err)
	}
}
