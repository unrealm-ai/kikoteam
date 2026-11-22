package repository

import (
	"context"
	"errors"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestAgentLifecycleAndVersions(t *testing.T) {
	db, models, w, owner := modelFixture(t)
	ctx := context.Background()
	m := saveTestModel(t, models, w, owner)
	s := &service.AgentService{Store: NewAgentStore(db)}
	a, e := s.Create(ctx, w, owner, service.AgentCreate{Name: "Employee", ModelProfileID: m.ID})
	if e != nil {
		t.Fatal(e)
	}
	if a.Instruction != "" || a.ConfigVersion != 1 || a.Version != 1 || !a.ModelAvailable {
		t.Fatal(a)
	}
	name := "New name"
	a, e = s.Update(ctx, w, owner, a.ID, service.AgentPatch{Version: a.Version, Name: &name})
	if e != nil {
		t.Fatal(e)
	}
	if a.ConfigVersion != 2 || a.Version != 2 {
		t.Fatal(a)
	}
	disabled := false
	a, e = s.Update(ctx, w, owner, a.ID, service.AgentPatch{Version: a.Version, Enabled: &disabled})
	if e != nil {
		t.Fatal(e)
	}
	if a.ConfigVersion != 2 || a.Version != 3 || a.AdmissionEpoch != 2 {
		t.Fatal(a)
	}
	var count int
	if e = db.QueryRow(`SELECT count(*) FROM platform_agent_versions WHERE agent_id=$1`, a.ID).Scan(&count); e != nil || count != 2 {
		t.Fatal(count, e)
	}
	if _, e = models.Change(ctx, w, owner, m.ID, "status", m.Version, false); e != nil {
		t.Fatal(e)
	}
	a, e = s.Update(ctx, w, owner, a.ID, service.AgentPatch{Version: a.Version, Name: &name})
	if e != nil || a.ModelAvailable {
		t.Fatal(a, e)
	}
	enabled := true
	if _, e = s.Update(ctx, w, owner, a.ID, service.AgentPatch{Version: a.Version, Enabled: &enabled}); e == nil {
		t.Fatal("enabled unavailable model")
	}
	if _, e = s.Create(ctx, w, owner, service.AgentCreate{Name: "Unavailable", ModelProfileID: m.ID}); e == nil {
		t.Fatal("created with disabled model")
	}
}
func TestAgentAuthorizationIsolationAndConcurrentPatch(t *testing.T) {
	db, models, w, owner := modelFixture(t)
	ctx := context.Background()
	m := saveTestModel(t, models, w, owner)
	s := &service.AgentService{Store: NewAgentStore(db)}
	member := service.NewID()
	insertWorkspaceAccount(t, db, member, "agent-member@example.test")
	if _, e := db.Exec(`INSERT INTO platform_members(workspace_id,user_id,role,status,version) VALUES($1,$2,'member','active',1)`, w, member); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Create(ctx, w, member, service.AgentCreate{Name: "Denied", ModelProfileID: m.ID}); e == nil {
		t.Fatal("member write")
	}
	a, e := s.Create(ctx, w, owner, service.AgentCreate{Name: "Employee", ModelProfileID: m.ID})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Get(ctx, w, member, a.ID); e != nil {
		t.Fatal(e)
	}
	other, e := (&service.WorkspaceService{Store: NewWorkspaceStore(db)}).Create(ctx, owner, "Other", "")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Create(ctx, other.ID, owner, service.AgentCreate{Name: "Cross", ModelProfileID: m.ID}); e == nil {
		t.Fatal("cross-workspace model")
	}
	if _, e = s.Get(ctx, other.ID, owner, a.ID); e == nil {
		t.Fatal("cross-workspace agent")
	}
	name := "Concurrent"
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := s.Update(ctx, w, owner, a.ID, service.AgentPatch{Version: 1, Name: &name})
			results <- e
		}()
	}
	wg.Wait()
	close(results)
	success := 0
	for e := range results {
		if e == nil {
			success++
			continue
		}
		var se *service.Error
		if !errors.As(e, &se) || se.Code != "agent_version_conflict" {
			t.Fatal(e)
		}
	}
	if success != 1 {
		t.Fatal(success)
	}
	p, e := s.List(ctx, w, owner, service.AgentListQuery{Page: 1, PageSize: 1})
	if e != nil || p.Total != 1 || len(p.Items) != 1 {
		t.Fatal(p, e)
	}
}

func TestAgentDirectoryFiltersAndResourceVisibility(t *testing.T) {
	db, _, _, workspace, owner, agent, _ := channelManagerFixture(t)
	ctx := context.Background()
	svc := service.AgentService{Store: NewAgentStore(db)}
	reviewExec(t, db, `UPDATE platform_agents SET role='50%_Reports',enabled=false WHERE id=$1`, agent)
	reviewExec(t, db, `INSERT INTO platform_capability_packages (id,workspace_id,agent_id,name,version,scope)
        VALUES ($1,$2,$3,'employee package',1,'{}'),($4,$2,NULL,'workspace template',1,'{}')`, service.NewID(), workspace, agent, service.NewID())
	reviewExec(t, db, `INSERT INTO manager_triggers(id,workspace_id,agent_id,kind,config,allowed_actions,created_by) VALUES($1,$2,$3,'schedule','{}','[]',$4)`, service.NewID(), workspace, agent, owner)
	reviewExec(t, db, `INSERT INTO platform_agents(id,workspace_id,name,role,model_profile_id,version,config_version,admission_epoch,enabled,created_at)
        SELECT gen_random_uuid(),a.workspace_id,'Employee '||i,'reports',a.model_profile_id,1,1,1,true,a.created_at
        FROM platform_agents a CROSS JOIN generate_series(1,20) i WHERE a.id=$1`, agent)
	q := service.AgentListQuery{Search: " REPorTS ", Page: 1, PageSize: 20}
	first, err := svc.List(ctx, workspace, owner, q)
	if err != nil || first.Total != 21 || first.Counts.Enabled != 20 || first.Counts.Disabled != 1 || len(first.Items) != 20 {
		t.Fatal(first, err)
	}
	q.Page = 2
	second, err := svc.List(ctx, workspace, owner, q)
	if err != nil || len(second.Items) != 1 {
		t.Fatal(second, err)
	}
	for _, item := range first.Items {
		if item.ID == second.Items[0].ID {
			t.Fatal("duplicate employee across pages")
		}
	}
	disabled := false
	q.Page, q.Enabled = 1, &disabled
	result, err := svc.List(ctx, workspace, owner, q)
	if err != nil || result.Total != 1 || result.Counts.Enabled != 20 || len(result.Items) != 1 || result.Items[0].ID != agent {
		t.Fatal(result, err)
	}
	summary := result.Items[0].CardResources
	if summary == nil || len(summary.Channels) != 1 || summary.Channels[0] != "feishu" || summary.PackageCount != 1 || summary.TriggerCount != 1 {
		t.Fatal(summary)
	}
	for _, role := range []string{"member", "admin"} {
		actor := service.NewID()
		insertWorkspaceAccount(t, db, actor, "directory-"+role+"@example.test")
		reviewExec(t, db, `INSERT INTO platform_members(workspace_id,user_id,role,status,version) VALUES($1,$2,$3,'active',1)`, workspace, actor, role)
		filter := service.AgentListQuery{Search: "%_", Page: 1, PageSize: 20}
		result, err := svc.List(ctx, workspace, actor, filter)
		if err != nil || result.Counts.Enabled != 0 || result.Total != 1 || len(result.Items) != 1 {
			t.Fatal(role, result, err)
		}
		if got := result.Items[0].CardResources; !reflect.DeepEqual(got, summary) {
			t.Fatalf("%s card resources = %+v, want %+v", role, got, summary)
		}
		filter = service.AgentListQuery{Search: "Employee 20", Page: 1, PageSize: 1}
		result, err = svc.List(ctx, workspace, actor, filter)
		if err != nil || len(result.Items) != 1 {
			t.Fatal(role, result, err)
		}
		if got := result.Items[0].CardResources; !reflect.DeepEqual(got, &service.AgentCardResources{Channels: []string{}}) {
			t.Fatalf("%s empty card resources = %+v", role, got)
		}
	}
	q.Page = 2
	result, err = svc.List(ctx, workspace, owner, q)
	if err != nil || result.Items == nil || len(result.Items) != 0 || result.Total != 1 {
		t.Fatal(result, err)
	}
	if _, err = svc.List(ctx, workspace, service.NewID(), q); err == nil {
		t.Fatal("non-member read directory")
	}
}

func TestAgentModelBindingDoesNotFollowDefault(t *testing.T) {
	db, models, w, owner := modelFixture(t)
	ctx := context.Background()
	first := saveTestModel(t, models, w, owner)
	second := saveTestModel(t, models, w, owner)
	s := &service.AgentService{Store: NewAgentStore(db)}
	a, e := s.Create(ctx, w, owner, service.AgentCreate{Name: "Explicit", ModelProfileID: first.ID})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = models.Change(ctx, w, owner, second.ID, "set-default", second.Version, false); e != nil {
		t.Fatal(e)
	}
	a, e = s.Get(ctx, w, owner, a.ID)
	if e != nil || a.ModelProfileID != first.ID {
		t.Fatal(a, e)
	}
	// A validated no-key model is a legitimate explicit reference.
	if _, e = db.Exec(`UPDATE platform_model_profiles SET has_key=false,credential='' WHERE id=$1`, second.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Create(ctx, w, owner, service.AgentCreate{Name: "No key", ModelProfileID: second.ID}); e != nil {
		t.Fatal(e)
	}
}

func TestAgentMigrationPreservesExistingModelAndAccount(t *testing.T) {
	db, models, w, owner := modelFixtureThrough(t, "000005_platform_models.sql")
	m := saveTestModel(t, models, w, owner)
	data, e := os.ReadFile("../../../../event-hub/database/postgres/migrations/000006_platform_agents.sql")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(strings.Split(string(data), "-- +goose Down")[0]); e != nil {
		t.Fatal(e)
	}
	got, e := models.Get(context.Background(), w, owner, m.ID)
	if e != nil || got.ID != m.ID {
		t.Fatal(got, e)
	}
	var n int
	if e = db.QueryRow(`SELECT count(*) FROM platform_accounts WHERE id=$1`, owner).Scan(&n); e != nil || n != 1 {
		t.Fatal(n, e)
	}
}
