package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	managercontroller "github.com/unrealm-ai/kikoteam/backend/internal/manager/controller"
	humanconfirmation "github.com/unrealm-ai/kikoteam/backend/internal/manager/mcp/humanconfirmation"
	delegation "github.com/unrealm-ai/kikoteam/backend/internal/manager/mcp/taskdelegation"
	taskspacehistory "github.com/unrealm-ai/kikoteam/backend/internal/manager/mcp/taskspacehistory"
	workitems "github.com/unrealm-ai/kikoteam/backend/internal/manager/mcp/workitems"
	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	platformcontroller "github.com/unrealm-ai/kikoteam/backend/internal/platform/controller"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/integration"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

// This opt-in acceptance makes billable requests to the configured supplier.
// It never supplies a test provider or rewrites the upstream Pi runtime.
func TestLiveOAuthConnections(t *testing.T) { liveConnectionAcceptance(t, false) }
func TestLiveBuiltinOffice(t *testing.T)    { liveConnectionAcceptance(t, true) }
func TestLiveBuiltinGitHub(t *testing.T)    { liveConnectionAcceptance(t, true) }
func TestLiveBuiltinMarket(t *testing.T)    { liveConnectionAcceptance(t, true) }
func liveConnectionAcceptance(t *testing.T, market bool) {
	if os.Getenv("KIKO_LIVE_MODEL_E2E") != "1" {
		t.Skip("opt in with KIKO_LIVE_MODEL_E2E=1 and DEEPSEEK_* credentials")
	}
	github := t.Name() == "TestLiveBuiltinGitHub"
	webConsent := github && os.Getenv("KIKO_LIVE_GITHUB_TOKEN") == ""
	if webConsent && (os.Getenv("KIKO_GITHUB_OAUTH_CLIENT_ID") == "" || os.Getenv("KIKO_GITHUB_OAUTH_CLIENT_SECRET") == "") {
		t.Fatal("configure the test GitHub App and register the loopback callback on port 18089")
	}
	key, endpoint, modelID := os.Getenv("DEEPSEEK_API_KEY"), os.Getenv("DEEPSEEK_BASE_URL"), os.Getenv("DEEPSEEK_MODEL")
	if key == "" || endpoint == "" || modelID == "" {
		t.Fatal("DEEPSEEK_API_KEY, DEEPSEEK_BASE_URL and DEEPSEEK_MODEL are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	db, models, workspace, actor := modelFixture(t)
	models.Prober = &integration.ModelProber{}
	models.NormalizeEndpoint = integration.NormalizeModelEndpoint
	cfg := service.ModelConfig{Name: "Live DeepSeek acceptance", Protocol: "chat_completions", BaseURL: endpoint, ModelID: modelID, APIKey: &key}
	probe, err := models.Test(ctx, workspace, actor, service.ModelTestRequest{ModelConfig: cfg})
	if err != nil || probe.Receipt == "" {
		t.Fatalf("live model validation failed: text=%s issue=%s error=%v", probe.Text.Status, probe.Text.Issue, err)
	}
	model, err := models.Save(ctx, workspace, actor, "", service.ModelSaveRequest{ModelConfig: cfg, TestReceipt: probe.Receipt})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = models.Change(ctx, workspace, actor, model.ID, "set-default", model.Version, true); err != nil {
		t.Fatal(err)
	}
	temp := t.TempDir()
	runtime := runtimeCapabilityServices(t, db)
	runtime.Plugins.Probe = &integration.MCPProber{}
	var ledger *oauthLedger
	if !market {
		ledger = newOAuthLedger(t)
		runtime.Plugins.Probe = &integration.MCPProber{Client: ledger.Client()}
	}
	store := &managerrepo.Store{DB: db, SnapshotCapabilities: SnapshotCapabilities, RuntimeRoot: temp}
	svc := &manager.Service{ProjectRoot: temp, Store: store, CursorSecret: []byte(strings.Repeat("c", 40))}
	engineToken := strings.Repeat("e", 40)
	cookie := service.NewID()
	if _, err = db.ExecContext(ctx, `INSERT INTO platform_sessions(token_hash,account_id,expires_at) VALUES($1,$2,$3)`, service.HashToken(cookie), actor, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	auth := &service.AuthService{Store: &AuthStore{DB: db}}
	mux := http.NewServeMux()
	(&managercontroller.Server{Runtime: runtime, Service: svc, Engines: store, EngineToken: engineToken, Origin: "http://localhost", Authenticate: func(ctx context.Context, raw string) (string, error) { v, e := auth.Me(ctx, raw); return v.ID, e }, ResolveModelKey: func(ctx context.Context, w, a string, c manager.Configuration) (string, error) {
		return models.RuntimeModelKey(ctx, w, a, c.ModelProfileID, c.Protocol, c.BaseURL, c.ModelID)
	}}).Routes(mux)
	handler, err := delegation.Handler(&manager.Delegation{Manager: svc, Store: store, Secret: engineToken}, nil)
	if err != nil {
		t.Fatal(err)
	}
	mux.Handle("/mcp/task-delegation", handler)
	if err := workitems.Register(mux, store, engineToken); err != nil {
		t.Fatal(err)
	}
	if err := taskspacehistory.Register(mux, &manager.TaskSpaceHistory{Store: store, CursorSecret: svc.CursorSecret}, engineToken, nil); err != nil {
		t.Fatal(err)
	}
	if err := humanconfirmation.Register(mux, store, engineToken); err != nil {
		t.Fatal(err)
	}
	platformAPI := (&platformcontroller.Server{Auth: auth, Plugins: runtime.Plugins}).Handler()
	mux.Handle("/api/v1/auth/", platformAPI)
	mux.Handle("/api/v1/workspaces/{w}/plugin-accounts/", platformAPI)
	mux.Handle("/api/v1/workspaces/{w}/agents/{a}/plugin-accounts/", platformAPI)
	mux.Handle("/api/v1/workspaces/{w}/agents/{a}/plugins/", platformAPI)
	backend := httptest.NewUnstartedServer(mux)
	if webConsent {
		runtime.Plugins.GitHubClientID = os.Getenv("KIKO_GITHUB_OAUTH_CLIENT_ID")
		runtime.Plugins.GitHubClientSecret = os.Getenv("KIKO_GITHUB_OAUTH_CLIENT_SECRET")
		if err := backend.Listener.Close(); err != nil {
			t.Fatal(err)
		}
		backend.Listener, err = net.Listen("tcp4", "127.0.0.1:18089")
		if err != nil {
			t.Fatal(err)
		}
	}
	backend.Start()
	runtime.Plugins.RedirectURI = backend.URL + "/api/v1/auth/callbacks/plugin-oauth"
	defer backend.Close()
	dispatchCtx, stopDispatch := context.WithCancel(ctx)
	dispatchDone := make(chan struct{})
	go func() {
		defer close(dispatchDone)
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			if e := store.Dispatch(dispatchCtx); e != nil && dispatchCtx.Err() == nil {
				t.Error("live dispatch failed")
			}
			select {
			case <-dispatchCtx.Done():
				return
			case <-tick.C:
			}
		}
	}()
	defer func() { stopDispatch(); <-dispatchDone }()
	call := func(method, path string, value any, dedup string) map[string]any {
		t.Helper()
		data, e := json.Marshal(value)
		if e != nil {
			t.Fatal(e)
		}
		r, e := http.NewRequestWithContext(ctx, method, backend.URL+path, bytes.NewReader(data))
		if e != nil {
			t.Fatal(e)
		}
		r.Header.Set("Cookie", "kiko_session="+cookie)
		r.Header.Set("X-CSRF-Protection", "1")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", dedup)
		res, e := (&http.Client{Timeout: 30 * time.Second}).Do(r)
		if e != nil {
			t.Fatal("live Backend request failed")
		}
		defer func() { _ = res.Body.Close() }()
		var out map[string]any
		if res.StatusCode == 204 {
			return nil
		}
		if res.StatusCode >= 400 {
			t.Fatalf("%s %s failed: %v", method, path, res.StatusCode)
		}
		if e = json.NewDecoder(res.Body).Decode(&out); e != nil {
			t.Fatal(e)
		}
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			t.Fatalf("live Backend HTTP %d", res.StatusCode)
		}
		return out
	}
	binary := filepath.Join(temp, "agent-engine")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/agent-engine")
	build.Dir = filepath.Join(root, "agent-engine")
	if output, e := build.CombinedOutput(); e != nil {
		t.Fatalf("Engine build failed: %s", output)
	}
	var schema string
	if err = db.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	dsn, err := url.Parse(os.Getenv("BACKEND_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal("invalid test database configuration")
	}
	query := dsn.Query()
	query.Set("search_path", schema)
	dsn.RawQuery = query.Encode()
	start := func() func() {
		t.Helper()
		cmd := exec.CommandContext(ctx, binary)
		var childEnv []string
		for _, kv := range os.Environ() {
			if !strings.HasPrefix(kv, "DEEPSEEK_") && !strings.HasPrefix(kv, "KIKO_GITHUB_OAUTH_") && !strings.HasPrefix(kv, "KIKO_LIVE_GITHUB_") {
				childEnv = append(childEnv, kv)
			}
		}
		cmd.Stderr = os.Stderr
		cmd.Env = append(childEnv, "DATABASE_URL="+dsn.String(), "KIKO_BACKEND_URL="+backend.URL, "KIKO_ENGINE_TOKEN="+engineToken, "KIKO_REPO_ROOT="+root, "KIKO_STORAGE_LOCAL_ROOT="+temp, "KIKO_ENGINE_HOST_ID=live-test-host")
		if e := cmd.Start(); e != nil {
			t.Fatal("Engine start failed")
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		return func() {
			t.Helper()
			if e := cmd.Process.Signal(os.Interrupt); e != nil {
				t.Error("Engine signal failed")
			}
			select {
			case e := <-done:
				if e != nil {
					t.Error("Engine did not exit normally")
				}
			case <-time.After(45 * time.Second):
				_ = cmd.Process.Kill()
				if e := <-done; e != nil {
					t.Log("forced Engine process exited")
				}
				t.Error("Engine shutdown timeout")
			}
		}
	}
	stop := start()
	defer func() {
		if stop != nil {
			stop()
		}
	}()
	base := "/api/v1/workspaces/" + workspace
	if t.Name() == "TestLiveBuiltinOffice" {
		verifyOfficeChat(t, ctx, db, runtime, workspace, actor, model.ID, base, temp, call)
		return
	}
	if market {
		verifyCatalogChat(t, ctx, db, runtime, workspace, actor, model.ID, base, call, func(p service.Plugin) {
			if token := os.Getenv("KIKO_LIVE_GITHUB_TOKEN"); token != "" {
				if _, err := runtime.Plugins.PutAccount(ctx, workspace, actor, "", p.ID, service.PluginAccountPut{Name: "只读 PAT", PluginVersionID: p.VersionID, Secret: &token}); err != nil {
					t.Fatal(err)
				}
				return
			}
			out := call("POST", base+"/plugin-accounts/"+p.ID+"/oauth/authorize", map[string]any{"name": "GitHub 验证账号", "plugin_version_id": p.VersionID, "version": 0}, "")
			path := "/consent/" + service.NewID()
			mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Referrer-Policy", "no-referrer")
				w.Header().Set("Cache-Control", "no-store")
				http.SetCookie(w, &http.Cookie{Name: "kiko_session", Value: cookie, HttpOnly: true, Path: "/api/v1/auth/callbacks/plugin-oauth", SameSite: http.SameSiteLaxMode})
				http.Redirect(w, r, out["authorization_url"].(string), http.StatusFound)
			})
			t.Log("Open this local link in your browser to authorize the test GitHub App:", backend.URL+path)
			for deadline := time.Now().Add(4 * time.Minute); time.Now().Before(deadline) && ctx.Err() == nil; time.Sleep(time.Second) {
				a, err := runtime.Plugins.GetAccount(ctx, workspace, actor, "", p.ID, p.VersionID)
				if err == nil && a.Enabled && a.Validated {
					return
				}
				if err != nil && !errors.Is(err, service.ErrWorkspaceMissing) {
					t.Fatal(err)
				}
			}
			t.Fatal("GitHub browser authorization did not complete")
		})
		return
	}
	agent, err := (&service.AgentService{Store: NewAgentStore(db)}).Create(ctx, workspace, actor, service.AgentCreate{Name: "Live acceptance", ModelProfileID: model.ID, Role: "业务对账专员"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := (&service.AgentService{Store: NewAgentStore(db)}).Create(ctx, workspace, actor, service.AgentCreate{Name: "财务复核员", Role: "业务对账专员", ModelProfileID: model.ID})
	if err != nil {
		t.Fatal(err)
	}
	plugin, err := runtime.Plugins.Create(ctx, workspace, actor, service.PluginCreate{Name: "业务对账", Endpoint: ledger.URL + "/ledger", AuthType: "oauth", OAuth: &service.OAuthClientInput{Issuer: ledger.URL, ClientID: "kiko", ClientSecret: "fixture-app-secret", Scopes: "ledger.read"}})
	if err != nil {
		t.Fatal(err)
	}
	connect := func(employee, who string) service.PluginAccount {
		t.Helper()
		path := base
		if employee != "" {
			path += "/agents/" + employee
		}
		path += "/plugin-accounts/" + plugin.ID
		out := call("POST", path+"/oauth/authorize", map[string]any{"name": who, "plugin_version_id": plugin.VersionID, "version": 0}, "")
		browser := *ledger.Client()
		browser.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		req, e := http.NewRequestWithContext(ctx, "GET", out["authorization_url"].(string), nil)
		if e != nil {
			t.Fatal(e)
		}
		req.AddCookie(&http.Cookie{Name: "ledger_login", Value: who})
		res, e := browser.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		_ = res.Body.Close()
		if res.StatusCode != 302 {
			t.Fatal("consent failed", res.StatusCode)
		}
		callback, e := http.NewRequestWithContext(ctx, "GET", res.Header.Get("Location"), nil)
		if e != nil {
			t.Fatal(e)
		}
		callback.AddCookie(&http.Cookie{Name: "kiko_session", Value: cookie})
		res, e = browser.Do(callback)
		if e != nil {
			t.Fatal(e)
		}
		_ = res.Body.Close()
		if res.StatusCode != 303 {
			t.Fatal("OAuth callback failed", res.StatusCode)
		}
		acc, e := runtime.Plugins.GetAccount(ctx, workspace, actor, employee, plugin.ID, plugin.VersionID)
		if e != nil || !acc.Validated {
			t.Fatal("connection missing", e)
		}
		return acc
	}
	shared := connect("", "team")
	for _, employee := range []string{agent.ID, second.ID} {
		_, err = runtime.Packages.Create(ctx, workspace, actor, employee, service.PackageCreate{Name: "对账查询", Plugins: []service.PackagePlugin{{PluginID: plugin.ID, VersionID: plugin.VersionID}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	chats := map[string]string{}
	run := func(employee, expected, connection string) {
		t.Helper()
		before, _, _ := ledger.counts()
		round := "R" + service.NewID()[:5]
		request := "请安排一次新的后台核对工作，查当前业务账本里最新的对账回执编号，并在这里回复编号和本次核对单号 " + round + "。请将结果交付回本对话。只检查现有访问权限，不能访问也是本次工作的最终结论，无需等我补资料或重新授权。如果确实无法访问，就直接交付并回复‘未能核实’和核对单号。"
		conversation := call("POST", base+"/conversations/web-direct", map[string]string{"agent_id": employee}, "")["id"].(string)
		chat := chats[employee]
		var previous int
		err = db.QueryRowContext(ctx, `SELECT count(*) FROM manager_tasks WHERE source_chat_session_id=NULLIF($1,'')::uuid`, chat).Scan(&previous)
		if err != nil {
			t.Fatal(err)
		}
		if chat == "" {
			chat = call("POST", base+"/sessions", map[string]any{"conversation_id": conversation, "mode": "chat", "input": map[string]string{"text": request}}, round)["id"].(string)
			chats[employee] = chat
		} else {
			call("POST", base+"/sessions/"+chat+"/inputs", map[string]string{"text": request}, round)
		}
		var last string
		for deadline := time.Now().Add(150 * time.Second); time.Now().Before(deadline); time.Sleep(time.Second) {
			var count int
			var task string
			err = db.QueryRowContext(ctx, `SELECT count(*),COALESCE((array_agg(s.id ORDER BY s.created_at DESC))[1]::text,'') FROM manager_tasks t JOIN manager_sessions s ON s.task_id=t.id AND s.mode='task' WHERE t.source_chat_session_id=$1`, chat).Scan(&count, &task)
			if err != nil {
				t.Fatal(err)
			}
			if count <= previous {
				continue
			}
			reads, _, marker := ledger.counts()
			if expected == "" && reads != before {
				t.Fatal("revoked employee reached upstream")
			}
			events := call("GET", base+"/sessions/"+chat+"/events?type=agent.message", nil, "")["items"].([]any)
			for _, ev := range events {
				bits, _ := ev.(map[string]any)["content"].([]any)
				for _, b := range bits {
					msg, _ := b.(map[string]any)["text"].(string)
					if strings.Contains(msg, round) {
						last = msg
						if expected == "" && !strings.Contains(msg, "未能核实") {
							continue
						}
						if expected != "" && (reads <= before || !strings.HasPrefix(marker, expected+"-") || !strings.Contains(msg, marker)) {
							continue
						}
						var reported bool
						err = db.QueryRowContext(ctx, `SELECT EXISTS(SELECT FROM manager_task_reports r JOIN manager_turns t ON t.id=r.source_turn_id JOIN manager_inputs i ON i.id=r.target_input_id WHERE t.session_id=$1 AND r.target_session_id=$2 AND i.status='processed' AND EXISTS(SELECT FROM manager_turns chat_turn WHERE chat_turn.input_id=i.id AND chat_turn.id::text=$3))`, task, chat, ev.(map[string]any)["turn_id"]).Scan(&reported)
						if err != nil {
							t.Fatal(err)
						}
						if !reported {
							continue
						}
						var matched, other int
						err = db.QueryRowContext(ctx, `SELECT count(*) FILTER(WHERE account_id::text=$2 AND agent_id=$3),count(*) FILTER(WHERE account_id::text IS DISTINCT FROM $2 OR agent_id IS DISTINCT FROM $3) FROM manager_capability_calls WHERE session_id=$1 AND status='succeeded'`, task, connection, employee).Scan(&matched, &other)
						if err != nil || other != 0 || expected != "" && matched == 0 {
							t.Fatalf("unexpected audit matched=%d other=%d err=%v", matched, other, err)
						}
						t.Logf("employee=%s chat=%s task=%s connection=%s upstream_calls=%d original_reply=%s", employee, chat, task, connection, reads-before, msg)
						return
					}
				}
			}
		}
		t.Fatalf("no verified original-chat reply for %s; last=%s", expected, last)
	}
	run(agent.ID, "team", shared.ID)
	reviewExec(t, db, "UPDATE platform_plugin_connections SET expires_at=now() WHERE id=$1", shared.ID)
	run(second.ID, "team", shared.ID)
	if _, refreshes, _ := ledger.counts(); refreshes != 1 {
		t.Fatal("shared connection refresh count", refreshes)
	}
	private := connect(second.ID, "private")
	run(second.ID, "private", private.ID)
	call("DELETE", base+"/agents/"+second.ID+"/plugin-accounts/"+plugin.ID+"?plugin_version_id="+plugin.VersionID, map[string]any{"version": private.Version}, "")
	run(second.ID, "", "")
	run(agent.ID, "team", shared.ID)
}
