package repository

import (
	"context"
	"strings"
	"testing"
)

func TestTriggerInputIndependentPrincipalAndEventFence(t *testing.T) {
	db, store, _ := triggerFixture(t)
	ctx := context.Background()
	w, a, agent := fixtureID(1), fixtureID(2), fixtureID(4)
	trigger, space, task, session := fixtureID(110), fixtureID(111), fixtureID(112), fixtureID(113)
	claimExec(t, db, `UPDATE platform_agents SET usage_config=jsonb_set(usage_config,'{access,allow}','[]') WHERE id=$1`, agent)
	claimExec(t, db, `INSERT INTO manager_triggers(id,workspace_id,agent_id,kind,enabled,config,allowed_actions,created_by)VALUES($1,$2,$3,'webhook',true,'{}','["create_task","supplement_task"]',$4)`, trigger, w, agent, a)
	claimExec(t, db, `INSERT INTO manager_task_spaces(id,workspace_id,created_by)VALUES($1,$2,$3)`, space, w, a)
	claimExec(t, db, `INSERT INTO manager_tasks(id,workspace_id,agent_id,task_space_id,trigger_id,trigger_version)VALUES($1,$2,$3,$4,$5,1)`, task, w, agent, space, trigger)
	claimExec(t, db, `INSERT INTO manager_sessions(id,workspace_id,agent_id,task_space_id,task_id,trigger_id,trigger_version,mode,configuration,engine_id,assignment_id,generation,last_input_seq)VALUES($1,$2,$3,$4,$5,$6,1,'task','{}',$7,$8,1,2)`, session, w, agent, space, task, trigger, store.EngineID, fixtureID(11))
	for n := 0; n < 2; n++ {
		event, input := fixtureID(114+n*2), fixtureID(115+n*2)
		claimExec(t, db, `INSERT INTO manager_trigger_events(id,trigger_id,workspace_id,agent_id,binding_version,event_id,body_hash,action,payload,occurred_at,task_id,session_id,input_id,status)VALUES($1,$2,$3,$4,1,$5,$6,'create_task','{}',now(),$7,$8,$9,'accepted')`, event, trigger, w, agent, event, strings.Repeat("a", 64), task, session, input)
		claimExec(t, db, `INSERT INTO manager_inputs(id,workspace_id,session_id,trigger_event_id,admission_epoch,seq,text,source_kind,trigger_agent_loop,delivery_mode)VALUES($1,$2,$3,$4,1,$5,'scheduled instruction','system',true,'steer')`, input, w, session, event, n+1)
	}
	work, err := store.Refresh(ctx, Assignment{SessionID: session, AssignmentID: fixtureID(11), Generation: 1})
	if err != nil {
		t.Fatal(err)
	}
	if work.TriggerID != trigger || work.ReportToChat != nil {
		t.Fatal("lost trigger or fabricated report")
	}
	first, err := store.Claim(ctx, work, fixtureID(119))
	if err != nil || first == nil {
		t.Fatal(first, err)
	}
	if first.Input.PrincipalType != "trigger" || first.Input.SubmittedBy != "" || first.Input.ChannelPrincipalID != nil || !strings.HasPrefix(first.Input.Text, "[系统消息]") {
		t.Fatal("incorrect input provenance", first.Input)
	}
	steer, err := store.ClaimSteering(ctx, work, *first)
	if err != nil || steer != nil {
		t.Fatal("different event inherited active turn", steer, err)
	}
	if err = store.Complete(ctx, work, *first, "completed", "", "done"); err != nil {
		t.Fatal(err)
	}
	claimExec(t, db, `UPDATE manager_triggers SET version=2 WHERE id=$1`, trigger)
	second, err := store.Claim(ctx, work, fixtureID(120))
	if err != nil || second != nil {
		t.Fatal("stale event executed", second, err)
	}
	var status string
	if err = db.QueryRow(`SELECT status FROM manager_inputs WHERE id=$1`, fixtureID(117)).Scan(&status); err != nil || status != "rejected" {
		t.Fatal(status, err)
	}
}
