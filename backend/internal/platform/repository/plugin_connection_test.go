package repository

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/integration"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestSharedOAuthRotationAndRevocation(t *testing.T) {
	db, models, w, actor := modelFixture(t)
	m := saveTestModel(t, models, w, actor)
	ctx := context.Background()
	agents := &service.AgentService{Store: NewAgentStore(db)}
	employee, e := agents.Create(ctx, w, actor, service.AgentCreate{Name: "Reviewer", ModelProfileID: m.ID})
	if e != nil {
		t.Fatal(e)
	}
	f := newOAuthLedger(t)
	s := &service.PluginService{Store: NewPluginStore(db), Probe: &integration.MCPProber{Client: f.Client()}, Secret: []byte(strings.Repeat("x", 40)), RedirectURI: "https://kiko.example.test/api/v1/auth/callbacks/plugin-oauth"}
	p, e := s.Create(ctx, w, actor, service.PluginCreate{Name: "ledger", Endpoint: f.URL + "/ledger", AuthType: "oauth", OAuth: &service.OAuthClientInput{Issuer: f.URL, ClientID: "kiko", ClientSecret: "fixture-app-secret", Scopes: "ledger.read"}})
	if e != nil {
		t.Fatal(e)
	}
	consent := func(employee, account string) (string, string) {
		t.Helper()
		authorization, e := s.AuthorizeOAuth(ctx, w, actor, "browser", employee, p.ID, service.PluginAccountPut{Name: account, PluginVersionID: p.VersionID})
		if e != nil {
			t.Fatal(e)
		}
		req, e := http.NewRequestWithContext(ctx, "GET", authorization, nil)
		if e != nil {
			t.Fatal(e)
		}
		req.AddCookie(&http.Cookie{Name: "ledger_login", Value: account})
		browser := *f.Client()
		browser.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		res, e := browser.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		if e = res.Body.Close(); e != nil {
			t.Fatal(e)
		}
		redirect, e := url.Parse(res.Header.Get("Location"))
		if e != nil {
			t.Fatal(e)
		}
		return redirect.Query().Get("state"), redirect.Query().Get("code")
	}
	state, code := consent("", "team")
	if _, e = s.CompleteOAuth(ctx, actor, "another-session", state, code, f.URL); e == nil {
		t.Fatal("foreign browser consumed consent")
	}
	a, e := s.CompleteOAuth(ctx, actor, "browser", state, code, f.URL)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.CompleteOAuth(ctx, actor, "browser", state, code, f.URL); e == nil {
		t.Fatal("callback replay accepted")
	}
	pin := service.RuntimeAccountPin{Source: "workspace", PluginID: p.ID, ID: a.ID, Version: a.Version, PluginVersionID: p.VersionID}
	reviewExec(t, db, "UPDATE platform_plugin_connections SET expires_at=now() WHERE id=$1", a.ID)
	var wait sync.WaitGroup
	errs := make(chan error, 6)
	for range 6 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := s.RuntimePlugin(ctx, w, actor, employee.ID, pin, p.VersionID)
			errs <- err
		}()
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	_, n, _ := f.counts()
	if n != 1 {
		t.Fatalf("concurrent refresh exchanged %d grants", n)
	}
	stale, e := s.ConnectionSource(ctx, w, actor, employee.ID, p.ID, p.VersionID, "", nil)
	if e != nil || stale.Account == nil || stale.Account.Version != a.Version {
		t.Fatal("refresh changed grant", e)
	}
	// Start an authorization, then change source while the browser is away.
	state, code = consent(employee.ID, "private")
	if _, e = s.ConnectionSource(ctx, w, actor, employee.ID, p.ID, p.VersionID, "agent", &stale.Revision); e != nil {
		t.Fatal(e)
	}
	if _, e = s.CompleteOAuth(ctx, actor, "browser", state, code, f.URL); e == nil {
		t.Fatal("stale callback overwrote source")
	}
	if _, e = s.RuntimePlugin(ctx, w, actor, employee.ID, pin, p.VersionID); e == nil {
		t.Fatal("old shared pin accepted")
	}
	state, code = consent(employee.ID, "private")
	private, e := s.CompleteOAuth(ctx, actor, "browser", state, code, f.URL)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.RevokeAccount(ctx, w, actor, employee.ID, p.ID, private.Version, p.VersionID); e != nil {
		t.Fatal(e)
	}
	current, e := s.ConnectionSource(ctx, w, actor, employee.ID, p.ID, p.VersionID, "", nil)
	if e != nil || current.Source != "agent" || current.Account == nil || current.Account.Enabled || current.Account.ID != private.ID {
		t.Fatal("revocation fell back to shared", e)
	}
	f.mu.Lock()
	for _, who := range f.refresh {
		if who == "private" {
			t.Error("private refresh token still valid upstream")
		}
	}
	f.mu.Unlock()
	if _, e = s.ConnectionSource(ctx, w, actor, employee.ID, p.ID, p.VersionID, "workspace", &current.Revision); e != nil {
		t.Fatal(e)
	}
	if _, e = s.RuntimePlugin(ctx, w, actor, employee.ID, pin, p.VersionID); e == nil {
		t.Fatal("old shared task regained access after switching back")
	}
	other, e := (&service.WorkspaceService{Store: NewWorkspaceStore(db)}).Create(ctx, actor, "Another", "")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.GetAccount(ctx, other.ID, actor, "", p.ID, p.VersionID); !errors.Is(e, service.ErrWorkspaceMissing) {
		t.Fatal("shared connection escaped workspace", e)
	}
	// Revoking at the issuer must persist the need to reconnect locally.
	f.mu.Lock()
	clear(f.refresh)
	f.mu.Unlock()
	reviewExec(t, db, "UPDATE platform_plugin_connections SET expires_at=now() WHERE id=$1", a.ID)
	current, e = s.ConnectionSource(ctx, w, actor, employee.ID, p.ID, p.VersionID, "", nil)
	if e != nil {
		t.Fatal(e)
	}
	pin.SourceRevision = current.Revision
	if _, e = s.RuntimePlugin(ctx, w, actor, employee.ID, pin, p.VersionID); e == nil {
		t.Fatal("revoked refresh succeeded")
	}
	shared, e := s.GetAccount(ctx, w, actor, "", p.ID, p.VersionID)
	if e != nil || shared.Validated {
		t.Fatal("invalid grant not persisted", e)
	}
}

func TestPluginConnectionMigrationKeepsRevokedPrivate(t *testing.T) {
	db, models, w, actor := modelFixtureThrough(t, "000077_official_resources.sql")
	model := saveTestModel(t, models, w, actor)
	agent, p, v, a := service.NewID(), service.NewID(), service.NewID(), service.NewID()
	reviewExec(t, db, "INSERT INTO platform_agents(id,workspace_id,name,model_profile_id,version,config_version,admission_epoch) VALUES($1,$2,'Reviewer',$3,1,1,1)", agent, w, model.ID)
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback() //nolint:errcheck // No-op after commit.
	_, e = tx.Exec("INSERT INTO platform_plugins(id,workspace_id,name,version,current_version_id) VALUES($1,$2,'ledger',1,$3)", p, w, v)
	if e != nil {
		t.Fatal(e)
	}
	_, e = tx.Exec("INSERT INTO platform_plugin_versions(id,workspace_id,plugin_id,version,name,description,endpoint,auth_type) VALUES($1,$2,$3,1,'ledger','','https://ledger.example.test','bearer')", v, w, p)
	if e != nil {
		t.Fatal(e)
	}
	_, e = tx.Exec("INSERT INTO platform_employee_plugin_accounts(id,workspace_id,agent_id,plugin_id,plugin_version_id,name,version,enabled,validated,credential,validated_at) VALUES($1,$2,$3,$4,$5,'revoked',4,false,false,''::bytea,now())", a, w, agent, p, v)
	if e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile("../../../../event-hub/database/postgres/migrations/000078_plugin_connections.sql")
	if e != nil {
		t.Fatal(e)
	}
	reviewExec(t, db, strings.Split(string(raw), "-- +goose Down")[0])
	var owner sql.NullString
	var revision int64
	var active bool
	e = db.QueryRow("SELECT platform_plugin_connection_owner($1,$2,$3)::text,c.version,c.enabled FROM platform_plugin_connections c WHERE c.id=$4", w, agent, p, a).Scan(&owner, &revision, &active)
	if e != nil || owner.String != agent || revision != 4 || active {
		t.Fatalf("revoked legacy connection changed: %v", e)
	}
}
