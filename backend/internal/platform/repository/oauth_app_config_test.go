package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/controller"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/integration"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

const testGitHubRedirect = "https://kiko.example/api/v1/auth/callbacks/plugin-oauth"

func TestGitHubOAuthAppAdminHTTPStorageAndDisable(t *testing.T) {
	db, _, _, _, actor, _, cookie := managerFixture(t)
	ctx := context.Background()
	s := &service.OAuthAppService{Store: &OAuthAppStore{DB: db}, Secret: []byte(strings.Repeat("k", 40)),
		RedirectURI: testGitHubRedirect}
	h := (&controller.Server{OAuthApps: s, Auth: &service.AuthService{Store: &AuthStore{DB: db}}, Origin: "https://kiko.example"}).Handler()
	do := func(method, body string, auth, csrf bool, want int) service.OAuthAppStatus {
		t.Helper()
		r := httptest.NewRequest(method, "/api/v1/platform/oauth-apps/github", strings.NewReader(body))
		if auth {
			r.Header.Set("Cookie", cookie)
		}
		if csrf {
			r.Header.Set("X-CSRF-Protection", "1")
			r.Header.Set("Origin", "https://kiko.example")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "private-credential") {
			t.Fatalf("HTTP status=%d wanted=%d; unexpected cache or response metadata", w.Code, want)
		}
		var out service.OAuthAppStatus
		if want == 200 {
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
		}
		return out
	}
	do("GET", "", false, false, 401)
	do("GET", "", true, false, 403) // Workspace owner is not a platform admin.
	reviewExec(t, db, `UPDATE platform_accounts SET is_platform_admin=true WHERE id=$1`, actor)
	got := do("GET", "", true, false, 200)
	if got.Version != 0 || got.Source != "unset" || got.Enabled || got.HasClientSecret || got.ClientID != "" || got.RedirectURI != testGitHubRedirect {
		t.Fatal("unset settings unexpectedly enabled", got)
	}
	do("PATCH", `{"expected_version":0,"enabled":false}`, true, false, 403)
	do("PATCH", `{"expected_version":0,"enabled":true,"clear_client_secret":true}`, true, true, 400)
	do("PATCH", `{"enabled":true}`, true, true, 400)
	do("PATCH", `{"expected_version":0,"enabled":true}`, true, true, 400)
	got = do("PATCH", `{"expected_version":0,"enabled":true,"client_id":"fixture-app","client_secret":"fixture-private-credential"}`, true, true, 200)
	if got.Version != 1 || got.Source != "database" || !got.Enabled || !got.HasClientSecret {
		t.Fatal("database settings not written", got)
	}
	var ciphertext []byte
	if err := db.QueryRowContext(ctx, `SELECT client_secret FROM platform_oauth_app_configs WHERE provider='github' AND purpose='plugin'`).Scan(&ciphertext); err != nil || len(ciphertext) < 28 || bytes.Contains(ciphertext, []byte("fixture-private-credential")) {
		t.Fatal("missing encryption", err)
	}
	do("PATCH", `{"expected_version":0,"enabled":false}`, true, true, 409)
	do("PATCH", `{"expected_version":1,"enabled":true,"client_id":"different-app"}`, true, true, 400)
	got = do("PATCH", `{"expected_version":1,"enabled":false,"client_id":"different-app"}`, true, true, 200)
	if got.Version != 2 || got.Source != "database" || got.Enabled || got.HasClientSecret || got.ClientID != "different-app" {
		t.Fatal("explicit disabled rotation reused credentials or fell back into the environment", got)
	}
	got = do("PATCH", `{"expected_version":2,"enabled":false,"clear_client_secret":true}`, true, true, 200)
	if got.Version != 3 || got.HasClientSecret {
		t.Fatal("explicit clear failed", got)
	}
}

type githubConfigTransport struct {
	fixture      *githubFixture
	exchangeHook func()
	requests     int
}

func (f *githubConfigTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f.requests++
	if r.URL.String() == githubIssuer+"/access_token" && f.exchangeHook != nil {
		f.exchangeHook()
	}
	return f.fixture.RoundTrip(r)
}

func TestGitHubOAuthAppRotationPinsAuthorization(t *testing.T) {
	db, _, w, actor := modelFixture(t)
	ctx := context.Background()
	reviewExec(t, db, `UPDATE platform_accounts SET is_platform_admin=true WHERE id=$1`, actor)
	rt := runtimeCapabilityServices(t, db)
	catalog, err := service.BuiltinCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if err = SyncCatalog(ctx, db, catalog, rt.Skills.Assets); err != nil {
		t.Fatal(err)
	}
	var id, version string
	for _, plugin := range catalog.Plugins {
		if plugin.Key == "github" {
			id, version = plugin.Ref().ID, plugin.Ref().VersionID
		}
	}
	if id == "" {
		t.Fatal("missing official GitHub plugin")
	}
	apps := &service.OAuthAppService{Store: &OAuthAppStore{DB: db}, Secret: rt.Plugins.Secret,
		RedirectURI: testGitHubRedirect}
	fixture := &githubFixture{t: t}
	wire := &githubConfigTransport{fixture: fixture}
	s := rt.Plugins
	s.OAuthApps, s.RedirectURI, s.Probe = apps, testGitHubRedirect, &integration.MCPProber{Client: &http.Client{Transport: wire}}
	req := service.PluginAccountPut{Name: "GitHub", PluginVersionID: version}
	begin := func(wantClient string) string {
		t.Helper()
		raw, e := s.AuthorizeOAuth(ctx, w, actor, "fixture-browser", "", id, req)
		if e != nil {
			t.Fatal(e)
		}
		u, e := url.Parse(raw)
		if e != nil {
			t.Fatal(e)
		}
		if u.Query().Get("client_id") != wantClient || strings.Contains(raw, "fixture-secret") {
			t.Fatal("client selection or redaction incorrect")
		}
		fixture.challenge = u.Query().Get("code_challenge")
		return u.Query().Get("state")
	}
	patch := func(v int64, enabled bool, client, secret *string) {
		t.Helper()
		if _, e := apps.UpdateGitHub(ctx, actor, service.OAuthAppPatch{ExpectedVersion: &v, Enabled: &enabled, ClientID: client, ClientSecret: secret}); e != nil {
			t.Fatal(e)
		}
	}
	finish := func(state string) (service.PluginAccount, error) {
		return s.CompleteOAuth(ctx, actor, "fixture-browser", state, "fixture-code", githubIssuer)
	}
	if _, e := s.AuthorizeOAuth(ctx, w, actor, "fixture-browser", "", id, req); oauthErrorCode(e) != "github_oauth_unconfigured" || wire.requests != 0 {
		t.Fatal("unset database configuration attempted authorization", e)
	}
	client, secret := "fixture-app", "fixture-secret"
	patch(0, true, &client, &secret)
	legacyState := begin(client)
	patch(1, true, nil, nil)
	n := wire.requests
	if _, err = finish(legacyState); oauthErrorCode(err) != "oauth_client_changed" || n != wire.requests {
		t.Fatal("stale callback reached provider or lacked clear error", err)
	}
	current := begin(client)
	wire.exchangeHook = func() { patch(2, true, nil, nil) }
	if _, err = finish(current); oauthErrorCode(err) != "oauth_client_changed" {
		t.Fatal("rotation during exchange saved the stale grant", err)
	}
	wire.exchangeHook = nil
	var accounts int
	if err = db.QueryRow(`SELECT count(*) FROM platform_plugin_connections WHERE plugin_id=$1`, id).Scan(&accounts); err != nil || accounts != 0 {
		t.Fatal("stale callback materialized a connection", err)
	}
	account, err := finish(begin(client))
	if err != nil || !account.Enabled || account.AuthMethod != "oauth" {
		t.Fatal("new attempt did not use saved app", err)
	}
	patch(3, false, nil, nil)
	n = wire.requests
	if _, err = s.AuthorizeOAuth(ctx, w, actor, "fixture-browser", "", id, req); oauthErrorCode(err) != "github_oauth_unconfigured" || n != wire.requests {
		t.Fatal("disabled app fell back or attempted provider", err)
	}
}

func oauthErrorCode(err error) string {
	var e *service.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}
