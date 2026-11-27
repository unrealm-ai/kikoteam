package repository

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/controller"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func employeeBindings(t *testing.T, db *sql.DB, workspace string) string {
	t.Helper()
	var snapshot string
	err := db.QueryRow(`SELECT jsonb_build_object(
 'packages',(SELECT jsonb_agg(to_jsonb(p) ORDER BY p.id) FROM platform_capability_packages p WHERE workspace_id=$1),
 'skills',(SELECT jsonb_agg(to_jsonb(s) ORDER BY package_id,skill_id) FROM platform_capability_package_skills s WHERE workspace_id=$1),
 'plugins',(SELECT jsonb_agg(to_jsonb(p) ORDER BY package_id,plugin_id) FROM platform_capability_package_plugins p WHERE workspace_id=$1),
 'accounts',(SELECT jsonb_agg(jsonb_build_array(id,plugin_version_id,version,enabled,validated,tools) ORDER BY id) FROM platform_plugin_connections WHERE workspace_id=$1))::text`, workspace).Scan(&snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestBuiltinResourceCardsHTTP(t *testing.T) {
	ctx := t.Context()
	db, models, w, owner := modelFixture(t)
	rt := runtimeCapabilityServices(t, db)
	catalog, err := service.BuiltinCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if err = SyncCatalog(ctx, db, catalog, rt.Skills.Assets); err != nil {
		t.Fatal(err)
	}
	model := saveTestModel(t, models, w, owner)
	templates := &service.TemplateService{Store: &TemplateStore{DB: db}, Assets: rt.Skills.Assets}
	employee := createCatalogEmployee(t, ctx, templates, w, owner, model.ID, "行情简报员", true)
	before := employeeBindings(t, db, w)
	auth := &AuthStore{DB: db}
	const session = "resource-card-test"
	if err = auth.CreateSession(ctx, service.HashToken(session), owner, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	handler := (&controller.Server{Auth: &service.AuthService{Store: auth}, Skills: rt.Skills, Plugins: rt.Plugins, Catalog: &ResourceCatalog{DB: db}}).Handler()
	type entry struct {
		ID          string
		Card        *service.ResourceCard
		Count       int    `json:"assigned_agent_count"`
		SharedState string `json:"shared_connection_state"`
	}
	read := func(path string) ([]entry, int) {
		t.Helper()
		req := httptest.NewRequest("GET", "/api/v1"+path, nil)
		req.AddCookie(&http.Cookie{Name: "kiko_session", Value: session})
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, req)
		var result struct {
			Items []entry
			Total int
		}
		if out.Code != 200 {
			t.Fatalf("%s: %d %s", path, out.Code, out.Body.String())
		}
		if err := json.Unmarshal(out.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result.Items, result.Total
	}
	base := "/workspaces/" + w
	for kind, want := range map[string]int{"skills": len(catalog.Skills), "plugins": len(catalog.Plugins)} {
		items, total := read(base + "/" + kind + "?scope=official")
		if total != want || len(items) != want {
			t.Fatalf("%s: %d", kind, total)
		}
		for _, item := range items {
			if item.Card == nil || item.Card.SummaryZH == "" || item.Card.SummaryEN == "" || item.Card.SummaryZH == item.Card.SummaryEN || item.Card.Highlights == "" {
				t.Fatalf("incomplete card for %s", item.ID)
			}
		}
	}
	for prefix, state := range map[string]string{base + "/plugins?scope=all&": "missing", "/official-plugins?": ""} {
		items, total := read(prefix + "q=Authorized%20Repositories&page_size=1")
		if total != 1 || len(items) != 1 || items[0].ID != service.CatalogID("plugin", "github") || items[0].SharedState != state {
			t.Fatalf("unexpected matches or shared state on %s", prefix)
		}
	}
	for kind, key := range map[string]string{"plugin": "tavily", "skill": "tavily-search"} {
		items, total := read(base + "/" + kind + "s?scope=official&q=Tavily")
		if total != 1 || len(items) != 1 || items[0].ID != service.CatalogID(kind, key) || items[0].Card == nil || items[0].Card.Category != "research" {
			t.Fatalf("Tavily %s missing from official search", kind)
		}
	}
	items, total := read(base + "/skills?scope=all&q=market-close")
	if total != 1 || len(items) != 1 || items[0].Count != 1 {
		t.Fatal("missing pinned reporter skill")
	}
	items, total = read(base + "/plugins?scope=official&q=timestamps")
	if total != 1 || len(items) != 1 || items[0].Count != 1 || items[0].SharedState != "" {
		t.Fatal("unexpected anonymous reporter plugin")
	}
	_, total = read(base + "/plugins?scope=workspace&q=timestamps")
	if total != 0 {
		t.Fatal("official match escaped workspace filter")
	}
	if err = SyncCatalog(ctx, db, catalog, rt.Skills.Assets); err != nil {
		t.Fatal(err)
	}
	if employeeBindings(t, db, w) != before {
		t.Fatal("catalog reads or reinstallation changed existing employee bindings or tool grants")
	}
	// A private account should never make the catalog claim a shared connection is ready.
	createConnection := func(agent any) string {
		t.Helper()
		id := service.NewID()
		_, err := db.Exec(`INSERT INTO platform_plugin_connections(id,workspace_id,agent_id,plugin_id,plugin_version_id,name,version,enabled,validated,credential,validated_at,auth_method)
 SELECT $1,$2,$3,p.id,p.current_version_id,'test',1,true,true,''::bytea,now(),'bearer' FROM platform_plugins p WHERE p.builtin_key='github'`, id, w, agent)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	checkState := func(want string) {
		t.Helper()
		items, _ := read(base + "/plugins?scope=official&q=authorized%20repositories")
		if len(items) != 1 || items[0].SharedState != want {
			t.Fatalf("expected shared state %s", want)
		}
	}
	createConnection(employee.ID)
	checkState("missing")
	shared := createConnection(nil)
	checkState("connected")
	if _, err = db.Exec("UPDATE platform_plugin_connections SET enabled=false,validated=false WHERE id=$1", shared); err != nil {
		t.Fatal(err)
	}
	checkState("revoked")
}
