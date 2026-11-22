package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/controller"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestOfficialResourcesAcrossWorkspaces(t *testing.T) {
	ctx := context.Background()
	db, models, source, publisher := modelFixture(t)
	runtime := runtimeCapabilityServices(t, db)
	catalog := &ResourceCatalog{DB: db, Assets: runtime.Skills.Assets}
	actor := service.NewID()
	insertWorkspaceAccount(t, db, actor, "target-catalog@example.test")
	target, err := (&service.WorkspaceService{Store: NewWorkspaceStore(db)}).Create(ctx, actor, "Target", "")
	if err != nil {
		t.Fatal(err)
	}
	local := publishRuntimeDirectory(t, ctx, runtime.Skills, source, publisher, service.Skill{}, "first")
	publication := service.ResourcePublication{SourceWorkspaceID: source, ResourceID: local.ID, VersionID: local.CurrentVersionID}
	if _, err = catalog.Publish(ctx, actor, "skill", "", "", publication); err == nil {
		t.Fatal("workspace admin published an official resource")
	}
	if _, err = db.ExecContext(ctx, "UPDATE platform_accounts SET is_platform_admin=true WHERE id=$1", publisher); err != nil {
		t.Fatal(err)
	}
	row, err := catalog.Publish(ctx, publisher, "skill", "", "", publication)
	if err != nil {
		t.Fatal(err)
	}
	skill := row.(service.Skill)
	if skill.Scope != "official" || !skill.Listed || skill.ID == local.ID {
		t.Fatal(skill)
	}
	if _, err = runtime.Skills.Get(ctx, target.ID, actor, local.ID); err == nil {
		t.Fatal("foreign private skill readable")
	}
	if _, err = runtime.Skills.File(ctx, target.ID, actor, skill.ID, skill.CurrentVersionID, "SKILL.md"); err != nil {
		t.Fatal(err)
	}
	list, err := runtime.Skills.List(ctx, target.ID, actor, 1, 20, service.ResourceListFilter{})
	if err != nil || list.Total != 0 {
		t.Fatal("legacy private catalog", list, err)
	}
	combined, err := runtime.Skills.List(ctx, target.ID, actor, 1, 20, service.ResourceListFilter{Scope: "all"})
	if err != nil || combined.Total != 1 || combined.Items[0].ID != skill.ID {
		t.Fatal(combined, err)
	}
	plugin, err := runtime.Plugins.Create(ctx, source, publisher, service.PluginCreate{Name: "MCP", Endpoint: "https://example.test/mcp", AuthType: "none"})
	if err != nil {
		t.Fatal(err)
	}
	row, err = catalog.Publish(ctx, publisher, "plugin", "", "", service.ResourcePublication{SourceWorkspaceID: source, ResourceID: plugin.ID, VersionID: plugin.VersionID})
	if err != nil {
		t.Fatal(err)
	}
	officialPlugin := row.(service.Plugin)
	if p, e := runtime.Plugins.Get(ctx, target.ID, actor, officialPlugin.ID); e != nil || p.Scope != "official" {
		t.Fatal(p, e)
	}
	model := saveTestModel(t, models, target.ID, actor)
	employee, err := (&service.AgentService{Store: NewAgentStore(db)}).Create(ctx, target.ID, actor, service.AgentCreate{Name: "Target employee", ModelProfileID: model.ID})
	if err != nil {
		t.Fatal(err)
	}
	createPackage := func(name string) service.CapabilityPackage {
		p, e := runtime.Packages.Create(ctx, target.ID, actor, employee.ID, service.PackageCreate{Name: name})
		if e != nil {
			t.Fatal(e)
		}
		return p
	}
	first, second := createPackage("First"), createPackage("Second")
	if _, err = db.ExecContext(ctx, "INSERT INTO platform_capability_package_skills(workspace_id,package_id,skill_id,version_id) VALUES($1,$2,$3,$4)", target.ID, first.ID, local.ID, local.CurrentVersionID); err == nil {
		t.Fatal("cross-tenant private grant passed DB constraint")
	}
	assign := func(kind, id, pin string, targets ...service.AssignmentTarget) []service.AssignmentResult {
		results, e := runtime.Packages.Assign(ctx, target.ID, actor, kind, id, service.ResourceAssignment{VersionID: pin, Targets: targets})
		if e != nil {
			t.Fatal(e)
		}
		return results
	}
	a := service.AssignmentTarget{AgentID: employee.ID, PackageID: first.ID, Version: first.Version}
	b := service.AssignmentTarget{AgentID: employee.ID, PackageID: second.ID, Version: second.Version + 1}
	results := assign("skill", skill.ID, skill.CurrentVersionID, a, b)
	if results[0].Status != "assigned" || results[1].Status != "package_version_conflict" {
		t.Fatal(results)
	}
	if result := assign("skill", skill.ID, skill.CurrentVersionID, a); result[0].Status != "already_assigned" {
		t.Fatal(result)
	}
	b.Version = second.Version
	if result := assign("plugin", officialPlugin.ID, officialPlugin.VersionID, b); result[0].Status != "assigned" {
		t.Fatal(result)
	}
	b.Version++
	runtime.Plugins.Probe = &runtimeProbe{}
	_, err = runtime.Plugins.PutAccount(ctx, target.ID, actor, employee.ID, officialPlugin.ID, service.PluginAccountPut{Name: "Target account", PluginVersionID: officialPlugin.VersionID})
	if err != nil {
		t.Fatal(err)
	}
	agent2, err := (&service.AgentService{Store: NewAgentStore(db)}).Create(ctx, target.ID, actor, service.AgentCreate{Name: "Second employee", ModelProfileID: model.ID})
	if err != nil {
		t.Fatal(err)
	}
	pack2, err := runtime.Packages.Create(ctx, target.ID, actor, agent2.ID, service.PackageCreate{Name: "Pending authorization"})
	if err != nil {
		t.Fatal(err)
	}
	withAuth := a
	withAuth.Version++
	withoutAuth := service.AssignmentTarget{AgentID: agent2.ID, PackageID: pack2.ID, Version: pack2.Version}
	results = assign("plugin", officialPlugin.ID, officialPlugin.VersionID, withAuth, withoutAuth)
	if results[0].Status != "assigned" || results[1].Status != "assigned" {
		t.Fatal(results)
	}
	uses, err := catalog.Uses(ctx, target.ID, actor, "skill", skill.ID, 1, 20)
	if err != nil || uses.(map[string]any)["total"] != 1 {
		t.Fatal(uses, err)
	}
	otherUses, err := catalog.Uses(ctx, source, publisher, "skill", skill.ID, 1, 20)
	if err != nil || otherUses.(map[string]any)["total"] != 0 {
		t.Fatal(otherUses, err)
	}
	row, err = catalog.Publish(ctx, actor, "skill", target.ID, "", service.ResourcePublication{SourceWorkspaceID: target.ID, ResourceID: skill.ID, VersionID: skill.CurrentVersionID})
	if err != nil {
		t.Fatal(err)
	}
	fork := row.(service.Skill)
	if result := assign("skill", fork.ID, fork.CurrentVersionID, b); result[0].Status != "assigned" {
		t.Fatal(result)
	}
	var detail *service.Error
	if err = catalog.Archive(ctx, target.ID, actor, "skill", fork.ID, fork.Version); !errors.As(err, &detail) || detail.Code != "resource_in_use" {
		t.Fatal(err)
	}
	off := false
	if _, err = catalog.Change(ctx, publisher, "skill", skill.ID, service.ResourceStateChange{Version: skill.Version, Listed: &off}); err != nil {
		t.Fatal(err)
	}
	b.Version++
	if result := assign("skill", skill.ID, skill.CurrentVersionID, b); result[0].Status != "skill_unavailable" {
		t.Fatal(result)
	}
	combined, err = runtime.Skills.List(ctx, target.ID, actor, 1, 20, service.ResourceListFilter{Scope: "all"})
	if err != nil || combined.Total != 1 || combined.Items[0].ID != fork.ID {
		t.Fatal(combined, err)
	}
	if _, err = runtime.Skills.RuntimeSkill(ctx, target.ID, actor, service.PackageSkill{SkillID: skill.ID, VersionID: skill.CurrentVersionID}); err != nil {
		t.Fatal("unlisting broke an existing pin", err)
	}
}
func TestResourceCatalogHTTP(t *testing.T) {
	db, _, w, owner := modelFixture(t)
	ctx := context.Background()
	runtime := runtimeCapabilityServices(t, db)
	auth := &AuthStore{DB: db}
	admin := service.NewID()
	insertWorkspaceAccount(t, db, admin, "catalog-admin@example.test")
	if e := auth.CreateSession(ctx, service.HashToken("bootstrap-catalog-admin"), admin, time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	const token = "catalog-http-test"
	if e := auth.CreateSession(ctx, service.HashToken(token), owner, time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	catalog := &ResourceCatalog{DB: db, Assets: runtime.Skills.Assets}
	handler := (&controller.Server{Auth: &service.AuthService{Store: auth}, Skills: runtime.Skills, Plugins: runtime.Plugins, Packages: runtime.Packages, Catalog: catalog}).Handler()
	request := func(method, path string, body any, want int) *httptest.ResponseRecorder {
		t.Helper()
		var data bytes.Buffer
		if body != nil {
			if e := json.NewEncoder(&data).Encode(body); e != nil {
				t.Fatal(e)
			}
		}
		req := httptest.NewRequest(method, "/api/v1"+path, &data)
		req.AddCookie(&http.Cookie{Name: "kiko_session", Value: token})
		req.Header.Set("X-CSRF-Protection", "1")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != want {
			t.Fatalf("%s %s: got %d, want %d: %s", method, path, response.Code, want, response.Body)
		}
		return response
	}
	p, err := runtime.Plugins.Create(ctx, w, owner, service.PluginCreate{Name: "Plugin", Endpoint: "https://example.test/mcp", AuthType: "none"})
	if err != nil {
		t.Fatal(err)
	}
	source := service.ResourcePublication{SourceWorkspaceID: w, ResourceID: p.ID, VersionID: p.VersionID}
	request("POST", "/official-plugins", source, 403)
	if _, err = db.ExecContext(ctx, "UPDATE platform_accounts SET is_platform_admin=true WHERE id=$1", owner); err != nil {
		t.Fatal(err)
	}
	result := request("POST", "/official-plugins", source, 201)
	var official service.Plugin
	if err = json.Unmarshal(result.Body.Bytes(), &official); err != nil {
		t.Fatal(err)
	}
	request("GET", "/official-plugins/"+official.ID+"/versions", nil, 200)
	source.Version = official.Version
	request("POST", "/official-plugins/"+official.ID+"/versions", source, 201)
	request("POST", "/official-plugins/"+official.ID+"/versions", source, 409)
	base := "/workspaces/" + w + "/plugins"
	request("GET", base+"?scope=all", nil, 200)
	request("GET", base+"?scope=manage", nil, 400)
	request("PATCH", base+"/"+official.ID, map[string]any{"version": 2, "enabled": false}, 403)
	request("DELETE", base+"/"+official.ID, map[string]int{"version": 2}, 404)
	result = request("POST", base+"/"+official.ID+"/copy", map[string]string{"version_id": official.VersionID}, 201)
	var fork service.Plugin
	if err = json.Unmarshal(result.Body.Bytes(), &fork); err != nil {
		t.Fatal(err)
	}
	request("DELETE", base+"/"+fork.ID, map[string]int{"version": 1}, 204)
	request("GET", base+"/"+fork.ID, nil, 404)
	request("PATCH", "/official-plugins/"+official.ID, map[string]any{"version": 2, "listed": false}, 200)
	request("GET", "/official-skills", nil, 200)
}
