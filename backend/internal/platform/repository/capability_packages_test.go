package repository

import (
	"context"
	"database/sql"
	"errors"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"testing"
)

func TestPackageLifecycleIsolationAndEmployeeVersions(t *testing.T) {
	db, models, w, owner := modelFixture(t)
	ctx := context.Background()
	m := saveTestModel(t, models, w, owner)
	a, e := (&service.AgentService{Store: NewAgentStore(db)}).Create(ctx, w, owner, service.AgentCreate{Name: "Employee", ModelProfileID: m.ID})
	if e != nil {
		t.Fatal(e)
	}
	s := &service.PackageService{Store: NewPackageStore(db)}
	skill, v1, v2 := service.NewID(), service.NewID(), service.NewID()
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			t.Error(err)
		}
	}()
	_, e = tx.Exec(`INSERT INTO platform_skills(id,workspace_id,name,description,version,current_version_id) VALUES($1,$2,'Skill','',1,$3)`, skill, w, v1)
	if e != nil {
		t.Fatal(e)
	}
	for i, id := range []string{v1, v2} {
		_, e = tx.Exec(`INSERT INTO platform_skill_versions(id,workspace_id,skill_id,sequence,technical_name,description,manifest,source,storage_identity,key_prefix) VALUES($1,$2,$3,$4,'skill','','{}','{}','local','test')`, id, w, skill, i+1)
		if e != nil {
			t.Fatal(e)
		}
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	template, e := s.Create(ctx, w, owner, "", service.PackageCreate{Name: "Template", Skills: []service.PackageSkill{{SkillID: skill, VersionID: v1}}})
	if e != nil {
		t.Fatal(e)
	}
	p, e := s.CopyTemplate(ctx, w, owner, a.ID, template.ID, service.PackageCopy{TemplateVersion: 1})
	if e != nil {
		t.Fatal(e)
	}
	if p.Skills[0].VersionID != v1 || p.SourceTemplateID != template.ID {
		t.Fatal(p)
	}
	_, e = db.Exec(`UPDATE platform_skills SET current_version_id=$1,version=2 WHERE id=$2`, v2, skill)
	if e != nil {
		t.Fatal(e)
	}
	p, e = s.Get(ctx, w, owner, a.ID, p.ID)
	if e != nil || p.Skills[0].VersionID != v1 {
		t.Fatal("publication changed the employee binding", p, e)
	}
	name := "Renamed"
	p, e = s.Update(ctx, w, owner, a.ID, p.ID, service.PackagePatch{Version: 1, Name: &name})
	if e != nil || p.Version != 2 || p.Skills[0].VersionID != v1 {
		t.Fatal(p, e)
	}
	if _, e = s.Update(ctx, w, owner, a.ID, p.ID, service.PackagePatch{Version: 1, Name: &name}); e == nil {
		t.Fatal("stale write accepted")
	}
	if _, e = s.Get(ctx, w, owner, "", p.ID); e == nil {
		t.Fatal("employee package exposed as template")
	}
	if _, e = s.Get(ctx, w, service.NewID(), a.ID, p.ID); e == nil {
		t.Fatal("nonmember read allowed")
	}
	var count int
	if e = db.QueryRow(`SELECT count(*) FROM platform_capability_package_versions WHERE package_id=$1`, p.ID).Scan(&count); e != nil || count != 2 {
		t.Fatal(count, e)
	}
	_, e = db.Exec(`UPDATE platform_skills SET enabled=false WHERE id=$1`, skill)
	if e != nil {
		t.Fatal(e)
	}
	disabled := false
	p, e = s.Update(ctx, w, owner, a.ID, p.ID, service.PackagePatch{Version: 2, Enabled: &disabled})
	if e != nil {
		t.Fatal("cannot disable broken package", e)
	}
	enabled := true
	if _, e = s.Update(ctx, w, owner, a.ID, p.ID, service.PackagePatch{Version: p.Version, Enabled: &enabled}); e == nil {
		t.Fatal("disabled skill rebound")
	}
}

func TestPackagePluginCanBeSavedBeforeAccountDiscovery(t *testing.T) {
	db, models, w, owner := modelFixture(t)
	ctx := context.Background()
	m := saveTestModel(t, models, w, owner)
	a, e := (&service.AgentService{Store: NewAgentStore(db)}).Create(ctx, w, owner, service.AgentCreate{Name: "Employee", ModelProfileID: m.ID})
	if e != nil {
		t.Fatal(e)
	}
	plugin, version := service.NewID(), service.NewID()
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			t.Error(err)
		}
	}()
	_, e = tx.Exec(`INSERT INTO platform_plugins(id,workspace_id,name,version,current_version_id)VALUES($1,$2,'Plugin',1,$3)`, plugin, w, version)
	if e != nil {
		t.Fatal(e)
	}
	_, e = tx.Exec(`INSERT INTO platform_plugin_versions(id,workspace_id,plugin_id,version,name,description,endpoint,auth_type)VALUES($1,$2,$3,1,'Plugin','','https://example.test/mcp','none')`, version, w, plugin)
	if e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	s := &service.PackageService{Store: NewPackageStore(db)}
	digest := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	request := service.PackageCreate{Name: "Tools", Plugins: []service.PackagePlugin{{PluginID: plugin, VersionID: version}}}
	if _, e = s.Create(ctx, w, owner, a.ID, request); e != nil {
		t.Fatal("cannot save before connecting", e)
	}
	if _, e = db.Exec(`INSERT INTO platform_agent_plugin_connection_settings(workspace_id,agent_id,plugin_id,source,revision) VALUES($1,$2,$3,'agent',0)`, w, a.ID, plugin); e != nil {
		t.Fatal(e)
	}
	_, e = db.Exec(`INSERT INTO platform_plugin_connections(id,workspace_id,agent_id,plugin_id,plugin_version_id,name,version,enabled,validated,tools,credential,validated_at,auth_method)VALUES($1,$2,$3,$4,$5,'Account',1,true,true,$6,'',now(),'none')`, service.NewID(), w, a.ID, plugin, version, `[{"name":"read","schema_digest":"`+digest+`"}]`)
	if e != nil {
		t.Fatal(e)
	}
	p, e := s.Create(ctx, w, owner, a.ID, request)
	if e != nil {
		t.Fatal(e)
	}
	request.Name = "Second package"
	if _, e = s.Create(ctx, w, owner, a.ID, request); e != nil {
		t.Fatal("same employee account reuse rejected", e)
	}
	_, e = db.Exec(`UPDATE platform_plugin_connections SET validated=false WHERE agent_id=$1`, a.ID)
	if e != nil {
		t.Fatal(e)
	}
	on := true
	if _, e = s.Update(ctx, w, owner, a.ID, p.ID, service.PackagePatch{Version: p.Version, Enabled: &on}); e != nil {
		t.Fatal("revoked account blocked editing the package", e)
	}
}
