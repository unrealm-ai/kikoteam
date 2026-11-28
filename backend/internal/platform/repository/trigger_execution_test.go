package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"strings"
	"testing"
)

func triggerRuntimeFixture(t *testing.T) (*sql.DB, string, string, string, service.TriggerExecution) {
	t.Helper()
	db, _, _, w, actor, employee, _ := managerFixture(t)
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	trigger, event, space, task, session, input, turn := service.NewID(), service.NewID(), service.NewID(), service.NewID(), service.NewID(), service.NewID(), service.NewID()
	// Human employee admission and manual space grants are deliberately absent.
	exec(`UPDATE platform_agents SET usage_config=jsonb_set(usage_config,'{access}', '{"mode":"allowlist","group_members_enabled":false,"allow":[],"deny":[]}'::jsonb) WHERE id=$1`, employee)
	exec(`INSERT INTO manager_triggers(id,workspace_id,agent_id,name,kind,enabled,config,allowed_actions,created_by)VALUES($1,$2,$3,'runtime test','webhook',true,'{}','["create_task"]',$4)`, trigger, w, employee, actor)
	exec(`INSERT INTO manager_trigger_events(id,trigger_id,workspace_id,agent_id,binding_version,event_id,body_hash,action,payload,occurred_at)VALUES($1,$2,$3,$4,1,'event','`+strings.Repeat("a", 64)+`','create_task','{}',now())`, event, trigger, w, employee)
	exec(`INSERT INTO manager_task_spaces(id,workspace_id,created_by)VALUES($1,$2,$3)`, space, w, actor)
	exec(`INSERT INTO manager_tasks(id,workspace_id,agent_id,task_space_id,trigger_id,trigger_version)VALUES($1,$2,$3,$4,$5,1)`, task, w, employee, space, trigger)
	exec(`INSERT INTO manager_sessions(id,workspace_id,agent_id,task_space_id,task_id,trigger_id,trigger_version,mode,configuration,last_input_seq)VALUES($1,$2,$3,$4,$5,$6,1,'task','{}',1)`, session, w, employee, space, task, trigger)
	exec(`INSERT INTO manager_inputs(id,workspace_id,session_id,trigger_event_id,admission_epoch,seq,text)VALUES($1,$2,$3,$4,1,1,'trigger body')`, input, w, session, event)
	exec(`UPDATE manager_trigger_events SET status='accepted',task_id=$2,session_id=$3,input_id=$4 WHERE id=$1`, event, task, session, input)
	exec(`INSERT INTO manager_turns(id,workspace_id,session_id,input_id,turn_no,assignment_id,generation,status)VALUES($1,$2,$3,$4,1,$5,1,'running')`, turn, w, session, input, service.NewID())
	exec(`UPDATE manager_sessions SET active_turn_id=$2 WHERE id=$1`, session, turn)
	exec(`UPDATE manager_inputs SET status='started',turn_id=$2 WHERE id=$1`, input, turn)
	return db, w, actor, employee, service.TriggerExecution{TriggerID: trigger, Version: 1, EventID: event, InputID: input}
}
func TestTriggerRuntimeProofAndPackageScope(t *testing.T) {
	db, w, actor, employee, g := triggerRuntimeFixture(t)
	ctx := context.Background()
	check := func(q service.TriggerExecution, a, emp string, running bool) error {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }() //nolint:errcheck // Read-only test transaction.
		return (&modelTx{tx: tx, workspace: w}).AuthorizeTriggerExecution(ctx, q, a, emp, running)
	}
	if err := check(g, "", employee, true); err != nil {
		t.Fatal("independent trigger blocked", err)
	}
	if err := check(g, "", "", true); err != nil {
		t.Fatal("model proof blocked", err)
	}
	if check(g, actor, employee, true) == nil {
		t.Fatal("human identity borrowed")
	}
	for _, bad := range []service.TriggerExecution{{TriggerID: g.TriggerID, Version: 2, EventID: g.EventID, InputID: g.InputID}, {TriggerID: service.NewID(), Version: 1, EventID: g.EventID, InputID: g.InputID}, {TriggerID: g.TriggerID, Version: 1, EventID: service.NewID(), InputID: g.InputID}, {TriggerID: g.TriggerID, Version: 1, EventID: g.EventID, InputID: service.NewID()}} {
		if check(bad, "", employee, true) == nil {
			t.Fatal("forged trigger accepted")
		}
	}
	// Admission may snapshot only after a real pending event exists, without
	// impersonating its administrator or needing a fabricated running Input.
	pending := service.NewID()
	if _, err := db.Exec(`INSERT INTO manager_trigger_events(id,trigger_id,workspace_id,agent_id,binding_version,event_id,body_hash,action,payload,occurred_at)VALUES($1,$2,$3,$4,1,'pending','`+strings.Repeat("b", 64)+`','create_task','{}',now())`, pending, g.TriggerID, w, employee); err != nil {
		t.Fatal(err)
	}
	admission := service.TriggerExecution{TriggerID: g.TriggerID, Version: 1, EventID: pending}
	if err := check(admission, "", employee, false); err != nil {
		t.Fatal("real pending admission blocked", err)
	}
	if check(admission, "", "", false) == nil || check(admission, "", service.NewID(), false) == nil || check(admission, "", employee, true) == nil {
		t.Fatal("pending admission accepted missing/foreign employee or running without Input")
	}
	packages := &service.PackageService{Store: NewPackageStore(db)}
	p := defaultEmployeePackage(t, packages, w, actor, employee)
	snapshot := func() service.ExecutionCapabilities {
		tx, e := db.BeginTx(ctx, nil)
		if e != nil {
			t.Fatal(e)
		}
		defer func() { _ = tx.Rollback() }() //nolint:errcheck // Read-only test transaction.
		s, e := SnapshotCapabilities(service.WithTriggerExecution(ctx, g), tx, w, "", employee)
		if e != nil {
			t.Fatal(e)
		}
		return s
	}
	old := snapshot()
	if len(old.Packages) != 1 {
		t.Fatal("enabled default package missing", old)
	}
	scope := service.PackageTriggerScope{Mode: "all", AllowTriggerIDs: []string{}, DenyTriggerIDs: []string{g.TriggerID}}
	if _, err := packages.Update(ctx, w, actor, employee, p.ID, service.PackagePatch{Version: p.Version, TriggerScope: &scope}); err != nil {
		t.Fatal(err)
	}
	current := snapshot()
	if len(current.Packages) != 0 {
		t.Fatal("deny did not filter initial snapshot")
	}
	runtime := &service.RuntimeService{Packages: packages}
	effective, err := runtime.Effective(service.WithTriggerExecution(ctx, g), w, "", employee, &old)
	if err != nil || len(effective) != 0 {
		t.Fatal("old snapshot bypassed current deny", effective, err)
	}
	foreign := service.PackageTriggerScope{Mode: "allowlist", AllowTriggerIDs: []string{service.NewID()}, DenyTriggerIDs: []string{}}
	if _, err = packages.Update(ctx, w, actor, employee, p.ID, service.PackagePatch{Version: p.Version, TriggerScope: &foreign}); err == nil {
		t.Fatal("unverified trigger ID accepted")
	}
	if _, err = db.Exec(`UPDATE manager_triggers SET version=2 WHERE id=$1`, g.TriggerID); err != nil {
		t.Fatal(err)
	}
	if check(g, "", employee, true) == nil {
		t.Fatal("old trigger version revived")
	}
	raw, err := json.Marshal(p)
	if err != nil || !strings.Contains(string(raw), "trigger_scope") {
		t.Fatal("public scope missing")
	}
}
