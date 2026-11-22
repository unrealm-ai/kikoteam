package repository

import (
	"bytes"
	"context"
	"encoding/json"
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
	delegation "github.com/unrealm-ai/kikoteam/backend/internal/manager/mcp/taskdelegation"
	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/integration"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

// This opt-in acceptance makes billable requests to the configured supplier.
// It never supplies a test provider or rewrites the upstream Pi runtime.
func TestTriggerRuntimeLiveModel(t *testing.T) {
	if os.Getenv("KIKO_TRIGGER_LIVE_E2E") != "1" {
		t.Skip("opt in with KIKO_TRIGGER_LIVE_E2E=1 and DEEPSEEK_* credentials")
	}
	key, endpoint, modelID := os.Getenv("DEEPSEEK_API_KEY"), os.Getenv("DEEPSEEK_BASE_URL"), os.Getenv("DEEPSEEK_MODEL")
	if key == "" || endpoint == "" || modelID == "" {
		t.Fatal("DEEPSEEK_API_KEY, DEEPSEEK_BASE_URL and DEEPSEEK_MODEL are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	db, models, workspace, actor := modelFixture(t)
	models.Prober = &integration.ModelProber{}
	models.NormalizeEndpoint = integration.NormalizeModelEndpoint
	cfg := service.ModelConfig{Name: "Live DeepSeek acceptance", Protocol: os.Getenv("KIKO_LIVE_MODEL_PROTOCOL"), BaseURL: endpoint, ModelID: modelID, APIKey: &key}
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
	agent, err := (&service.AgentService{Store: NewAgentStore(db)}).Create(ctx, workspace, actor, service.AgentCreate{Name: "Live acceptance", ModelProfileID: model.ID, Instruction: "完成用户明确要求的项目操作。工具直接执行，不请求确认；不要访问网络或安装依赖。"})
	if err != nil {
		t.Fatal(err)
	}
	store := &managerrepo.Store{DB: db, TriggerSecret: []byte(strings.Repeat("t", 32))}
	svc := &manager.Service{Store: store, CursorSecret: []byte(strings.Repeat("c", 40))}
	engineToken := strings.Repeat("e", 40)
	cookie := "live-acceptance-cookie"
	if _, err = db.ExecContext(ctx, `INSERT INTO platform_sessions(token_hash,account_id,expires_at) VALUES($1,$2,$3)`, service.HashToken(cookie), actor, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	auth := &service.AuthService{Store: &AuthStore{DB: db}}
	mux := http.NewServeMux()
	(&managercontroller.Server{Service: svc, Engines: store, EngineToken: engineToken, Origin: "http://localhost", Authenticate: func(ctx context.Context, raw string) (string, error) { v, e := auth.Me(ctx, raw); return v.ID, e }, ResolveModelKey: func(ctx context.Context, w, a string, c manager.Configuration) (string, error) {
		key, e := models.RuntimeModelKey(ctx, w, a, c.ModelProfileID, c.Protocol, c.BaseURL, c.ModelID)
		if e != nil {
			t.Logf("runtime model authorization: %v", e)
		}
		return key, e
	}}).Routes(mux)
	handler, err := delegation.Handler(&manager.Delegation{Manager: svc, Store: store, Secret: engineToken}, nil)
	if err != nil {
		t.Fatal(err)
	}
	mux.Handle("/mcp/task-delegation", handler)
	backend := httptest.NewServer(mux)
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
		res, e := (&http.Client{Timeout: 5 * time.Second}).Do(r)
		if e != nil {
			t.Fatal("live Backend request failed")
		}
		defer func() { _ = res.Body.Close() }()
		var out map[string]any
		if e = json.NewDecoder(res.Body).Decode(&out); e != nil {
			t.Fatal(e)
		}
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			t.Fatalf("live Backend HTTP %d", res.StatusCode)
		}
		return out
	}
	temp := t.TempDir()
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		walkErr := filepath.Walk(temp, func(p string, info os.FileInfo, e error) error {
			if e != nil || info.IsDir() || !strings.HasSuffix(p, ".jsonl") {
				return nil
			}
			b, e := os.ReadFile(p)
			if e != nil {
				return nil
			}
			for _, line := range strings.Split(string(b), "\n") {
				var x struct {
					Message struct {
						Role         string `json:"role"`
						StopReason   string `json:"stopReason"`
						ErrorMessage string `json:"errorMessage"`
					} `json:"message"`
				}
				if json.Unmarshal([]byte(line), &x) == nil && x.Message.ErrorMessage != "" {
					t.Logf("native failure: %s %s", x.Message.StopReason, strings.ReplaceAll(x.Message.ErrorMessage, key, "[REDACTED]"))
				}
			}
			return nil
		})
		if walkErr != nil {
			t.Log("native diagnostics unavailable")
		}
	})
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
		cmd.Env = append(os.Environ(), "DATABASE_URL="+dsn.String(), "KIKO_BACKEND_URL="+backend.URL, "KIKO_ENGINE_TOKEN="+engineToken, "KIKO_REPO_ROOT="+root, "KIKO_STORAGE_LOCAL_ROOT="+temp, "KIKO_ENGINE_HOST_ID=live-test-host")
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

	group := service.NewID()
	for _, x := range []struct {
		q    string
		args []any
	}{
		{`INSERT INTO platform_web_groups(id,workspace_id,title,created_by)VALUES($1,$2,'live notice',$3)`, []any{group, workspace, actor}},
		{`INSERT INTO platform_web_group_agents(group_id,workspace_id,agent_id,status)VALUES($1,$2,$3,'active')`, []any{group, workspace, agent.ID}},
		{`UPDATE platform_agents SET usage_config=jsonb_set(usage_config,'{access}', '{"mode":"allowlist","group_members_enabled":false,"allow":[],"deny":[]}'::jsonb)WHERE id=$1`, []any{agent.ID}},
	} {
		if _, err = db.ExecContext(ctx, x.q, x.args...); err != nil {
			t.Fatal(err)
		}
	}
	target, err := store.ResolveTriggerGroupCandidate(ctx, workspace, actor, agent.ID, manager.TriggerGroupResolve{Provider: "web", CandidateID: group})
	if err != nil {
		t.Fatal(err)
	}
	conv := target.ConversationID

	targets := json.RawMessage(`[{"conversation_id":"` + conv + `"}]`)
	b, err := store.SaveTrigger(ctx, workspace, actor, agent.ID, "", manager.TriggerWrite{Kind: "webhook", Config: json.RawMessage(`{"adapter":"generic"}`), AllowedActions: []string{"create_task"}, NotificationTargets: targets})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := store.CreateTriggerCredential(ctx, workspace, actor, agent.ID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, err = store.SaveTrigger(ctx, workspace, actor, agent.ID, b.ID, manager.TriggerWrite{Version: b.Version, Kind: b.Kind, Enabled: true, Config: b.Config, AllowedActions: b.AllowedActions, NotificationTargets: targets})
	if err != nil {
		t.Fatal(err)
	}
	nonce := strings.ReplaceAll(service.NewID(), "-", "")
	command := manager.TriggerCommand{Version: 1, EventID: "live-hook", Action: "create_task", Description: "只操作当前项目、不访问网络。必须调用write工具创建 live-trigger.txt，内容为 " + nonce + "；再调用bash_start读取验证。完成回复 TRIGGER_LIVE_OK。"}
	receipt, err := signedTrigger(t, store, b, credential, command)
	if err != nil {
		t.Fatal(err)
	}
	again, err := signedTrigger(t, store, b, credential, command)
	if err != nil || !again.Duplicate || again.TaskID != receipt.TaskID {
		t.Fatal("dedup failed", err)
	}
	path := "/api/v1/workspaces/" + workspace + "/sessions/" + receipt.SessionID
	taskSpaceLiveAwaitTurn(t, ctx, db, call, path, 1)
	project := filepath.Join(temp, "state/sandboxes/workspaces", workspace, "employees", agent.ID, "taskspaces", receipt.TaskSpaceID, "tasks", receipt.TaskID, "project")
	proof, err := os.ReadFile(filepath.Join(project, "live-trigger.txt"))
	if err != nil || strings.TrimSpace(string(proof)) != nonce {
		t.Fatal("real tool proof failed", err)
	}
	var calls int
	if err = db.QueryRow(`SELECT count(DISTINCT event->>'name')FROM manager_session_events WHERE session_id=$1 AND event->>'type'='agent.tool_result' AND event->>'status'='completed' AND event->>'name' IN ('write','bash_start')`, receipt.SessionID).Scan(&calls); err != nil || calls != 2 {
		t.Fatal("native tools incomplete", calls, err)
	}
	deadline := time.Now().Add(20 * time.Second)
	var status, kind string
	var loops bool
	for time.Now().Before(deadline) {
		if err = store.MaterializeTriggerNotifications(ctx); err != nil {
			t.Fatal(err)
		}
		err = db.QueryRow(`SELECT n.status,i.source_kind,i.trigger_agent_loop FROM manager_trigger_notifications n JOIN manager_inputs i ON i.notification_id=n.id WHERE n.task_id=$1 AND n.source_input_id=$2`, receipt.TaskID, receipt.InputID).Scan(&status, &kind, &loops)
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil || status != "sent" || kind != "task_result" || loops {
		var facts string
		if e := db.QueryRow(`SELECT json_build_object('background',s.background_process_count,'active_turn',s.active_turn_id,'block',s.block_reason,'stop',s.stop_revision,'applied',s.applied_stop_revision,'input_status',i.status,'unknown',t.has_unknown_tool_results,'targets',(SELECT count(*) FROM manager_trigger_notification_targets WHERE task_id=s.task_id),'notices',(SELECT json_agg(json_build_object('status',n.status,'context',n.context_status,'error',n.context_error))FROM manager_trigger_notifications n WHERE n.task_id=s.task_id))::text FROM manager_sessions s JOIN manager_inputs i ON i.session_id=s.id LEFT JOIN manager_turns t ON t.id=i.turn_id WHERE i.id=$1`, receipt.InputID).Scan(&facts); e != nil {
			t.Fatal(e)
		}
		t.Fatal("notice not settled", status, kind, loops, err, facts)
	}
	t.Log("real local model: signed Hook dedup -> independent trigger TaskSpace -> native write/bash -> terminal Web notice -> task_result false; human usage denied")
}
