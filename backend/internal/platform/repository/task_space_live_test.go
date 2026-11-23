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
func TestTaskSpaceRuntimeLiveModel(t *testing.T) {
	if os.Getenv("KIKO_TASKSPACE_LIVE_E2E") != "1" {
		t.Skip("opt in with KIKO_TASKSPACE_LIVE_E2E=1 and DEEPSEEK_* credentials")
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

	base := "/api/v1/workspaces/" + workspace
	space := call("POST", base+"/task-spaces", map[string]string{"agent_id": agent.ID, "business_key": "live-taskspace"}, "live-space")["id"].(string)
	sessions := []string{}
	projects := []string{}
	nonces := []string{strings.ReplaceAll(service.NewID(), "-", ""), strings.ReplaceAll(service.NewID(), "-", "")}
	for n, nonce := range nonces {
		id := call("POST", base+"/sessions", map[string]any{"task_space_id": space, "agent_id": agent.ID, "mode": "task", "input": map[string]string{"text": "这是独立任务。请记住任务口令 " + nonce + "。必须调用write工具在项目根目录创建 live-proof.txt 内容为该口令，再用 bash_start 读取该文件验证内容。仅操作当前项目，不访问网络，不安装依赖。完成回复 LIVE_TASK_OK。"}}, "live-space-task-"+nonce)["id"].(string)
		sessions = append(sessions, id)
		taskSpaceLiveAwaitTurn(t, ctx, db, call, base+"/sessions/"+id, 1)
		var task string
		if err = db.QueryRowContext(ctx, `SELECT task_id FROM manager_sessions WHERE id=$1`, id).Scan(&task); err != nil {
			t.Fatal(err)
		}
		project := filepath.Join(temp, "workspaces", workspace, "projects", "task-"+task)
		projects = append(projects, project)
		proof, e := os.ReadFile(filepath.Join(project, "live-proof.txt"))
		if e != nil || strings.TrimSpace(string(proof)) != nonces[n] {
			t.Fatal("real task file proof failed", e)
		}
	}
	if projects[0] == projects[1] {
		t.Fatal("tasks share project")
	}
	var nativeCount int
	if err = db.QueryRowContext(ctx, `SELECT count(DISTINCT native_session_id) FROM manager_sessions WHERE task_space_id=$1`, space).Scan(&nativeCount); err != nil || nativeCount != 2 {
		t.Fatal("tasks share or lack native Pi session", err)
	}
	for _, id := range sessions {
		var calls int
		if err = db.QueryRowContext(ctx, `SELECT count(DISTINCT event->>'name') FROM manager_session_events WHERE session_id=$1 AND event->>'type'='agent.tool_result' AND event->>'status'='completed' AND event->>'name' IN ('write','bash_start')`, id).Scan(&calls); err != nil || calls != 2 {
			t.Fatal("real model did not complete write and bash tools", err)
		}
	}

	stop()
	stop = nil
	stop = start()
	for n, id := range sessions {
		path := base + "/sessions/" + id
		call("POST", path+"/inputs", map[string]string{"text": "不要调用任何工具，也不要读取文件。仅凭此任务已有对话历史回复此前告诉你的任务口令。"}, "live-restart-"+id)
		taskSpaceLiveAwaitTurn(t, ctx, db, call, path, 2)
		turns := call("GET", path+"/turns", nil, "")["items"].([]any)
		last, marshalErr := json.Marshal(turns[len(turns)-1])
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if !bytes.Contains(last, []byte(nonces[n])) || bytes.Contains(last, []byte(nonces[1-n])) {
			t.Fatal("native context restart/isolation mismatch")
		}
		proof, e := os.ReadFile(filepath.Join(projects[n], "live-proof.txt"))
		if e != nil || strings.TrimSpace(string(proof)) != nonces[n] {
			t.Fatal("restart lost project", e)
		}
	}
	call("POST", base+"/sessions/"+sessions[0]+"/archive", map[string]any{}, "")
	proof, e := os.ReadFile(filepath.Join(projects[1], "live-proof.txt"))
	if e != nil || strings.TrimSpace(string(proof)) != nonces[1] {
		t.Fatal("archiving sibling lost task project", e)
	}
	t.Log("real supplier TaskSpace passed: API creation, distinct task tools/files, Engine restart native history, sibling archive preservation")
}

func taskSpaceLiveAwaitTurn(t *testing.T, ctx context.Context, db *sql.DB, call func(string, string, any, string) map[string]any, session string, count int) {
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
				t.Fatalf("live Turn terminated: %#v", turns[count-1])
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
