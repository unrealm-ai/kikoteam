package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type interruptedCatalogAssets struct {
	service.SkillAssets
	remaining int
}

func (s *interruptedCatalogAssets) Write(ctx context.Context, key string, b service.SkillBundle) error {
	if s.remaining == 0 {
		return errors.New("interrupted asset write")
	}
	s.remaining--
	return s.SkillAssets.Write(ctx, key, b)
}
func createCatalogEmployee(t *testing.T, ctx context.Context, templates *service.TemplateService, workspace, owner, model, name string, requiredOnly bool) service.Agent {
	t.Helper()
	page, err := templates.List(ctx, "", owner, service.TemplateQuery{Q: name, Page: 1, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Content.Name != name {
		t.Fatalf("expected published template %q", name)
	}
	template := page.Items[0]
	employee, err := templates.CreateFromTemplate(ctx, workspace, owner, service.TemplateCreation{
		TemplateID: template.ID, TemplateVersionID: template.PublishedVersionID, ModelProfileID: model, Name: name, RequiredPackagesOnly: requiredOnly,
	})
	if err != nil {
		t.Fatal(err)
	}
	return employee
}

func TestCatalogRestartAndOptInMarketReporter(t *testing.T) {
	ctx := context.Background()
	db, models, w, owner := modelFixture(t)
	assets := runtimeCapabilityServices(t, db).Skills.Assets
	c, e := service.BuiltinCatalog()
	if e != nil {
		t.Fatal(e)
	}
	if e = SyncCatalog(ctx, db, c, &interruptedCatalogAssets{assets, 2}); e == nil {
		t.Fatal("expected interruption")
	}
	count := func(query string, args ...any) int {
		t.Helper()
		var n int
		if e := db.QueryRow(query, args...).Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	if count("select count(*) from platform_skills where builtin_key is not null") != 0 {
		t.Fatal("partial release")
	}
	if e = SyncCatalog(ctx, db, c, assets); e != nil {
		t.Fatal(e)
	}
	for tbl, want := range map[string]int{"platform_skills": len(c.Skills), "platform_plugins": len(c.Plugins)} {
		if count("select count(*) from "+tbl+" where builtin_key is not null and listed and enabled") != want {
			t.Fatal(tbl)
		}
	}
	model := saveTestModel(t, models, w, owner)
	if e = SyncCatalog(ctx, db, c, assets); e != nil {
		t.Fatal(e)
	}
	if count("select count(*) from platform_workspaces where id=$1 and starter_pending", w) != 0 || count("select count(*) from platform_agents where workspace_id=$1 and name='行情简报员'", w) != 0 {
		t.Fatal("new workspace has a pending or automatically created market reporter")
	}
	templates := &service.TemplateService{Store: &TemplateStore{DB: db}, Assets: assets}
	employee := createCatalogEmployee(t, ctx, templates, w, owner, model.ID, "行情简报员", true)
	if count("select count(*) from platform_capability_packages where workspace_id=$1 and agent_id=$2", w, employee.ID) != 3 {
		t.Fatal("expected the base package and the two required reporter packages")
	}
	assertNewAgentDefaults(t, db, w, owner, employee.ID)
	if count("select count(*) from platform_plugin_connections where workspace_id=$1 and plugin_id=$2 and enabled and validated", w, service.CatalogID("plugin", "stock")) != 1 {
		t.Fatal("public account missing")
	}
}
func TestDisableWorkspaceStartersMigration(t *testing.T) {
	ctx := t.Context()
	const previous = "000107_builtin_employee_management.sql"
	db, models, workspace, owner := modelFixtureThrough(t, previous)
	assets := runtimeCapabilityServices(t, db).Skills.Assets
	catalog, err := service.BuiltinCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if err = SyncCatalog(ctx, db, catalog, assets); err != nil {
		t.Fatal(err)
	}
	model := saveTestModel(t, models, workspace, owner)
	templates := &service.TemplateService{Store: &TemplateStore{DB: db}, Assets: assets}
	employee := createCatalogEmployee(t, ctx, templates, workspace, owner, model.ID, "行情简报员", false)
	var queued bool
	if err = db.QueryRow(`SELECT starter_pending FROM platform_workspaces WHERE id=$1`, workspace).Scan(&queued); err != nil || queued {
		t.Fatal("manual creation must consume the pending flag for older backend instances", err)
	}
	var packageCount int
	if err = db.QueryRow(`SELECT count(*) FROM platform_capability_packages WHERE agent_id=$1`, employee.ID).Scan(&packageCount); err != nil || packageCount != 4 {
		t.Fatal("full reporter template must retain the deferred package alongside the base package", packageCount, err)
	}
	before := employeeBindings(t, db, workspace)
	pending, err := (&service.WorkspaceService{Store: NewWorkspaceStore(db)}).Create(ctx, owner, "pending", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT starter_pending FROM platform_workspaces WHERE id=$1`, pending.ID).Scan(&queued); err != nil || !queued {
		t.Fatal("expected pending workspace before migration", err)
	}
	applyTestMigrations(t, ctx, db, previous, "000108_disable_workspace_starters.sql")
	var stillPending, keptEmployee bool
	if err = db.QueryRow(`SELECT starter_pending, EXISTS(SELECT FROM platform_agents WHERE id=$2 AND workspace_id=$3) FROM platform_workspaces WHERE id=$1`, pending.ID, employee.ID, workspace).Scan(&stillPending, &keptEmployee); err != nil {
		t.Fatal(err)
	}
	if stillPending || !keptEmployee || employeeBindings(t, db, workspace) != before {
		t.Fatal("upgrade must cancel pending creation without changing existing employees or bindings")
	}
}

func TestCatalogTenantClientIsolation(t *testing.T) {
	ctx := context.Background()
	db, _, w, owner := modelFixture(t)
	rt := runtimeCapabilityServices(t, db)
	catalog, e := service.BuiltinCatalog()
	if e != nil {
		t.Fatal(e)
	}
	if e = SyncCatalog(ctx, db, catalog, rt.Skills.Assets); e != nil {
		t.Fatal(e)
	}
	var version string
	for _, p := range catalog.Plugins {
		if p.Key == "fxiaoke" {
			version = p.Ref().VersionID
		}
	}
	id := service.CatalogID("plugin", "fxiaoke")
	other, e := (&service.WorkspaceService{Store: NewWorkspaceStore(db)}).Create(ctx, owner, "other", "")
	if e != nil {
		t.Fatal(e)
	}
	key := "fixture-secret"
	endpoint := "https://open.fxiaoke.com/mcp/tenant/sales"
	if e = rt.Plugins.Store.WithWorkspace(ctx, w, func(tx service.PluginTx) error {
		p, e := tx.GetVersion(ctx, id, version)
		if e != nil {
			return e
		}
		p.Endpoint = endpoint
		if e = rt.Plugins.ConfigureOAuth(w, &p, &service.OAuthClientInput{Resource: endpoint, Issuer: "https://open.fxiaoke.com", ClientID: "sales", ClientSecret: key}); e != nil {
			return e
		}
		p.OAuth.ReadTools = []string{"customer_read"}
		return tx.SaveTenantOAuth(ctx, p)
	}); e != nil {
		t.Fatal(e)
	}
	for _, ws := range []string{w, other.ID} {
		if e = rt.Plugins.Store.WithWorkspace(ctx, ws, func(tx service.PluginTx) error {
			p, e := tx.GetVersion(ctx, id, version)
			if e != nil {
				return e
			}
			if ws == w {
				if p.Endpoint != endpoint || p.OAuth.ClientID != "sales" || len(p.OAuth.ReadTools) != 1 {
					t.Fatal("tenant metadata missing")
				}
				cipher, e := tx.ClientSecret(ctx, p.OAuth.ID)
				if e != nil {
					return e
				}
				if len(cipher) == 0 || string(cipher) == key {
					t.Fatal("unsealed tenant client")
				}
			} else if p.Endpoint == endpoint || p.OAuth.ID != "" {
				t.Fatal("cross-workspace configuration")
			}
			return nil
		}); e != nil {
			t.Fatal(e)
		}
	}
}
