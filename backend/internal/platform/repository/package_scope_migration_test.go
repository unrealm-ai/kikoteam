package repository

import (
	"encoding/json"
	"testing"

	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestPackageScopeMigrationPreservesGrantsAndSession(t *testing.T) {
	const before = "000117_platform_timezone.sql"
	db, models, w, owner := modelFixtureThrough(t, before)
	ctx := t.Context()
	model := saveTestModel(t, models, w, owner)
	employee, err := (&service.AgentService{Store: NewAgentStore(db)}).Create(ctx, w, owner, service.AgentCreate{Name: "Fixture", ModelProfileID: model.ID})
	if err != nil {
		t.Fatal(err)
	}
	kept, denied, sender := service.NewID(), service.NewID(), service.NewID()
	var id string
	if err = db.QueryRow(`SELECT id FROM platform_capability_packages WHERE agent_id=$1`, employee.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	reviewExec(t, db, `UPDATE platform_capability_packages SET work_scope=NULL,conversation_scope=NULL,enabled=false,trigger_scope=jsonb_build_object(
 'mode','allowlist','allow_trigger_ids',jsonb_build_array($2::text,$3::text),'deny_trigger_ids',jsonb_build_array($3::text)) WHERE id=$1`, id, kept, denied)
	reviewExec(t, db, `UPDATE platform_capability_package_versions v SET snapshot=to_jsonb(p) FROM platform_capability_packages p WHERE v.package_id=p.id AND p.id=$1`, id)
	blocked := service.NewID()
	reviewExec(t, db, `INSERT INTO platform_capability_packages(id,workspace_id,agent_id,name,description,enabled,version,scope,trigger_scope,work_scope)
 VALUES($1,$2,$3,'Restricted','',true,1,'{"mode":"allowlist"}','{"mode":"all","allow_trigger_ids":[],"deny_trigger_ids":[]}',NULL)`, blocked, w, employee.ID)
	reviewExec(t, db, `INSERT INTO platform_capability_package_versions(workspace_id,package_id,version,snapshot)
 SELECT workspace_id,id,version,to_jsonb(p) FROM platform_capability_packages p WHERE id=$1`, blocked)
	m := &manager.Service{Store: &managerrepo.Store{DB: db}}
	chat, err := m.ResolveWebConversation(ctx, w, owner, employee.ID)
	if err != nil {
		t.Fatal(err)
	}
	session, _, err := m.CreateSession(ctx, w, owner, "", manager.CreateSession{ConversationID: chat.ID, Mode: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	reviewExec(t, db, `UPDATE manager_sessions SET configuration=configuration||jsonb_build_object('capabilities',jsonb_build_object('packages',(
 SELECT jsonb_agg(snapshot) FROM platform_capability_package_versions WHERE package_id IN($2,$3)))) WHERE id=$1`, session.ID, id, blocked)
	applyTestMigrations(t, ctx, db, before, "000118_explicit_package_scopes.sql")
	var modes []byte
	if err = db.QueryRow(`SELECT jsonb_object_agg(id,work_scope->>'mode') FROM platform_capability_packages WHERE agent_id=$1`, employee.ID).Scan(&modes); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{}
	if err = json.Unmarshal(modes, &values); err != nil || values[id] != "inherit" || values[blocked] != "allowlist" {
		t.Fatal(values, err)
	}
	var allowed, rejected bool
	if err = db.QueryRow(`SELECT
 manager_work_package_allows((SELECT snapshot FROM platform_capability_package_versions WHERE package_id=$1),$3),
 manager_work_package_allows((SELECT snapshot FROM platform_capability_package_versions WHERE package_id=$2),$3)`, id, blocked, sender).Scan(&allowed, &rejected); err != nil || allowed || rejected {
		t.Fatal(allowed, rejected, err)
	}
	var configuration []byte
	if err = db.QueryRow(`SELECT configuration FROM manager_sessions WHERE id=$1`, session.ID).Scan(&configuration); err != nil {
		t.Fatal(err)
	}
	var cfg manager.Configuration
	if err = json.Unmarshal(configuration, &cfg); err != nil || cfg.Capabilities == nil || len(cfg.Capabilities.Packages) != 2 {
		t.Fatal(err)
	}
	for _, p := range cfg.Capabilities.Packages {
		if p.WorkScope == nil || p.WorkScope.Mode != values[p.ID] {
			t.Fatal(p)
		}
		if p.ID == id {
			p.Enabled = true
			if !service.PackageAllowsTrigger(p, kept) || service.PackageAllowsTrigger(p, denied) || len(p.TriggerScope.DenyTriggerIDs) != 0 {
				t.Fatal(p.TriggerScope)
			}
		}
	}
}
