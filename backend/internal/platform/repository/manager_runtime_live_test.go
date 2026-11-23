package repository

import (
	"bytes"
	"context"
	"database/sql"
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
func TestManagerRuntimeLiveDeepSeek(t *testing.T) {
	if os.Getenv("KIKO_LIVE_MODEL_E2E") != "1" {
		t.Skip("opt in with KIKO_LIVE_MODEL_E2E=1 and DEEPSEEK_* credentials")
	}
	key, endpoint, modelID := os.Getenv("DEEPSEEK_API_KEY"), os.Getenv("DEEPSEEK_BASE_URL"), os.Getenv("DEEPSEEK_MODEL")
	if key == "" || endpoint == "" || modelID == "" {
		t.Fatal("DEEPSEEK_API_KEY, DEEPSEEK_BASE_URL and DEEPSEEK_MODEL are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
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
	agent, err := (&service.AgentService{Store: NewAgentStore(db)}).Create(ctx, workspace, actor, service.AgentCreate{Name: "Live acceptance", ModelProfileID: model.ID, Instruction: "完成用户明确要求的项目操作。工具直接执行，不请求确认；不要访问网络或安装依赖。"})
	if err != nil {
		t.Fatal(err)
	}
	store := &managerrepo.Store{DB: db}
	svc := &manager.Service{Store: store, CursorSecret: []byte(strings.Repeat("c", 40))}
	engineToken := strings.Repeat("e", 40)
	cookie := "live-acceptance-cookie"
	if _, err = db.ExecContext(ctx, `INSERT INTO platform_sessions(token_hash,account_id,expires_at) VALUES($1,$2,$3)`, service.HashToken(cookie), actor, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	auth := &service.AuthService{Store: &AuthStore{DB: db}}
	mux := http.NewServeMux()
	(&managercontroller.Server{Service: svc, Engines: store, EngineToken: engineToken, Origin: "http://localhost", Authenticate: func(ctx context.Context, raw string) (string, error) { v, e := auth.Me(ctx, raw); return v.ID, e }, ResolveModelKey: func(ctx context.Context, w, a string, c manager.Configuration) (string, error) {
		return models.RuntimeModelKey(ctx, w, a, c.ModelProfileID, c.Protocol, c.BaseURL, c.ModelID)
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
	base := "/api/v1/workspaces/" + workspace
	conversation := call("POST", base+"/conversations", map[string]string{"agent_id": agent.ID}, "")["id"].(string)
	nonce := strings.ReplaceAll(service.NewID(), "-", "")
	chat := call("POST", base+"/sessions", map[string]any{"conversation_id": conversation, "mode": "chat", "input": map[string]string{"text": "请记住本次口令 " + nonce + "，只回复 OK，不调用工具。"}}, "live-chat-first")["id"].(string)
	chatPath := base + "/sessions/" + chat
	await := func(session string, count int) {
		t.Helper()
		liveAwaitTurn(t, ctx, db, call, session, count)
	}
	await(chatPath, 1)
	stop()
	stop = nil
	stop = start()
	call("POST", chatPath+"/inputs", map[string]string{"text": "上次告诉你的口令是什么？只回复口令，不调用工具。"}, "live-chat-restart")
	await(chatPath, 2)
	chatMessages := call("GET", chatPath+"/events?type=agent.message", nil, "")
	encoded, err := json.Marshal(chatMessages)
	if err != nil || !bytes.Contains(encoded, []byte(nonce)) {
		t.Fatal("real model did not retain native context across Engine restart")
	}
	project := filepath.Join(temp, "workspaces", workspace, "projects", "conv-"+conversation)
	task := call("POST", base+"/sessions", map[string]any{"conversation_id": conversation, "mode": "task", "input": map[string]string{"text": "必须实际调用 write 工具创建项目根目录 live-proof.txt，内容为 " + nonce + "。再用 bash_start 执行 python3 读取该文件，验证 strip() 后内容等于上述口令。不访问网络、不安装依赖，成功后只回复 LIVE_TASK_OK。"}}, "live-task")["id"].(string)
	await(base+"/sessions/"+task, 1)
	proof, err := os.ReadFile(filepath.Join(project, "live-proof.txt"))
	if err != nil || strings.TrimSpace(string(proof)) != nonce {
		t.Fatal("real model did not create the expected file through Sandbox tools")
	}
	var calls int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM manager_session_events WHERE session_id=$1 AND event->>'type'='agent.tool_result' AND event->>'status'='completed' AND event->>'name' IN ('write','bash_start')`, task).Scan(&calls); err != nil || calls < 2 {
		t.Fatal("real model did not complete both file and Bash tool calls")
	}
	call("POST", chatPath+"/inputs", map[string]string{"text": "请立即调用 task_delegation_create_session 委派当前员工，instruction 为：实际使用 write 工具在项目根目录创建 delegated-proof.txt，内容为 " + nonce + "，完成后回复 LIVE_DELEGATED_OK。创建成功后立刻返回委派的 session_id，不要等待或轮询，不要直接操作文件。"}, "live-delegation")
	await(chatPath, 3)
	var delegated string
	if err = db.QueryRowContext(ctx, `SELECT s.id FROM manager_sessions s JOIN manager_tasks t ON t.id=s.task_id WHERE t.source_chat_session_id=$1`, chat).Scan(&delegated); err != nil {
		t.Fatal("real chat did not create a delegated task through MCP")
	}
	await(base+"/sessions/"+delegated, 1)
	proof, err = os.ReadFile(filepath.Join(project, "delegated-proof.txt"))
	if err != nil || strings.TrimSpace(string(proof)) != nonce {
		t.Fatal("real delegated task did not write the shared project")
	}
	call("POST", chatPath+"/inputs", map[string]string{"text": "请调用 task_delegation_get_session 查询刚才委派的任务 " + delegated + " 的实际结果，确认后简短回复，不创建新任务。"}, "live-query")
	await(chatPath, 4)
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM manager_session_events WHERE session_id=$1 AND event->>'type'='agent.tool_result' AND event->>'name'='task_delegation_get_session' AND event->>'status'='completed'`, chat).Scan(&calls); err != nil || calls < 1 {
		t.Fatal("real chat did not query the delegated task through MCP")
	}
	call("POST", base+"/conversations/"+conversation+"/archive", map[string]any{}, "")
	until := time.Now().Add(15 * time.Second)
	for time.Now().Before(until) {
		v := call("GET", base+"/conversations/"+conversation, nil, "")
		if v["cleanup_pending"] == false {
			t.Log("live supplier passed: chat restart/history, file/Bash MCP, delegated task and query, archive")
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("live acceptance archive did not converge")
}

func liveAwaitTurn(t *testing.T, ctx context.Context, db *sql.DB, call func(string, string, any, string) map[string]any, session string, count int) {
	t.Helper()
	until := time.Now().Add(75 * time.Second)
	for time.Now().Before(until) {
		turns := call("GET", session+"/turns", nil, "")["items"].([]any)
		if len(turns) >= count {
			status := turns[count-1].(map[string]any)["status"]
			if status == "completed" {
				return
			}
			if status == "failed" || status == "canceled" || status == "interrupted" {
				t.Fatalf("live Turn terminated with status %s", status)
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal("live acceptance deadline exceeded")
		case <-time.After(150 * time.Millisecond):
		}
	}
	var block sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT block_reason FROM manager_sessions WHERE id=$1`, filepath.Base(session)).Scan(&block); err == nil {
		t.Logf("live Session block reason: %s", block.String)
	}
	t.Fatal("live Turn did not complete before timeout")
}
