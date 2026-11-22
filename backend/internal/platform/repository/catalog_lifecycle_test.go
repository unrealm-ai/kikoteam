package repository

import (
	"context"
	"errors"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"testing"
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
func TestCatalogRestartAndStarter(t *testing.T) {
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
	s := &service.TemplateService{Store: &TemplateStore{DB: db}, Assets: assets}
	if e = ProvisionCatalogStarters(ctx, db, s); e != nil {
		t.Fatal(e)
	}
	if count("select count(*) from platform_agents where workspace_id=$1", w) != 0 {
		t.Fatal("starter before model")
	}
	saveTestModel(t, models, w, owner)
	finished := make(chan error, 2)
	for range 2 {
		go func() { finished <- ProvisionCatalogStarters(ctx, db, s) }()
	}
	for range 2 {
		if e := <-finished; e != nil {
			t.Fatal(e)
		}
	}
	if count("select count(*) from platform_agents where workspace_id=$1", w) != 1 || count("select count(*) from platform_capability_packages where workspace_id=$1", w) != 2 {
		t.Fatal("unexpected starter configuration")
	}
	if count("select count(*) from platform_plugin_connections where workspace_id=$1 and plugin_id=$2 and enabled and validated", w, service.CatalogID("plugin", "stock")) != 1 {
		t.Fatal("public account missing")
	}
	if count("select count(*) from platform_workspaces where id=$1 and starter_pending", w) != 0 {
		t.Fatal("starter flag unconsumed")
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
