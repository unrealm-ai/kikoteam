package repository

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/integration"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

const githubIssuer = "https://github.com/login/oauth"
const githubMCP = "https://api.githubcopilot.com/mcp/readonly"

type githubFixture struct {
	t         *testing.T
	challenge string
	verified  bool
}

func (f *githubFixture) RoundTrip(r *http.Request) (*http.Response, error) {
	var body string
	switch r.URL.String() {
	case "https://api.githubcopilot.com/.well-known/oauth-protected-resource/mcp/readonly":
		body = `{"resource":"https://api.githubcopilot.com/mcp/readonly","authorization_servers":["https://github.com/login/oauth"]}`
	case "https://github.com/.well-known/oauth-authorization-server/login/oauth":
		body = `{"issuer":"https://github.com/login/oauth","authorization_endpoint":"https://github.com/login/oauth/authorize","token_endpoint":"https://github.com/login/oauth/access_token","code_challenge_methods_supported":["S256"],"authorization_response_iss_parameter_supported":true}`
	case githubIssuer + "/access_token":
		if err := r.ParseForm(); err != nil {
			return nil, err
		}
		if r.PostForm.Get("client_id") != "fixture-app" || r.PostForm.Get("client_secret") != "fixture-secret" || r.Header.Get("Authorization") != "" || r.Header.Get("Accept") != "application/json" {
			f.t.Fatal("unexpected GitHub token request credentials or response format")
		}
		switch r.PostForm.Get("grant_type") {
		case "authorization_code":
			hash := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
			if base64.RawURLEncoding.EncodeToString(hash[:]) != f.challenge {
				f.t.Fatal("authorization code or PKCE mismatch")
			}
			body = `{"access_token":"access-1","refresh_token":"refresh-1","expires_in":28800,"token_type":"bearer","scope":""}`
			if r.PostForm.Get("code") == "probe-denied" {
				body = `{"access_token":"denied","token_type":"bearer"}`
			}
		case "refresh_token":
			if r.PostForm.Get("refresh_token") == "refresh-1" {
				body = `{"access_token":"access-2","refresh_token":"refresh-2","expires_in":28800,"token_type":"bearer"}`
			} else {
				body = `{"error":"bad_refresh_token"}`
			}
		default:
			f.t.Fatal("unexpected grant")
		}
	case githubMCP:
		switch r.Header.Get("Authorization") {
		case "Bearer access-1", "Bearer shared-pat", "Bearer denied":
		default:
			return &http.Response{StatusCode: http.StatusUnauthorized, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		var req struct {
			ID     int
			Method string
			Params struct{ Name string }
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return nil, err
		}
		var result string
		switch req.Method {
		case "initialize":
			result = `{"protocolVersion":"2025-03-26","capabilities":{"tools":{}}}`
		case "notifications/initialized":
			result = "{}"
		case "tools/list":
			result = `{"tools":[{"name":"list_commits","inputSchema":{"type":"object"}}]}`
		case "tools/call":
			if req.Params.Name != "get_me" {
				f.t.Fatal("unexpected verification tool")
			}
			switch r.Header.Get("Authorization") {
			case "Bearer denied":
				result = `{"isError":true,"content":[{"type":"text","text":"access denied"}]}`
			case "Bearer shared-pat":
				result = `{"content":[{"type":"text","text":"team-account"}]}`
			default:
				f.verified = true
				result = `{"content":[{"type":"text","text":"oauth-account"}]}`
			}
		default:
			f.t.Fatal("unexpected MCP request")
		}
		body = fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":%s}`, req.ID, result)
	default:
		f.t.Fatal("unexpected destination", r.URL.Host, r.URL.Path)
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
}

func TestCatalogGitHubConnectionUpgrade(t *testing.T) {
	ctx := context.Background()
	db, models, w, actor := modelFixtureThrough(t, "000081_employee_capability_versions.sql")
	rt := runtimeCapabilityServices(t, db)
	catalog, err := service.BuiltinCatalog()
	if err != nil {
		t.Fatal(err)
	}
	var current service.CatalogPlugin
	for _, p := range catalog.Plugins {
		if p.Key == "github" {
			current = p
		}
	}
	old := current
	old.Auth, old.Issuer, old.Scopes = "bearer", "", ""
	old.Description = "限定仓库的只读 PAT。读取 PR、Issue、差异和提交记录。"
	priorOAuth := current
	priorOAuth.Auth, priorOAuth.Issuer, priorOAuth.Scopes = "oauth", githubIssuer, "offline_access"
	priorOAuth.Description = "通过 GitHub 网页授权，只读访问获准仓库的 PR、Issue、差异和提交记录。"
	publish := func(revision int64, definitions ...service.CatalogPlugin) {
		t.Helper()
		if err := SyncCatalog(ctx, db, service.TemplateCatalog{Revision: revision, Plugins: definitions}, rt.Skills.Assets); err != nil {
			t.Fatal(err)
		}
	}
	publish(3, old)
	model := saveTestModel(t, models, w, actor)
	s := rt.Plugins
	s.RedirectURI = "https://kiko.example/api/v1/auth/callbacks/plugin-oauth"
	id, version := old.Ref().ID, old.Ref().VersionID
	var employees []string
	for _, name := range []string{"Reviewer", "Analyst"} {
		a, err := (&service.AgentService{Store: NewAgentStore(db)}).Create(ctx, w, actor, service.AgentCreate{Name: name, ModelProfileID: model.ID})
		if err != nil {
			t.Fatal(err)
		}
		_, err = (&service.PackageService{Store: NewPackageStore(db)}).Create(ctx, w, actor, a.ID, service.PackageCreate{Name: "GitHub", Scope: &service.PackageScope{Mode: "whitelist_with_blacklist", AllowMemberIDs: []string{actor}}, Plugins: []service.PackagePlugin{{PluginID: id, VersionID: version}}})
		if err != nil {
			t.Fatal(err)
		}
		employees = append(employees, a.ID)
	}
	preview := old
	preview.Key, preview.Name = "github_pat", "GitHub PAT"
	publish(4, priorOAuth, preview)
	seed := func(def service.CatalogPlugin, secret string) string {
		t.Helper()
		connection := service.NewID()
		resource := def.Ref()
		// Use the pre-082 envelope and AAD, independent of the current writer.
		key := sha256.Sum256(append([]byte("kiko:plugin-credential:v1:"), s.Secret...))
		block, err := aes.NewCipher(key[:])
		if err != nil {
			t.Fatal(err)
		}
		gcm, err := cipher.NewGCM(block)
		if err != nil {
			t.Fatal(err)
		}
		nonce := make([]byte, gcm.NonceSize())
		if _, err = rand.Read(nonce); err != nil {
			t.Fatal(err)
		}
		sealed := gcm.Seal(nonce, nonce, []byte(secret), []byte(w+":"+resource.ID+":"+connection+":v1"))
		reviewExec(t, db, `INSERT INTO platform_plugin_connections(id,workspace_id,plugin_id,plugin_version_id,name,version,enabled,validated,credential,validated_at)
VALUES($1,$2,$3,$4,'legacy',5,true,true,$5,now())`, connection, w, resource.ID, resource.VersionID, sealed)
		return connection
	}
	patID := seed(old, "shared-pat")
	oauthID := seed(priorOAuth, `{"access_token":"legacy-oauth","token_type":"bearer"}`)
	previewID := seed(preview, "shared-pat")
	snapshot := func() string {
		t.Helper()
		var state string
		err := db.QueryRow(`SELECT jsonb_build_array(
 (SELECT jsonb_agg(to_jsonb(c)-'auth_method' ORDER BY id) FROM platform_plugin_connections c),
 (SELECT jsonb_agg(to_jsonb(v) ORDER BY package_id,version) FROM platform_capability_package_versions v),
 (SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM platform_capability_packages p),
 (SELECT jsonb_agg(to_jsonb(b) ORDER BY package_id,plugin_id) FROM platform_capability_package_plugins b),
 (SELECT jsonb_agg(to_jsonb(s) ORDER BY agent_id,plugin_id) FROM platform_agent_plugin_connection_settings s))::text`).Scan(&state)
		if err != nil {
			t.Fatal(err)
		}
		return state
	}
	before := snapshot()
	data, err := os.ReadFile("../../../../event-hub/database/postgres/migrations/000082_plugin_connection_auth_method.sql")
	if err != nil {
		t.Fatal(err)
	}
	reviewExec(t, db, strings.Split(string(data), "-- +goose Down")[0])
	if err = SyncCatalog(ctx, db, catalog, rt.Skills.Assets); err != nil {
		t.Fatal(err)
	}
	if snapshot() != before {
		t.Fatal("upgrade changed a connection, binding or permission")
	}
	// The fixture staged migration 82 explicitly. Runtime now persists GitHub
	// app pins, so install its schema before exercising post-migration OAuth.
	data, err = os.ReadFile("../../../../event-hub/database/postgres/migrations/000090_platform_oauth_app_configs.sql")
	if err != nil {
		t.Fatal(err)
	}
	reviewExec(t, db, strings.Split(string(data), "-- +goose Down")[0])
	var count int
	if err = db.QueryRow("SELECT count(*) FROM platform_plugins WHERE builtin_key LIKE 'github%' AND listed").Scan(&count); err != nil || count != 1 {
		t.Fatal("expected one official GitHub listing", err)
	}
	previous, err := s.GetAccount(ctx, w, actor, "", preview.Ref().ID, preview.Ref().VersionID)
	if err != nil || previous.ID != previewID || !previous.Enabled || previous.AuthMethod != "bearer" {
		t.Fatal("delisting the preview removed its existing grant", err)
	}
	latest, err := s.Get(ctx, w, actor, id)
	if err != nil || latest.DefaultAuthMethod != "bearer" || !slices.Equal(latest.SupportedAuthMethods, []string{"bearer", "oauth"}) {
		t.Fatal("incorrect GitHub connection options", err)
	}
	fixture := &githubFixture{t: t}
	prober := &integration.MCPProber{Client: &http.Client{Transport: fixture}}
	s.Probe = prober
	checkAdmission := func(employee, wantIdentity string) {
		t.Helper()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		admitted, err := SnapshotCapabilities(ctx, tx, w, actor, employee)
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			t.Fatal(rollbackErr)
		}
		if err != nil {
			t.Fatal(err)
		}
		if wantIdentity == "" {
			if len(admitted.Accounts) != 0 {
				t.Fatal("revoked employee received a connection in a new task")
			}
			return
		}
		if len(admitted.Accounts) != 1 || admitted.Accounts[0].PluginVersionID != version {
			t.Fatal("task admission lost employee binding")
		}
		plugin, err := s.RuntimePlugin(ctx, w, actor, employee, admitted.Accounts[0], version)
		if err != nil {
			t.Fatal(err)
		}
		result, err := prober.CallTool(ctx, plugin, "get_me", json.RawMessage("{}"))
		if err != nil || !strings.Contains(string(result), wantIdentity) {
			t.Fatal("task used wrong upstream account", err)
		}
	}
	load := func(employee, v string) (service.ConnectionSource, service.RuntimeAccountPin) {
		t.Helper()
		src, err := s.ConnectionSource(ctx, w, actor, employee, id, v, "", nil)
		if err != nil || src.Account == nil {
			t.Fatal("expected selected account", err)
		}
		a := src.Account
		return src, service.RuntimeAccountPin{Source: src.Source, SourceRevision: src.Revision, PluginID: id, ID: a.ID, Version: a.Version, PluginVersionID: v}
	}
	checkToken := func(employee string, pin service.RuntimeAccountPin, want string) {
		t.Helper()
		got, err := s.RuntimePlugin(ctx, w, actor, employee, pin, pin.PluginVersionID)
		if want == "" {
			if err == nil {
				t.Fatal("obsolete or revoked credential used")
			}
			return
		}
		if err != nil || got.Headers["Authorization"] != "Bearer "+want {
			t.Fatal("wrong runtime identity", err)
		}
	}
	legacy, sharedPin := load(employees[0], version)
	if legacy.Account.ID != patID || legacy.Account.AuthMethod != "bearer" || legacy.Account.AgentCount != 2 {
		t.Fatal("expected two employees on the migrated PAT")
	}
	for _, e := range employees {
		checkToken(e, sharedPin, "shared-pat")
		checkAdmission(e, "team-account")
	}
	historic, pin := load(employees[0], priorOAuth.Ref().VersionID)
	if historic.Account.ID != oauthID || historic.Account.AuthMethod != "oauth" {
		t.Fatal("old OAuth method lost")
	}
	checkToken(employees[0], pin, "legacy-oauth")
	req := service.PluginAccountPut{Name: "team", PluginVersionID: version, Version: sharedPin.Version}
	if _, err := s.AuthorizeOAuth(ctx, w, actor, "browser", "", id, req); err == nil {
		t.Fatal("OAuth started without a registered client")
	}
	s.GitHubClientID, s.GitHubClientSecret = "fixture-app", "fixture-secret"
	begin := func(employee string, rev int64) string {
		t.Helper()
		link, err := s.AuthorizeOAuth(ctx, w, actor, "browser", employee, id, service.PluginAccountPut{Name: "GitHub", PluginVersionID: version, Version: rev})
		if err != nil {
			t.Fatal(err)
		}
		u, err := url.Parse(link)
		if err != nil {
			t.Fatal(err)
		}
		q := u.Query()
		if u.Scheme+"://"+u.Host+u.Path != githubIssuer+"/authorize" || q.Get("scope") != "offline_access" || q.Get("client_id") != s.GitHubClientID || q.Get("redirect_uri") != s.RedirectURI || q.Get("code_challenge_method") != "S256" || strings.Contains(link, s.GitHubClientSecret) {
			t.Fatal("incorrect GitHub consent URL")
		}
		fixture.challenge = q.Get("code_challenge")
		return q.Get("state")
	}
	finish := func(state, code string) (service.PluginAccount, error) {
		return s.CompleteOAuth(ctx, actor, "browser", state, code, githubIssuer)
	}
	// Both cancellation and a rejected business probe must leave the original grant usable.
	before = snapshot()
	for _, code := range []string{"", "probe-denied"} {
		if _, err := finish(begin("", req.Version), code); err == nil {
			t.Fatal("failed OAuth accepted")
		}
		if snapshot() != before {
			t.Fatal("failed authorization changed durable state")
		}
		checkToken(employees[0], sharedPin, "shared-pat")
	}
	oauth, err := finish(begin("", req.Version), "fixture-code")
	if err != nil || !fixture.verified || oauth.ID != patID || oauth.AuthMethod != "oauth" || oauth.Version != req.Version+1 || oauth.ExpiresAt == nil {
		t.Fatal("PAT to OAuth switch failed", err)
	}
	_, pin = load(employees[0], version)
	checkToken(employees[0], pin, "access-1")
	checkToken(employees[0], sharedPin, "")
	// On refresh, the runtime reads OAuth metadata from a historically PAT-only definition.
	reviewExec(t, db, "UPDATE platform_plugin_connections SET expires_at=now() WHERE id=$1", oauth.ID)
	checkToken(employees[0], pin, "access-2")
	req.AuthMethod, req.Version = "bearer", oauth.Version
	invalid := "invalid-pat"
	before = snapshot()
	for _, key := range []*string{nil, &invalid} {
		req.Secret = key
		if _, err := s.PutAccount(ctx, w, actor, "", id, req); err == nil {
			t.Fatal("invalid PAT accepted")
		}
		if snapshot() != before {
			t.Fatal("failed PAT switch changed durable state")
		}
		checkToken(employees[0], pin, "access-2")
	}
	stale := begin("", oauth.Version)
	valid := "shared-pat"
	req.Secret = &valid
	shared, err := s.PutAccount(ctx, w, actor, "", id, req)
	if err != nil || shared.ID != patID || shared.AuthMethod != "bearer" || shared.ExpiresAt != nil || shared.GrantedScopes != "" || shared.ExternalAccount != "" || shared.AgentCount != 2 {
		t.Fatal("OAuth to PAT switch failed", err)
	}
	if _, err := finish(stale, "fixture-code"); err == nil {
		t.Fatal("late callback overwrote newer PAT")
	}
	_, sharedPin = load(employees[0], version)
	for _, e := range employees {
		checkToken(e, sharedPin, "shared-pat")
	}
	if _, err := finish(begin(employees[1], 0), "probe-denied"); err == nil {
		t.Fatal("failed exclusive connection accepted")
	}
	checkToken(employees[1], sharedPin, "shared-pat")
	private, err := finish(begin(employees[1], 0), "fixture-code")
	if err != nil || private.ID == patID || private.Source != "agent" {
		t.Fatal("private OAuth failed", err)
	}
	src, privatePin := load(employees[1], version)
	if src.Source != "agent" || privatePin.ID != private.ID {
		t.Fatal("wrong private selection")
	}
	checkToken(employees[1], sharedPin, "")
	checkToken(employees[1], privatePin, "access-1")
	checkToken(employees[0], sharedPin, "shared-pat")
	checkAdmission(employees[0], "team-account")
	checkAdmission(employees[1], "oauth-account")
	if err = s.RevokeAccount(ctx, w, actor, employees[1], id, private.Version, version); err != nil {
		t.Fatal(err)
	}
	src, revoked := load(employees[1], version)
	if src.Source != "agent" || src.Account.Enabled || src.Account.Validated || revoked.ID != private.ID {
		t.Fatal("private revoke fell back")
	}
	for _, p := range []service.RuntimeAccountPin{privatePin, revoked, sharedPin} {
		checkToken(employees[1], p, "")
	}
	checkToken(employees[0], sharedPin, "shared-pat")
	// The current release must also default to PAT without deployment OAuth configuration.
	checkAdmission(employees[0], "team-account")
	checkAdmission(employees[1], "")
	s.GitHubClientID, s.GitHubClientSecret = "", ""
	created, err := s.PutAccount(ctx, w, actor, "", id, service.PluginAccountPut{Name: "new", PluginVersionID: latest.VersionID, Secret: &valid})
	if err != nil || created.AuthMethod != "bearer" {
		t.Fatal("new GitHub connection did not default to PAT", err)
	}
}
