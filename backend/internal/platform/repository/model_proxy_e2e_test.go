package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	mc "github.com/unrealm-ai/kikoteam/backend/internal/manager/controller"
	delegation "github.com/unrealm-ai/kikoteam/backend/internal/manager/mcp/taskdelegation"
	workitems "github.com/unrealm-ai/kikoteam/backend/internal/manager/mcp/workitems"
	mr "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/controller"
	proxy "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy"
	pc "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy/controller"
	pi "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy/integration"
	pr "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy/repository"
	ps "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type proxyFixture struct {
	db          *sql.DB
	store       pr.Store
	secrets     ps.Secrets
	server      *httptest.Server
	provider    *httptest.Server
	engineToken string
	entered     chan struct{}
	release     chan struct{}
}

func modelProxyFixture(t *testing.T) *proxyFixture {
	t.Helper()
	return modelProxyFixtureOnDB(t, workspaceTestDB(t))
}

func modelProxyFixtureOnDB(t *testing.T, db *sql.DB) *proxyFixture {
	t.Helper()
	f := &proxyFixture{db: db, secrets: ps.Secrets{Root: []byte(strings.Repeat("m", 40))},
		engineToken: strings.Repeat("e", 40), entered: make(chan struct{}, 2), release: make(chan struct{})}
	f.store = pr.Store{DB: f.db}
	f.provider = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveProxyFixture(t, f, w, r)
	}))
	t.Cleanup(f.provider.Close)
	upstream := pi.Upstream{HTTP: pi.Client()}
	auth := &service.AuthService{Store: &AuthStore{DB: f.db, InitCredits: pr.Signup}, Secret: f.secrets.Root, EmailCodeDisabled: true}
	models := &service.ModelService{Store: NewModelStore(f.db)}
	mux := http.NewServeMux()
	managerStore := &mr.Store{DB: f.db, SnapshotCapabilities: SnapshotCapabilities}
	managerService := &manager.Service{Store: managerStore, CursorSecret: f.secrets.Root}
	f.server = httptest.NewUnstartedServer(mux)
	origin := "http://" + f.server.Listener.Addr().String()
	(&mc.Server{Origin: origin, EngineToken: f.engineToken, Engines: managerStore, Service: managerService, Runtime: runtimeCapabilityServices(t, f.db),
		Authenticate: func(ctx context.Context, token string) (string, error) { a, e := auth.Me(ctx, token); return a.ID, e },
		ResolveModelKey: func(ctx context.Context, w, actor string, c manager.Configuration) (string, error) {
			return models.RuntimeModelKey(ctx, w, actor, c.ModelProfileID, c.Protocol, c.BaseURL, c.ModelID)
		},
		ResolveOfficialModel: func(ctx context.Context, workspace, actor, agent string, c *manager.Configuration) (string, error) {
			if e := models.AuthorizeOfficialRuntime(ctx, workspace, actor, agent); e != nil {
				return "", e
			}
			c.BaseURL = origin + "/model-proxy/v1"
			if c.Protocol == "anthropic_messages" {
				c.BaseURL = origin + "/model-proxy"
			}
			return f.secrets.RuntimeKey(ctx, f.db, workspace, agent, c.PlatformModelID, c.Protocol)
		}}).Routes(mux)
	delegate, e := delegation.Handler(&manager.Delegation{Manager: managerService, Store: managerStore, Secret: f.engineToken}, nil)
	if e != nil {
		t.Fatal(e)
	}
	mux.Handle("/mcp/task-delegation", delegate)
	if err := workitems.Register(mux, managerStore, f.engineToken); err != nil {
		t.Fatal(err)
	}
	(pc.Proxy{Calls: f.store, Secrets: f.secrets, Upstream: upstream}).Routes(mux, strings.Repeat("h", 40))
	mux.Handle("/api/", (&controller.Server{Origin: origin, Auth: auth, Official: &ps.Models{Store: f.store, Secrets: f.secrets, Upstream: upstream, AllowLoopbackHTTP: true},
		Models: models, Agents: &service.AgentService{Store: &AgentStore{DB: f.db, EnsureKey: f.secrets.EnsureKey}},
		Workspaces: &service.WorkspaceService{Store: NewWorkspaceStore(f.db)},
		Templates:  &service.TemplateService{Store: &TemplateStore{DB: f.db, EnsureKey: f.secrets.EnsureKey}},
		Admin:      &service.AdminService{Store: &AdminStore{DB: f.db}}}).Handler())
	if dir := os.Getenv("KIKO_PROXY_WEB_DIST"); dir != "" {
		mux.Handle("/", http.FileServer(http.Dir(dir)))
	}
	f.server.Start()
	t.Cleanup(f.server.Close)
	return f
}

func serveProxyFixture(t *testing.T, f *proxyFixture, w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
		t.Error(e)
		w.WriteHeader(400)
		return
	}
	if body["model"] != "actual" || r.Header.Get("Authorization") != "Bearer supplier-only" && r.Header.Get("X-Api-Key") != "supplier-only" || r.Header.Get("Cookie") != "" {
		t.Error("upstream target or credential was incorrect")
		w.WriteHeader(400)
		return
	}
	if strings.Contains(fmt.Sprint(body["messages"]), "parallel") {
		f.entered <- struct{}{}
		select {
		case <-f.release:
		case <-r.Context().Done():
			return
		}
	}
	w.Header().Set("X-Request-ID", "test-provider")
	w.Header().Set("Content-Type", "text/event-stream")
	switch r.URL.Path {
	case "/v1/messages":
		writeFixture(t, w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":2,\"cache_creation_input_tokens\":1,\"cache_read_input_tokens\":2,\"output_tokens\":0}}}\n\n"+
			"event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":3}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	case "/v1/responses":
		writeFixture(t, w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":5,\"input_tokens_details\":{\"cached_tokens\":3},\"output_tokens\":3}}}\n\n")
	case "/v1/chat/completions":
		if opts, ok := body["stream_options"].(map[string]any); !ok || opts["include_usage"] != true {
			t.Error("usage was not enabled upstream")
		}
		writeFixture(t, w, "data: {\"id\":\"fixture\",\"object\":\"chat.completion.chunk\",\"model\":\"actual\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Model proxy fixture reply\"},\"finish_reason\":null}]}\n\n")
		writeFixture(t, w, "data: {\"id\":\"fixture\",\"object\":\"chat.completion.chunk\",\"model\":\"actual\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		if !strings.Contains(fmt.Sprint(body["messages"]), "missing usage") {
			writeFixture(t, w, "data: {\"id\":\"fixture\",\"object\":\"chat.completion.chunk\",\"model\":\"actual\",\"choices\":[],\"usage\":{\"prompt_tokens\":5,\"prompt_tokens_details\":{\"cached_tokens\":3},\"completion_tokens\":3}}\n\n")
		}
		writeFixture(t, w, "data: [DONE]\n\n")
	default:
		t.Errorf("unexpected fixture path %s", r.URL.Path)
	}
}

func writeFixture(t *testing.T, w http.ResponseWriter, value string) {
	t.Helper()
	if _, e := io.WriteString(w, value); e != nil {
		t.Error(e)
	}
}
func proxyBrowserClient(t *testing.T) *http.Client {
	t.Helper()
	jar, e := cookiejar.New(nil)
	if e != nil {
		t.Fatal(e)
	}
	return &http.Client{Jar: jar, Timeout: 10 * time.Second}
}

func (f *proxyFixture) api(t *testing.T, c *http.Client, method, path string, body any, key string, status int) map[string]any {
	t.Helper()
	raw, e := json.Marshal(body)
	if e != nil {
		t.Fatal(e)
	}
	req, e := http.NewRequestWithContext(t.Context(), method, f.server.URL+"/api/v1"+path, strings.NewReader(string(raw)))
	if e != nil {
		t.Fatal(e)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Protection", "1")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	reply, e := c.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer reply.Body.Close() //nolint:errcheck // Response fully read.
	data, e := io.ReadAll(reply.Body)
	if e != nil || reply.StatusCode != status {
		t.Fatalf("%s %s status=%d wanted=%d body=%s error=%v", method, path, reply.StatusCode, status, data, e)
	}
	var out map[string]any
	if len(data) > 0 && json.Unmarshal(data, &out) != nil {
		t.Fatalf("invalid JSON: %s", data)
	}
	return out
}

func (f *proxyFixture) register(t *testing.T, c *http.Client, email string) (string, string) {
	t.Helper()
	v := f.api(t, c, "POST", "/auth/registrations", map[string]string{"email": email, "name": "Test user", "method": "password", "password": "sample-test-password"}, "", 201)
	workspaces := f.api(t, c, "GET", "/workspaces", nil, "", 200)
	return v["user"].(map[string]any)["id"].(string), workspaces["items"].([]any)[0].(map[string]any)["id"].(string)
}

func (f *proxyFixture) createModel(t *testing.T, c *http.Client, protocol string, def bool) string {
	t.Helper()
	body := map[string]any{"name": protocol, "protocol": protocol, "base_url": f.provider.URL + "/v1", "upstream_model_id": "actual", "api_key": "supplier-only"}
	test := f.api(t, c, "POST", "/platform-models/test", body, "", 200)
	if test["usage_available"] != true {
		t.Fatalf("probe did not return billable usage: %v", test)
	}
	body["test_receipt"] = test["receipt"]
	body["prices"] = map[string]any{"input": "1000000", "output": "1000000", "cache_read": "0", "cache_write": "0"}
	body["enabled"] = true
	body["is_default"] = def
	out := f.api(t, c, "POST", "/platform-models", body, "", 201)
	if _, ok := out["api_key"]; ok || out["has_key"] != true {
		t.Fatal("supplier credential projection is inaccurate")
	}
	return out["id"].(string)
}

func (f *proxyFixture) callModel(ctx context.Context, path, key, id, prompt string) (int, string, error) {
	body, e := json.Marshal(map[string]any{"model": id, "stream": true, "messages": []any{map[string]string{"role": "user", "content": prompt}}})
	if e != nil {
		return 0, "", e
	}
	req, e := http.NewRequestWithContext(ctx, "POST", f.server.URL+"/model-proxy/v1/"+path, strings.NewReader(string(body)))
	if e != nil {
		return 0, "", e
	}
	if path == "messages" {
		req.Header.Set("x-api-key", key)
	} else {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	response, e := http.DefaultClient.Do(req)
	if e != nil {
		return 0, "", e
	}
	defer response.Body.Close() //nolint:errcheck // Response fully read.
	data, e := io.ReadAll(response.Body)
	if e != nil {
		return 0, "", e
	}
	if strings.Contains(string(data), "supplier-only") {
		return 0, "", fmt.Errorf("supplier credential leaked")
	}
	return response.StatusCode, response.Header.Get("X-Model-Call-ID"), nil
}

func TestOfficialModelsAndCreditsEndToEnd(t *testing.T) {
	f := modelProxyFixture(t)
	admin, user := proxyBrowserClient(t), proxyBrowserClient(t)
	adminID, _ := f.register(t, admin, "admin@model.test")
	f.api(t, admin, "PATCH", "/credit-settings", map[string]any{"version": 1, "signup_bonus": "6"}, "", 200)
	userID, w := f.register(t, user, "user@model.test")
	checkBalance := func(c *http.Client, id, want string) {
		t.Helper()
		if got := f.api(t, c, "GET", "/accounts/"+id+"/credit-wallet", nil, "", 200)["balance"]; got != want {
			t.Fatalf("balance=%v want=%s", got, want)
		}
	}
	checkBalance(admin, adminID, "1000.000000")
	checkBalance(user, userID, "6.000000")
	rollingAccount := service.NewID()
	insertWorkspaceAccount(t, f.db, rollingAccount, "during-rollout@model.test")
	checkBalance(admin, rollingAccount, "0.000000")
	f.api(t, admin, "POST", "/accounts/"+rollingAccount+"/credit-grants", map[string]string{"amount": "1", "reason": "rollout account"}, "rollout-grant", 201)
	checkBalance(admin, rollingAccount, "1.000000")
	f.api(t, user, "GET", "/accounts/"+adminID+"/credit-wallet", nil, "", 403)
	f.api(t, user, "GET", "/platform-models", nil, "", 403)
	chat := f.createModel(t, admin, "chat_completions", true)
	messages := f.createModel(t, admin, "anthropic_messages", false)
	responses := f.createModel(t, admin, "openai_responses", false)
	base := "/workspaces/" + w
	available := f.api(t, user, "GET", base+"/model-profiles?include_platform=true&page_size=2", nil, "", 200)
	if available["total"] != float64(3) || len(available["items"].([]any)) != 2 {
		t.Fatalf("unified pagination: %v", available)
	}
	for _, v := range available["items"].([]any) {
		if item := v.(map[string]any); item["source"] != "platform" || item["base_url"] != nil {
			t.Fatalf("unsafe model selection: %v", item)
		}
	}
	original := f.api(t, user, "GET", base+"/model-profiles", nil, "", 200)
	if original["total"] != float64(0) {
		t.Fatalf("official models entered workspace management: %v", original)
	}
	reviewExec(t, f.db, `INSERT INTO platform_members(workspace_id,user_id,role) VALUES($1,$2,'admin')`, w, adminID)
	employee := f.api(t, admin, "POST", base+"/agents", map[string]string{"name": "Support", "platform_model_id": chat}, "", 201)
	id := employee["id"].(string)
	if employee["payer"].(map[string]any)["id"] != userID {
		t.Fatalf("incorrect payer: %v", employee)
	}
	key, e := f.secrets.RuntimeKey(t.Context(), f.db, w, id, chat, "chat_completions")
	if e != nil {
		t.Fatal(e)
	}
	var wait sync.WaitGroup
	var release sync.Once
	unblock := func() { release.Do(func() { close(f.release) }) }
	defer unblock()
	outcomes := make(chan error, 2)
	for range 2 {
		wait.Go(func() {
			status, _, e := f.callModel(t.Context(), "chat/completions", key, chat, "parallel")
			if status != 200 && e == nil {
				e = fmt.Errorf("parallel call returned %d", status)
			}
			outcomes <- e
		})
	}
	for range 2 {
		select {
		case <-f.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent calls did not both reach supplier")
		}
	}
	// After admission, changing price must not change either call's charge.
	f.api(t, admin, "PATCH", "/platform-models/"+chat, map[string]any{"version": 1, "prices": map[string]string{"input": "2000000", "output": "2000000", "cache_read": "0", "cache_write": "0"}}, "", 200)
	unblock()
	wait.Wait()
	for range 2 {
		if e = <-outcomes; e != nil {
			t.Fatal(e)
		}
	}
	checkBalance(user, userID, "-4.000000")
	checkBalance(admin, adminID, "1000.000000")
	if status, _, e := f.callModel(t.Context(), "chat/completions", key, chat, "blocked"); e != nil || status != 402 {
		t.Fatalf("overdraft not blocked: %d %v", status, e)
	}
	path := "/accounts/" + userID + "/credit-grants"
	grant := map[string]string{"amount": "30", "reason": "test grant"}
	f.api(t, admin, "POST", path, grant, "same-grant", 201)
	f.api(t, admin, "POST", path, grant, "same-grant", 200)
	f.api(t, admin, "POST", path, map[string]string{"amount": "31", "reason": "test grant"}, "same-grant", 409)
	checkBalance(user, userID, "26.000000")
	for endpoint, model := range map[string]string{"messages": messages, "responses": responses, "chat/completions": chat} {
		status, call, e := f.callModel(t.Context(), endpoint, key, model, "usage")
		if e != nil || status != 200 || call == "" {
			t.Fatalf("%s: %d %s %v", endpoint, status, call, e)
		}
		if e = f.store.Settle(t.Context(), call); e != nil {
			t.Fatal(e)
		}
	}
	checkBalance(user, userID, "6.000000")
	status, call, e := f.callModel(t.Context(), "chat/completions", key, chat, "missing usage")
	if e != nil || status != 200 {
		t.Fatalf("unknown usage: %d %v", status, e)
	}
	usage := f.api(t, user, "GET", "/model-usage?status=pending_review", nil, "", 200)["items"].([]any)
	if len(usage) != 1 || usage[0].(map[string]any)["charge"] != nil {
		t.Fatalf("unknown usage was charged: %v", usage)
	}
	f.api(t, user, "POST", "/model-usage/"+call+"/resolve", map[string]string{"action": "no_charge", "reason": "verified"}, "", 403)
	f.api(t, admin, "POST", "/model-usage/"+call+"/resolve", map[string]string{"action": "no_charge", "reason": "verified"}, "", 204)
	ledger := f.api(t, user, "GET", "/accounts/"+userID+"/credit-ledger?limit=1", nil, "", 200)
	if ledger["summary"].(map[string]any)["spent"] != "30.000000" || ledger["next_cursor"] == "" {
		t.Fatalf("ledger: %v", ledger)
	}
	f.api(t, user, "GET", "/accounts/"+userID+"/credit-ledger?limit=1&cursor="+ledger["next_cursor"].(string), nil, "", 200)
	f.recoverCalls(t, key, responses)
	checkBalance(user, userID, "5.000000")
	if e = f.secrets.RotateKey(t.Context(), f.db, id); e != nil {
		t.Fatal(e)
	}
	if status, _, e = f.callModel(t.Context(), "responses", key, responses, "revoked"); e != nil || status != 401 {
		t.Fatalf("old key accepted: %d %v", status, e)
	}
	key, e = f.secrets.RuntimeKey(t.Context(), f.db, w, id, chat, "chat_completions")
	if e != nil {
		t.Fatal(e)
	}
	f.api(t, user, "PATCH", base+"/agents/"+id, map[string]any{"version": 1, "enabled": false}, "", 200)
	if status, _, e = f.callModel(t.Context(), "responses", key, responses, "disabled"); e != nil || status != 403 {
		t.Fatalf("disabled employee accepted: %d %v", status, e)
	}
}

func (f *proxyFixture) recoverCalls(t *testing.T, key, model string) {
	t.Helper()
	ctx := t.Context()
	ready, e := f.store.Admit(ctx, key, model, "openai_responses", time.Now().Add(time.Minute))
	if e != nil {
		t.Fatal(e)
	}
	if e = f.store.Record(ctx, ready.ID, "", "", "", &proxy.Usage{Input: 1}); e != nil {
		t.Fatal(e)
	}
	expired, e := f.store.Admit(ctx, key, model, "openai_responses", time.Now().Add(-time.Minute))
	if e != nil {
		t.Fatal(e)
	}
	for _, token := range []string{f.engineToken, strings.Repeat("h", 40), strings.Repeat("h", 40)} {
		req, e := http.NewRequestWithContext(ctx, "POST", f.server.URL+"/api/internal/v1/model-proxy/reconcile", nil)
		if e != nil {
			t.Fatal(e)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		if e = resp.Body.Close(); e != nil {
			t.Fatal(e)
		}
		want := 204
		if token == f.engineToken {
			want = 401
		}
		if resp.StatusCode != want {
			t.Fatalf("reconciliation status=%d want=%d", resp.StatusCode, want)
		}
	}
	for id, want := range map[string]string{ready.ID: "settled", expired.ID: "pending_review"} {
		var got string
		if e = f.db.QueryRowContext(ctx, `SELECT status FROM platform_model_usage WHERE id=$1`, id).Scan(&got); e != nil || got != want {
			t.Fatalf("reconcile %s: %s %v", id, got, e)
		}
	}
}

const insertLegacyModel = `INSERT INTO platform_model_profiles(id,workspace_id,name,protocol,base_url,model_id,version,enabled,validated,
 has_key,credential,credential_revision,vision_status,tested_at)
 VALUES($1,$2,'Existing connection','chat_completions','https://existing.example/v1','existing',1,true,true,false,'',1,'unknown',now())`

func TestOfficialModelMigrationFromExistingAccounts(t *testing.T) {
	ctx := t.Context()
	db := workspaceTestDBThrough(t, "000017_agent_templates.sql")
	owner, model, employee := service.NewID(), service.NewID(), service.NewID()
	insertWorkspaceAccount(t, db, owner, "existing@migration.test")
	workspaces := service.WorkspaceService{Store: NewWorkspaceStore(db)}
	w, e := workspaces.Create(ctx, owner, "Existing workspace", "")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.ExecContext(ctx, insertLegacyModel, model, w.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = db.ExecContext(ctx, `INSERT INTO platform_agents(id,workspace_id,name,model_profile_id,version,config_version,admission_epoch)
	 VALUES($1,$2,'Existing employee',$3,1,1,1)`, employee, w.ID, model); e != nil {
		t.Fatal(e)
	}
	const path = "../../../../event-hub/database/postgres/migrations/000018_platform_model_proxy.sql"
	sqlFile, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	up, _, _ := strings.Cut(string(sqlFile), "-- +goose Down")
	if _, e = db.ExecContext(ctx, up); e != nil {
		t.Fatal(e)
	}
	v, e := (&service.AgentService{Store: NewAgentStore(db)}).Get(ctx, w.ID, owner, employee)
	if e != nil || v.ModelProfileID != model || v.PlatformModelID != "" || v.Payer != nil {
		t.Fatalf("existing employee changed: %v %v", v, e)
	}
	var balance, ledger int64
	e = db.QueryRowContext(ctx, `SELECT balance_units,(SELECT count(*) FROM platform_credit_ledger)
	FROM platform_credit_wallets WHERE account_id=$1`, owner).Scan(&balance, &ledger)
	if e != nil || balance != 0 || ledger != 0 {
		t.Fatalf("existing wallet balance=%d ledger=%d error=%v", balance, ledger, e)
	}
}

func TestOfficialModelLegacyAndTemplateCreation(t *testing.T) {
	f := modelProxyFixture(t)
	admin, user := proxyBrowserClient(t), proxyBrowserClient(t)
	adminID, _ := f.register(t, admin, "admin@legacy.test")
	payer, w := f.register(t, user, "owner@legacy.test")
	reviewExec(t, f.db, `INSERT INTO platform_members(workspace_id,user_id,role) VALUES($1,$2,'admin')`, w, adminID)
	f.createModel(t, admin, "chat_completions", true)
	modelID := f.createModel(t, admin, "anthropic_messages", false)
	byok := service.NewID()
	if _, e := f.db.ExecContext(t.Context(), insertLegacyModel, byok, w); e != nil {
		t.Fatal(e)
	}
	legacy, e := (&service.AgentService{Store: NewAgentStore(f.db)}).Create(t.Context(), w, payer, service.AgentCreate{Name: "Legacy", ModelProfileID: byok})
	if e != nil {
		t.Fatal(e)
	}
	base := "/workspaces/" + w
	old := base + "/agents/" + legacy.ID
	out := f.api(t, user, "PATCH", old, map[string]any{"version": 1, "name": "Legacy renamed", "model_profile_id": byok}, "", 200)
	if out["payer"] != nil {
		t.Fatal("renaming a legacy employee implicitly bound a wallet")
	}
	convo := f.api(t, user, "POST", base+"/conversations", map[string]string{"agent_id": legacy.ID}, "", 201)["id"]
	chat := f.api(t, user, "POST", base+"/sessions", map[string]any{"mode": "chat", "conversation_id": convo}, "", 201)
	cfg := chat["configuration"].(map[string]any)
	if cfg["model_profile_id"] != byok || cfg["platform_model_id"] != nil && cfg["platform_model_id"] != "" {
		t.Fatalf("legacy BYOK chat used official fallback: %v", cfg)
	}
	out = f.api(t, user, "PATCH", old, map[string]any{"version": 2, "model_profile_id": byok, "confirm_platform_billing": true}, "", 200)
	if out["payer"] != nil {
		t.Fatalf("custom model update implicitly bound a wallet for an unrelated default: %v", out)
	}
	bound := f.api(t, admin, "PATCH", old, map[string]any{"version": 3, "platform_model_id": modelID, "confirm_platform_billing": true}, "", 200)
	if got := bound["payer"].(map[string]any)["id"]; got != payer {
		t.Fatalf("legacy first binding payer=%v, want workspace owner %s", got, payer)
	}
	templateSvc := &service.TemplateService{Store: &TemplateStore{DB: f.db, EnsureKey: f.secrets.EnsureKey}}
	template, e := templateSvc.Create(t.Context(), w, payer, testTemplateContent())
	if e != nil {
		t.Fatal(e)
	}
	template, e = templateSvc.Publish(t.Context(), w, payer, template.ID, service.TemplatePublish{Version: template.Version, TemplateVersionID: template.LatestVersionID})
	if e != nil {
		t.Fatal(e)
	}
	created := f.api(t, admin, "POST", base+"/agents/from-template", map[string]any{
		"template_id": template.ID, "template_version_id": template.PublishedVersionID, "name": "Official from template",
		"platform_model_id": modelID, "parameters": map[string]string{"project": "Support"},
	}, "", 201)
	if created["platform_model_id"] != modelID || created["payer"].(map[string]any)["id"] != payer {
		t.Fatalf("template creation: %v", created)
	}
	officialConversation := f.api(t, user, "POST", base+"/conversations", map[string]any{"agent_id": created["id"]}, "", 201)["id"]
	chat = f.api(t, user, "POST", base+"/sessions", map[string]any{"mode": "chat", "conversation_id": officialConversation}, "", 201)
	if chat["configuration"].(map[string]any)["platform_model_id"] != modelID {
		t.Fatalf("chat did not use the selected non-default official model: %v", chat)
	}
	reviewExec(t, f.db, `DELETE FROM platform_agent_model_keys WHERE agent_id=$1`, created["id"])
	next := f.api(t, user, "POST", base+"/conversations", map[string]any{"agent_id": created["id"]}, "", 201)["id"]
	bad := f.api(t, user, "POST", base+"/sessions", map[string]any{"mode": "chat", "conversation_id": next}, "", 422)
	businessError, _ := bad["error"].(map[string]any)
	if businessError["code"] != "platform_billing_unconfigured" {
		t.Fatalf("expected explicit official billing refusal without fallback: %v", bad)
	}
}

func TestModelProxyBrowser(t *testing.T) {
	script := os.Getenv("KIKO_PROXY_BROWSER_SCRIPT")
	if script == "" {
		t.Skip("set KIKO_PROXY_BROWSER_SCRIPT and KIKO_PROXY_WEB_DIST for browser acceptance")
	}
	f := modelProxyFixture(t)
	root, e := filepath.Abs("../../../..")
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	stop := f.startEngine(t, ctx, root)
	defer stop()
	cmd := exec.CommandContext(ctx, "node", script)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "KIKO_PROXY_BROWSER_URL="+f.server.URL, "KIKO_PROXY_PROVIDER_URL="+f.provider.URL)
	if output, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("browser: %v\n%s", e, output)
	} else {
		t.Log(string(output))
	}
}

func (f *proxyFixture) startEngine(t *testing.T, ctx context.Context, root string) func() {
	t.Helper()
	temp := t.TempDir()
	binary := filepath.Join(temp, "agent-engine")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/agent-engine")
	build.Dir = filepath.Join(root, "agent-engine")
	if out, e := build.CombinedOutput(); e != nil {
		t.Fatalf("Engine build failed: %v\n%s", e, out)
	}
	dsn, e := url.Parse(os.Getenv("BACKEND_TEST_DATABASE_URL"))
	if e != nil {
		t.Fatal(e)
	}
	var schema string
	if e = f.db.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&schema); e != nil {
		t.Fatal(e)
	}
	query := dsn.Query()
	query.Set("search_path", schema)
	dsn.RawQuery = query.Encode()
	logs, e := os.Create(filepath.Join(temp, "engine.log"))
	if e != nil {
		t.Fatal(e)
	}
	run, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(run, binary)
	cmd.Env = append(os.Environ(), "DATABASE_URL="+dsn.String(), "KIKO_BACKEND_URL="+f.server.URL, "KIKO_ENGINE_TOKEN="+f.engineToken,
		"KIKO_REPO_ROOT="+root, "KIKO_STORAGE_LOCAL_ROOT="+filepath.Join(temp, "state"), "KIKO_ENGINE_HOST_ID=model-proxy-test", "KIKO_ENGINE_ADDRESS=127.0.0.1:0")
	cmd.Stderr = logs
	if e = cmd.Start(); e != nil {
		cancel()
		_ = logs.Close()
		t.Fatal(e)
	}
	ended := make(chan error, 1)
	go func() { ended <- cmd.Wait() }()
	dispatched := make(chan struct{})
	go func() {
		defer close(dispatched)
		store := &mr.Store{DB: f.db}
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-run.Done():
				return
			case <-tick.C:
				if e := store.Dispatch(run); e != nil && run.Err() == nil {
					t.Error(e)
				}
			}
		}
	}()
	return func() {
		if e := cmd.Process.Signal(os.Interrupt); e != nil {
			t.Log(e)
		}
		select {
		case e := <-ended:
			if e != nil {
				t.Errorf("Engine shutdown: %v", e)
			}
		case <-time.After(7 * time.Second):
			cancel()
			if e := <-ended; e != nil {
				t.Errorf("Engine forced shutdown: %v", e)
			}
		}
		cancel()
		<-dispatched
		_ = logs.Close()
		if t.Failed() {
			if raw, e := os.ReadFile(logs.Name()); e == nil {
				t.Logf("Engine diagnostics: %s", raw)
			}
		}
	}
}

func TestOfficialModelWorkspaceOwnerPayerUpgrade(t *testing.T) {
	ctx := t.Context()
	f := modelProxyFixtureOnDB(t, workspaceTestDBThrough(t, "000103_conversation_last_message.sql"))
	admin, user := proxyBrowserClient(t), proxyBrowserClient(t)
	creator, _ := f.register(t, admin, "creator@payer-upgrade.test")
	owner, w := f.register(t, user, "owner@payer-upgrade.test")
	reviewExec(t, f.db, `INSERT INTO platform_members(workspace_id,user_id,role) VALUES($1,$2,'admin')`, w, creator)
	model := f.createModel(t, admin, "chat_completions", true)
	id := f.api(t, admin, "POST", "/workspaces/"+w+"/agents", map[string]string{"name": "Migrated", "platform_model_id": model}, "", 201)["id"].(string)
	key, err := f.secrets.RuntimeKey(ctx, f.db, w, id, model, "chat_completions")
	if err != nil {
		t.Fatal(err)
	}
	// Construct the payer snapshot an old binary would have written for an already admitted call.
	inFlight, err := f.store.Admit(ctx, key, model, "chat_completions", time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	reviewExec(t, f.db, `UPDATE platform_agent_model_keys SET payer_account_id=$2 WHERE agent_id=$1`, id, creator)
	reviewExec(t, f.db, `UPDATE platform_model_usage SET payer_account_id=$2 WHERE id=$1`, inFlight.ID, creator)
	if _, err := f.store.Admit(ctx, key, model, "chat_completions", time.Now().Add(time.Minute)); err == nil {
		t.Fatal("new proxy accepted previous creator as the payer")
	}

	migration, err := os.ReadFile("../../../../event-hub/database/postgres/migrations/000104_workspace_owner_model_payer.sql")
	if err != nil {
		t.Fatal(err)
	}
	reviewExec(t, f.db, string(migration))
	newKey, err := f.secrets.RuntimeKey(ctx, f.db, w, id, model, "chat_completions")
	if err != nil || newKey != key {
		t.Fatalf("runtime credential was not preserved: %v", err)
	}
	status, after, err := f.callModel(ctx, "chat/completions", key, model, "after migration")
	if err != nil || status != 200 {
		t.Fatalf("new call status=%d error=%v", status, err)
	}
	if err = f.store.Record(ctx, inFlight.ID, "", "", "", &proxy.Usage{Input: 1}); err != nil {
		t.Fatal(err)
	}
	if err = f.store.Settle(ctx, inFlight.ID); err != nil {
		t.Fatal(err)
	}
	for call, want := range map[string]string{inFlight.ID: creator, after: owner} {
		var payer string
		err = f.db.QueryRowContext(ctx, `SELECT account_id FROM platform_credit_ledger WHERE kind='model_charge' AND usage_id=$1`, call).Scan(&payer)
		if err != nil || payer != want {
			t.Fatalf("call %s charged %s, want %s: %v", call, payer, want, err)
		}
	}
}
