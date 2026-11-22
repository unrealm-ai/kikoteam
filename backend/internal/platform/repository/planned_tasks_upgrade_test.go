package repository

import (
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"os"
	"strings"
	"testing"
)

func TestPlannedTasksUpgradePreservesStartedWork(t *testing.T) {
	db, models, w, actor := modelFixtureThrough(t, "000044_notification_group_candidates.sql")
	model := saveTestModel(t, models, w, actor)
	agent, conv, task, session := manager.NewID(), manager.NewID(), manager.NewID(), manager.NewID()
	for _, x := range []struct {
		q    string
		args []any
	}{
		{`INSERT INTO platform_agents(id,workspace_id,name,version,config_version,admission_epoch,model_profile_id)VALUES($1,$2,'legacy',1,1,1,$3)`, []any{agent, w, model.ID}},
		{`INSERT INTO manager_conversations(id,workspace_id,agent_id,created_by)VALUES($1,$2,$3,$4)`, []any{conv, w, agent, actor}},
		{`INSERT INTO manager_tasks(id,workspace_id,conversation_id,agent_id)VALUES($1,$2,$3,$4)`, []any{task, w, conv, agent}},
		{`INSERT INTO manager_sessions(id,workspace_id,agent_id,conversation_id,task_id,created_by,mode,configuration)VALUES($1,$2,$3,$4,$5,$6,'task','{}')`, []any{session, w, agent, conv, task, actor}},
	} {
		if _, err := db.Exec(x.q, x.args...); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile("../../../../event-hub/database/postgres/migrations/000047_planned_tasks.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(strings.Split(string(raw), "-- +goose Down")[0]); err != nil {
		t.Fatal(err)
	}
	var state string
	var version int64
	if err = db.QueryRow(`SELECT planning_state,planning_version FROM manager_tasks WHERE id=$1`, task).Scan(&state, &version); err != nil || state != "started" || version != 1 {
		t.Fatal(state, version, err)
	}
	if _, err = db.Exec(`INSERT INTO manager_inputs(id,workspace_id,session_id,submitted_by,admission_epoch,seq,text)VALUES(gen_random_uuid(),$1,$2,$3,1,1,'legacy continues')`, w, session, actor); err != nil {
		t.Fatal(err)
	}
}
