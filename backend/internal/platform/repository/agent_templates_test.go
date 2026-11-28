package repository

import (
	"context"
	"encoding/json"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/controller"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/integration/skillasset"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func testTemplateContent() service.TemplateContent {
	return service.TemplateContent{SchemaVersion: 1, Name: "周报员", Role: "整理周报", Category: "engineering", Instruction: "为 {{project}} 整理周报。", Parameters: []service.TemplateParameter{{Key: "project", Type: "short_text", Required: true}}, Packages: []service.TemplatePackage{}, Resources: []service.TemplateResource{}, Examples: []service.TemplateExample{{Key: "weekly", Text: "生成本周周报", Output: "本周进展与风险", PackageKeys: []string{}}}}
}
func TestTemplatePublishCreateScopeAndRollback(t *testing.T) {
	db, models, w, owner := modelFixture(t)
	ctx := context.Background()
	model := saveTestModel(t, models, w, owner)
	s := &service.TemplateService{Store: &TemplateStore{DB: db}}
	c := testTemplateContent()
	v, e := s.Create(ctx, w, owner, c)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.CreateFromTemplate(ctx, w, owner, service.TemplateCreation{TemplateID: v.ID, TemplateVersionID: v.LatestVersionID, Name: "one", ModelProfileID: model.ID, Parameters: map[string]any{"project": "支付"}}); e == nil {
		t.Fatal("created from draft")
	}
	v, e = s.Publish(ctx, w, owner, v.ID, service.TemplatePublish{Version: v.Version, TemplateVersionID: v.LatestVersionID})
	if e != nil {
		t.Fatal(e)
	}
	published := v.PublishedVersionID
	c.Name = "新版岗位"
	draft, e := s.Update(ctx, w, owner, v.ID, service.TemplatePatch{Version: v.Version, Content: &c})
	if e != nil {
		t.Fatal(e)
	}
	public, e := s.Get(ctx, w, owner, v.ID, false)
	if e != nil || public.Content.Name != "周报员" || public.Status != "published" || public.LatestVersionID != "" {
		t.Fatalf("draft leaked: %+v %v", public, e)
	}
	if _, e = s.Publish(ctx, w, owner, v.ID, service.TemplatePublish{Version: v.Version, TemplateVersionID: published}); e == nil {
		t.Fatal("stale publish accepted")
	}
	req := service.TemplateCreation{TemplateID: v.ID, TemplateVersionID: published, Name: "支付周报", ModelProfileID: model.ID, Parameters: map[string]any{"project": "支付"}}
	a, e := s.CreateFromTemplate(ctx, w, owner, req)
	if e != nil {
		t.Fatal(e)
	}
	if a.Instruction != "为 支付 整理周报。" || a.TemplateOrigin == nil || a.WebAccess.Mode != "workspace" {
		t.Fatal(a)
	}
	assertNewAgentDefaults(t, db, w, owner, a.ID)
	packages, e := (&service.PackageService{Store: NewPackageStore(db)}).List(ctx, w, owner, a.ID, 1, 20)
	if e != nil || packages.Total != 1 || len(packages.Items) != 1 {
		t.Fatal(packages, e)
	}
	assertDefaultEmployeePackage(t, packages.Items[0], a.ID)
	other := service.NewID()
	insertWorkspaceAccount(t, db, other, "other-market@example.test")
	if _, e = db.Exec(`INSERT INTO platform_members(workspace_id,user_id,role) VALUES($1,$2,'member')`, w, other); e != nil {
		t.Fatal(e)
	}
	var allowed bool
	if e = db.QueryRow(`SELECT platform_agent_web_allowed($1,$2,$3)`, w, a.ID, other).Scan(&allowed); e != nil || !allowed {
		t.Fatal("workspace member should be able to use the new employee", e)
	}
	reviewExec(t, db, `UPDATE platform_members SET role='admin' WHERE workspace_id=$1 AND user_id=$2`, w, other)
	b, e := s.CreateFromTemplate(ctx, w, other, req)
	if e != nil || b.ID == a.ID {
		t.Fatal("ordinary create incorrectly deduplicated", e)
	}
	assertNewAgentDefaults(t, db, w, other, b.ID)
	// A bad persisted package name fails after Agent INSERT; the whole final transaction must roll back.
	bad := testTemplateContent()
	bad.Name = "rollback"
	bad.Packages = []service.TemplatePackage{{Key: "too_long", Name: strings.Repeat("x", 101), Onboarding: "required"}}
	raw := mustMarketJSON(t, bad)
	if _, e = db.Exec(`UPDATE platform_agent_template_versions SET content=$2 WHERE id=$1`, published, raw); e != nil {
		t.Fatal(e)
	}
	var before, after int
	if e = db.QueryRow(`SELECT count(*) FROM platform_agents WHERE workspace_id=$1`, w).Scan(&before); e != nil {
		t.Fatal(e)
	}
	if _, e = s.CreateFromTemplate(ctx, w, owner, req); e == nil {
		t.Fatal("expected database package constraint failure")
	}
	if e = db.QueryRow(`SELECT count(*) FROM platform_agents WHERE workspace_id=$1`, w).Scan(&after); e != nil || before != after {
		t.Fatal("partial employee committed", before, after, e)
	}
	_ = draft
}
func TestPlatformAdminBootstrapConcurrentAndDelegation(t *testing.T) {
	db := workspaceTestDB(t)
	s := &service.AuthService{Store: &AuthStore{DB: db}}
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, email := range []string{"first@example.test", "second@example.test"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, e := s.RegisterWithPassword(ctx, email, "User", "long-test-password", "test")
			errs <- e
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	var count int
	var actor, target string
	if e := db.QueryRow(`SELECT count(*) FROM platform_accounts WHERE is_platform_admin`).Scan(&count); e != nil || count != 1 {
		t.Fatal("bootstrap race", count, e)
	}
	if e := db.QueryRow(`SELECT id FROM platform_accounts WHERE is_platform_admin`).Scan(&actor); e != nil {
		t.Fatal(e)
	}
	if e := db.QueryRow(`SELECT id FROM platform_accounts WHERE NOT is_platform_admin`).Scan(&target); e != nil {
		t.Fatal(e)
	}
	admins := &service.AdminService{Store: &AdminStore{DB: db}}
	if _, e := admins.Set(ctx, actor, actor, service.AdminPatch{Version: 2, IsPlatformAdmin: false}); e == nil {
		t.Fatal("removed final admin")
	}
	if _, e := admins.Set(ctx, target, target, service.AdminPatch{Version: 1, IsPlatformAdmin: true}); e == nil {
		t.Fatal("self elevation")
	}
	promoted, e := admins.Set(ctx, actor, target, service.AdminPatch{Version: 1, IsPlatformAdmin: true})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = admins.Set(ctx, target, actor, service.AdminPatch{Version: 2, IsPlatformAdmin: false}); e != nil {
		t.Fatal(e)
	}
	if _, e = admins.Set(ctx, actor, target, service.AdminPatch{Version: promoted.Version, IsPlatformAdmin: false}); e == nil {
		t.Fatal("revoked admin reused privilege")
	}
}
func TestTemplateHTTPRejectsSelectionAndUsesPublishedVersion(t *testing.T) {
	db, models, w, owner := modelFixture(t)
	model := saveTestModel(t, models, w, owner)
	ctx := context.Background()
	auth := &service.AuthService{Store: &AuthStore{DB: db}}
	token := "market-test-session"
	if e := (&AuthStore{DB: db}).CreateSession(ctx, service.HashToken(token), owner, time.Now().Add(24*time.Hour)); e != nil {
		t.Fatal(e)
	}
	templates := &service.TemplateService{Store: &TemplateStore{DB: db}}
	h := (&controller.Server{Auth: auth, Templates: templates, Agents: &service.AgentService{Store: NewAgentStore(db)}}).Handler()
	request := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("X-CSRF-Protection", "1")
		r.AddCookie(&http.Cookie{Name: "kiko_session", Value: token})
		out := httptest.NewRecorder()
		h.ServeHTTP(out, r)
		return out
	}
	body := mustMarketJSON(t, map[string]any{"content": testTemplateContent()})
	created := request("POST", "/api/v1/workspaces/"+w+"/agent-templates", string(body))
	if created.Code != 201 {
		t.Fatal(created.Code, created.Body)
	}
	var v service.AgentTemplate
	if e := json.Unmarshal(created.Body.Bytes(), &v); e != nil {
		t.Fatal(e)
	}
	publish := mustMarketJSON(t, service.TemplatePublish{Version: v.Version, TemplateVersionID: v.LatestVersionID})
	if res := request("POST", "/api/v1/workspaces/"+w+"/agent-templates/"+v.ID+"/publish", string(publish)); res.Code != 200 {
		t.Fatal(res.Code, res.Body)
	}
	req := map[string]any{"template_id": v.ID, "template_version_id": v.LatestVersionID, "name": "HTTP created", "model_profile_id": model.ID, "parameters": map[string]any{"project": "支付"}}
	req["selected_capabilities"] = []string{}
	b := mustMarketJSON(t, req)
	if res := request("POST", "/api/v1/workspaces/"+w+"/agents/from-template", string(b)); res.Code != 400 {
		t.Fatal("accepted capability selection", res.Code)
	}
	delete(req, "selected_capabilities")
	b = mustMarketJSON(t, req)
	if res := request("POST", "/api/v1/workspaces/"+w+"/agents/from-template", string(b)); res.Code != 201 {
		t.Fatal(res.Code, res.Body)
	}
}

func mustMarketJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return b
}

func TestEmployeeScopeProtectsAdmissionQueuedInputAndRuntime(t *testing.T) {
	db, s, _, w, owner, agent, _ := managerFixture(t)
	ctx := context.Background()
	other := service.NewID()
	insertWorkspaceAccount(t, db, other, "scope-other@example.test")
	if _, e := db.Exec(`INSERT INTO platform_members(workspace_id,user_id,role) VALUES($1,$2,'member')`, w, other); e != nil {
		t.Fatal(e)
	}
	agents := &service.AgentService{Store: NewAgentStore(db)}
	current, e := agents.Get(ctx, w, owner, agent)
	if e != nil {
		t.Fatal(e)
	}
	usage := &service.AgentUsageService{Store: &AgentUsageStore{DB: db}}
	policy, e := usage.Get(ctx, w, owner, agent)
	if e != nil {
		t.Fatal(e)
	}
	policy.UsageConfig.Access = service.DefaultAgentUsage(owner).Access
	policy, e = usage.Update(ctx, w, owner, agent, policy)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ResolveWebConversation(ctx, w, other, agent); e == nil {
		t.Fatal("private employee admitted another member")
	}
	c, e := s.ResolveWebConversation(ctx, w, owner, agent)
	if e != nil {
		t.Fatal(e)
	}
	chat, _, e := s.CreateSession(ctx, w, owner, "", manager.CreateSession{ConversationID: c.ID, Mode: "chat"})
	if e != nil {
		t.Fatal(e)
	}
	input, _, e := s.SubmitInput(ctx, w, owner, chat.ID, "before-revoke", "queued work")
	if e != nil {
		t.Fatal(e)
	}
	policy.UsageConfig.Access = service.DefaultAgentUsage(other).Access
	_, e = usage.Update(ctx, w, owner, agent, policy)
	if e != nil {
		t.Fatal(e)
	}
	var allowed bool
	if e = db.QueryRow(`SELECT manager_input_actor_allowed($1)`, input.ID).Scan(&allowed); e != nil || allowed {
		t.Fatal("queued input retained revoked permission", e)
	}
	if _, _, e = s.SubmitInput(ctx, w, owner, chat.ID, "after-revoke", "no"); e == nil {
		t.Fatal("revoked member submitted input")
	}
	modelService := &service.ModelService{Store: NewModelStore(db), Secret: []byte("test-only-secret-test-only-secret")}
	if _, e = modelService.RuntimeModelKey(service.WithWebEmployee(ctx, agent), w, owner, current.ModelProfileID, "unused", "unused", "unused"); e == nil {
		t.Fatal("runtime credential path bypassed revoked employee access")
	}
}

func TestOfficialTemplateImportsFixedAssetsAndReusesMapping(t *testing.T) {
	db, models, source, actor := modelFixture(t)
	ctx := context.Background()
	assets, e := skillasset.NewStore(ctx, skillasset.Config{LocalRoot: t.TempDir()})
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if e := assets.Close(); e != nil {
			t.Error(e)
		}
	}()
	skills := &service.SkillService{Store: NewSkillStore(db), Assets: assets}
	imp, e := skills.Import(ctx, source, actor, service.SkillImportRequest{Source: "manual", Name: "weekly", Description: "Weekly", Body: "Use real sources."})
	if e != nil {
		t.Fatal(e)
	}
	skill, e := skills.Publish(ctx, source, actor, "", service.SkillPublish{ImportID: imp.ID, Name: "Weekly"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE platform_accounts SET is_platform_admin=true WHERE id=$1`, actor); e != nil {
		t.Fatal(e)
	}
	catalog := ResourceCatalog{DB: db, Assets: assets}
	official, e := catalog.Publish(ctx, actor, "skill", "", "", service.ResourcePublication{SourceWorkspaceID: source, ResourceID: skill.ID, VersionID: skill.CurrentVersionID})
	if e != nil {
		t.Fatal(e)
	}
	global := official.(service.Skill)
	s := &service.TemplateService{Store: &TemplateStore{DB: db}, Assets: assets}
	c := testTemplateContent()
	c.Resources = []service.TemplateResource{{Key: "weekly", Kind: "skill", WorkspaceID: source, ID: skill.ID, VersionID: skill.CurrentVersionID}}
	c.Packages = []service.TemplatePackage{{Key: "weekly", Name: "整理周报", Onboarding: "required", Skills: []service.TemplatePin{{ResourceKey: "weekly"}}, Plugins: []service.TemplatePluginPin{}}}
	c.Resources = append(c.Resources, service.TemplateResource{Key: "global", Kind: "skill", ID: global.ID, VersionID: global.CurrentVersionID})
	c.Packages[0].Skills = append(c.Packages[0].Skills, service.TemplatePin{ResourceKey: "global"})
	c.Examples[0].PackageKeys = []string{"weekly"}
	v, e := s.Create(ctx, "", actor, c)
	if e != nil {
		t.Fatal(e)
	}
	v, e = s.Publish(ctx, "", actor, v.ID, service.TemplatePublish{Version: v.Version, TemplateVersionID: v.LatestVersionID})
	if e != nil {
		t.Fatal(e)
	}
	public, e := s.Get(ctx, "", actor, v.ID, false)
	if e != nil || public.Content.Resources[0].ID != "" || public.Content.Resources[0].WorkspaceID != "" {
		t.Fatal("private reference exposed", public, e)
	}
	ws, e := (&service.WorkspaceService{Store: NewWorkspaceStore(db)}).Create(ctx, actor, "Target", "")
	if e != nil {
		t.Fatal(e)
	}
	model := saveTestModel(t, models, ws.ID, actor)
	req := service.TemplateCreation{TemplateID: v.ID, TemplateVersionID: v.PublishedVersionID, Name: "Imported", ModelProfileID: model.ID, Parameters: map[string]any{"project": "Target"}}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := s.CreateFromTemplate(ctx, ws.ID, actor, req); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	var imports, employees int
	if e = db.QueryRow(`SELECT count(*) FROM platform_agent_template_resource_imports WHERE workspace_id=$1`, ws.ID).Scan(&imports); e != nil || imports != 1 {
		t.Fatal("duplicate imports", imports, e)
	}
	if e = db.QueryRow(`SELECT count(*) FROM platform_agents WHERE workspace_id=$1`, ws.ID).Scan(&employees); e != nil || employees != 2 {
		t.Fatal("employees missing", employees, e)
	}
	var bindings int
	if e = db.QueryRow("SELECT count(*) FROM platform_capability_package_skills WHERE workspace_id=$1 AND skill_id=$2 AND version_id=$3", ws.ID, global.ID, global.CurrentVersionID).Scan(&bindings); e != nil || bindings != 2 {
		t.Fatal("official pins not shared", bindings, e)
	}
	var targetID, pin string
	if e = db.QueryRow(`SELECT skill_id,skill_version_id FROM platform_agent_template_resource_imports WHERE workspace_id=$1`, ws.ID).Scan(&targetID, &pin); e != nil {
		t.Fatal(e)
	}
	if _, e = skills.File(ctx, ws.ID, actor, targetID, pin, "SKILL.md"); e != nil {
		t.Fatal("target files missing", e)
	}
	if e = skills.Cleanup(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = skills.File(ctx, ws.ID, actor, targetID, pin, "SKILL.md"); e != nil {
		t.Fatal("cleanup removed active file", e)
	}
	if _, e = db.Exec(`UPDATE platform_skills SET enabled=false WHERE id=$1`, targetID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.CreateFromTemplate(ctx, ws.ID, actor, req); e == nil {
		t.Fatal("disabled imported asset revived")
	}
}
