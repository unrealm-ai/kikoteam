package repository

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	managercontroller "github.com/unrealm-ai/kikoteam/backend/internal/manager/controller"
	humanconfirmation "github.com/unrealm-ai/kikoteam/backend/internal/manager/mcp/humanconfirmation"
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/mcp/inputattachments"
	delegation "github.com/unrealm-ai/kikoteam/backend/internal/manager/mcp/taskdelegation"
	taskspacehistory "github.com/unrealm-ai/kikoteam/backend/internal/manager/mcp/taskspacehistory"
	workitems "github.com/unrealm-ai/kikoteam/backend/internal/manager/mcp/workitems"
	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	platformcontroller "github.com/unrealm-ai/kikoteam/backend/internal/platform/controller"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/integration/skillasset"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

const taskBFollowup = "task-read shared file and check background"

// This exercises real HTTP, PostgreSQL, a separate Engine executable and the
// unmodified upstream Pi RPC process. Only the model provider is deterministic.
func TestManagerRuntimeCapabilitiesEndToEnd(t *testing.T) { testManagerRuntime(t, "capabilities") }

func TestManagerRuntimeProjectFiles(t *testing.T) { testManagerRuntime(t, "files") }

func TestManagerRuntimeChatEndToEnd(t *testing.T) {
	testManagerRuntime(t, "chat")
}

func TestManagerRuntimeTaskReportReminderEndToEnd(t *testing.T) { testManagerRuntime(t, "report") }

func TestManagerRuntimeEmployeePromptsEndToEnd(t *testing.T) {
	testManagerRuntime(t, "prompts")
}

func TestManagerRuntimeEventWebhook(t *testing.T) { testManagerRuntime(t, "events") }

func TestManagerRuntimeHumanRequestsEndToEnd(t *testing.T) {
	testManagerRuntime(t, "human")
}

func TestManagerRuntimeWorkItemsEndToEnd(t *testing.T) { testManagerRuntime(t, "work") }

func TestManagerRuntimeWorkAmendmentsEndToEnd(t *testing.T) { testManagerRuntime(t, "work-amendment") }

func TestManagerRuntimeCrashTakeover(t *testing.T) { testManagerRuntime(t, "crash") }

func testManagerRuntime(t *testing.T, mode string) {
	t.Helper()
	workMode := mode == "work" || mode == "work-amendment"
	liveEventModel := mode == "events" && os.Getenv("KIKO_EVENT_MODEL_BASE_URL") != ""
	if os.Getenv("KIKO_RUNTIME_E2E") != "1" {
		t.Skip("set KIKO_RUNTIME_E2E=1 for actual Engine/Pi process acceptance")
	}
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(root, "agent-engine/node_modules/.bin/pi")); err != nil {
		t.Fatal("run npm ci in agent-engine")
	}
	var requestsMu sync.Mutex
	requests := 0
	var work workRuntimeFixture
	var amendments *workAmendmentRuntime
	var workMu sync.RWMutex
	askReady, releaseAsk := make(chan struct{}, 1), make(chan struct{})
	crashRequest := make(chan struct{}, 1)
	taskBModelReady, resumeTaskBModel := make(chan struct{}), make(chan struct{})
	var pauseTaskBModel sync.Once
	var reportCalls reportReminderCalls
	var eventRepo eventTestRepo
	if mode == "events" {
		eventRepo = newEventTestRepo(t)
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body runtimeRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if mode == "report" {
			reportCalls.capture(body)
		}
		if mode == "prompts" && employeePromptModelReply(t, w, body) {
			return
		}
		if mode == "events" && eventModelReply(t, w, body, eventRepo.after) {
			return
		}
		if mode == "files" && (asyncAttachmentModelReply(t, w, body) || projectModelReply(t, w, body)) {
			return
		}
		if mode == "crash" && len(body.Messages) > 0 && strings.Contains(fmt.Sprint(body.Messages[len(body.Messages)-1].Content), "crash-during-prompt") {
			select {
			case crashRequest <- struct{}{}:
			default:
			}
			<-r.Context().Done()
			return
		}
		if mode == "crash" {
			humanTextStream(t, w, "fixture completed")
			return
		}
		if confirmationModelReply(t, w, body) || runtimeCapabilityReply(t, w, body) {
			return
		}
		if workMode {
			workMu.RLock()
			fixture := work
			amendmentFixture := amendments
			workMu.RUnlock()
			if amendmentFixture != nil && amendmentFixture.reply(t, w, r, body) {
				return
			}
			if fixture.reply(t, w, r, body) {
				return
			}
		}
		if mode == "human" && runtimeHumanReply(t, w, body, func() bool {
			select {
			case askReady <- struct{}{}:
			case <-r.Context().Done():
				return false
			}
			select {
			case <-releaseAsk:
				return true
			case <-r.Context().Done():
				return false
			}
		}) {
			return
		}
		if mode == "chat" && len(body.Messages) > 0 {
			last := body.Messages[len(body.Messages)-1]
			if last.Role == "user" && strings.Contains(fmt.Sprint(last.Content), taskBFollowup) {
				// Keep the follow-up Turn active during Manager replay checks.
				pauseTaskBModel.Do(func() { close(taskBModelReady) })
				select {
				case <-resumeTaskBModel:
				case <-r.Context().Done():
					return
				}
			}
		}
		for _, tool := range body.Tools {
			if strings.HasPrefix(tool.Function.Name, "mcp__builtin__") {
				runtimeTaskReply(t, w, body)
				return
			}
		}
		for _, tool := range body.Tools {
			// These fixture Chat inputs come from their Web private-conversation
			// owner. Manager allows the recipient-side confirmation
			// tools here, not task-side confirmation tools or arbitrary MCPs.
			switch tool.Function.Name {
			case "mcp__human_confirmation__list_pending_human_confirmations", "mcp__human_confirmation__submit_human_confirmation", "mcp__human_confirmation__get_human_confirmation_receipt":
			default:
				if !strings.HasPrefix(tool.Function.Name, "mcp__task_delegation__") {
					t.Errorf("chat exposed an unapproved tool: %s", tool.Function.Name)
				}
			}
		}
		if runtimeDelegationReply(t, w, body) {
			return
		}
		requestsMu.Lock()
		requests++
		requestsMu.Unlock()
		last := fmt.Sprint(body.Messages[len(body.Messages)-1].Content)
		if strings.Contains(last, "steer-base") {
			select {
			case <-time.After(1500 * time.Millisecond):
			case <-r.Context().Done():
				return
			}
		}
		if strings.Contains(last, "slow-response") {
			<-r.Context().Done()
			return
		}
		userCount := 0
		remembered := false
		for _, v := range body.Messages {
			if v.Role == "user" {
				userCount++
				if strings.Contains(fmt.Sprint(v.Content), "amber") {
					remembered = true
				}
			}
		}
		text := fmt.Sprintf("reply:remembered=%v:users=%d", remembered, userCount)
		w.Header().Set("Content-Type", "text/event-stream")
		for _, v := range []any{map[string]any{"id": "runtime", "object": "chat.completion.chunk", "choices": []any{map[string]any{"index": 0, "delta": map[string]string{"role": "assistant", "content": text}, "finish_reason": nil}}}, map[string]any{"id": "runtime", "object": "chat.completion.chunk", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 12, "completion_tokens": 4, "total_tokens": 16}}} {
			data, err := json.Marshal(v)
			if err != nil {
				t.Error(err)
				return
			}
			if _, err = fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				return
			}
		}
		if _, err := fmt.Fprint(w, "data: [DONE]\n\n"); err != nil {
			return
		}
	}))
	defer provider.Close()
	db, models, workspace, actor := modelFixture(t)
	models.AllowLoopbackHTTP = true
	budget := 180 * time.Second
	if workMode || liveEventModel {
		budget = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	key := "fixture-key"
	modelConfig := service.ModelConfig{Name: "Runtime fixture", Protocol: "chat_completions", BaseURL: provider.URL + "/v1", ModelID: "fixture-model", APIKey: &key}
	if liveEventModel {
		u, e := url.Parse(os.Getenv("KIKO_EVENT_MODEL_BASE_URL"))
		if e != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || os.Getenv("KIKO_EVENT_MODEL_ID") == "" {
			t.Fatal("live event test needs a loopback test model and KIKO_EVENT_MODEL_ID")
		}
		modelConfig.BaseURL, modelConfig.ModelID = u.String(), os.Getenv("KIKO_EVENT_MODEL_ID")
	}
	probe, err := models.Test(ctx, workspace, actor, service.ModelTestRequest{ModelConfig: modelConfig})
	if err != nil {
		t.Fatal(err)
	}
	model, err := models.Save(ctx, workspace, actor, "", service.ModelSaveRequest{ModelConfig: modelConfig, TestReceipt: probe.Receipt})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = models.Change(ctx, workspace, actor, model.ID, "set-default", model.Version, true); err != nil {
		t.Fatal(err)
	}
	agents := &service.AgentService{Store: NewAgentStore(db)}
	employeeInput := service.AgentCreate{Name: "Runtime", ModelProfileID: model.ID}
	if workMode {
		cfg := modelConfig
		cfg.Name = "Original reviewer model"
		receipt, e := models.Test(ctx, workspace, actor, service.ModelTestRequest{ModelConfig: cfg})
		if e != nil {
			t.Fatal(e)
		}
		pinned, e := models.Save(ctx, workspace, actor, "", service.ModelSaveRequest{ModelConfig: cfg, TestReceipt: receipt.Receipt})
		if e != nil {
			t.Fatal(e)
		}
		employeeInput.ModelProfileID = pinned.ID
	}
	if mode == "prompts" {
		employeeInput.Role = "Fixture reports"
		employeeInput.Instruction = "Fixture employee rules"
	}
	agent, err := agents.Create(ctx, workspace, actor, employeeInput)
	if err != nil {
		t.Fatal(err)
	}
	if workMode {
		b, e := (&service.AgentService{Store: NewAgentStore(db)}).Create(ctx, workspace, actor, service.AgentCreate{Name: "Handoff B", ModelProfileID: model.ID})
		if e != nil {
			t.Fatal(e)
		}
		c, e := (&service.AgentService{Store: NewAgentStore(db)}).Create(ctx, workspace, actor, service.AgentCreate{Name: "Handoff C", ModelProfileID: model.ID})
		if e != nil {
			t.Fatal(e)
		}
		workMu.Lock()
		work = workRuntimeFixture{b: b.ID, c: c.ID, markUnknown: func() {
			_, err := db.ExecContext(ctx, `UPDATE manager_turns SET has_unknown_tool_results=true WHERE id IN (SELECT s.active_turn_id FROM manager_sessions s JOIN manager_work_assignments a ON a.session_id=s.id WHERE a.summary='WORK-UNKNOWN')`)
			if err != nil {
				t.Error(err)
			}
		}, firstResult: make(chan struct{}, 1), releaseResult: make(chan struct{})}
		if mode == "work-amendment" {
			amendments = newWorkAmendmentRuntime(b.ID)
		}
		workMu.Unlock()
	}

	token := "runtime-http-session"
	if _, err = db.ExecContext(ctx, `INSERT INTO platform_sessions(token_hash,account_id,expires_at) VALUES($1,$2,$3)`, service.HashToken(token), actor, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	engineToken := strings.Repeat("e", 40)
	store := &managerrepo.Store{DB: db, SnapshotCapabilities: SnapshotCapabilities, TriggerSecret: []byte(strings.Repeat("t", 32))}
	temp := t.TempDir()
	t.Setenv("KIKO_CONTEXT_ROOT", filepath.Join(temp, "context"))
	// Runner sockets must work even when Engine's temp root alone exceeds the Unix socket limit.
	t.Setenv("KIKO_RUNTIME_TEMP_ROOT", filepath.Join(temp, strings.Repeat("long-runtime-", 9)))
	store.RuntimeRoot = filepath.Join(temp, "state")
	capabilities := runtimeCapabilityServicesAt(t, db, store.RuntimeRoot)
	var eventCommitTool string
	if mode == "events" {
		eventCommitTool = installEventTestPlugin(t, ctx, db, capabilities, workspace, actor, agent.ID, eventRepo)
	}
	if workMode || mode == "files" {
		assets, e := skillasset.NewStore(ctx, skillasset.Config{LocalRoot: store.RuntimeRoot})
		if e != nil {
			t.Fatal(e)
		}
		store.WorkAssets = assets
		t.Cleanup(func() {
			if e := assets.Close(); e != nil {
				t.Error(e)
			}
		})
	}
	svc := &manager.Service{ProjectRoot: filepath.Join(temp, "state"), Store: store, CursorSecret: []byte(strings.Repeat("s", 32))}
	auth := &service.AuthService{Store: &AuthStore{DB: db}}
	mux := http.NewServeMux()
	mux.Handle("POST /api/v1/workspaces/{workspace_id}/agents/{agent_id}/capability-upgrades", (&platformcontroller.Server{Auth: auth, Packages: capabilities.Packages}).Handler())
	(&managercontroller.Server{Runtime: capabilities, Service: svc, Engines: store, EngineToken: engineToken, Origin: "http://localhost", Authenticate: func(ctx context.Context, raw string) (string, error) { v, err := auth.Me(ctx, raw); return v.ID, err }, ResolveModelKey: func(ctx context.Context, w, a string, c manager.Configuration) (string, error) {
		return models.RuntimeModelKey(ctx, w, a, c.ModelProfileID, c.Protocol, c.BaseURL, c.ModelID)
	}}).Routes(mux)
	delegationHandler, err := delegation.Handler(&manager.Delegation{Manager: svc, Store: store, Secret: engineToken}, nil)
	if err != nil {
		t.Fatal(err)
	}
	mux.Handle("/mcp/task-delegation", delegationHandler)
	inputattachments.Register(mux, store, svc, engineToken)
	if err := humanconfirmation.Register(mux, store, engineToken); err != nil {
		t.Fatal(err)
	}
	if err := workitems.Register(mux, store, engineToken); err != nil {
		t.Fatal(err)
	}
	if workMode || mode == "events" {
		if err := taskspacehistory.Register(mux, &manager.TaskSpaceHistory{Store: store, CursorSecret: svc.CursorSecret}, engineToken, nil); err != nil {
			t.Fatal(err)
		}
	}
	hubToken := strings.Repeat("h", 40)
	(&managercontroller.ChannelServer{Store: store, Manager: svc, Token: hubToken}).Routes(mux)
	backend := httptest.NewServer(mux)
	defer backend.Close()
	dispatchCtx, stopDispatch := context.WithCancel(ctx)
	dispatchDone := make(chan struct{})
	go func() {
		defer close(dispatchDone)
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			if err := errors.Join(store.ReconcileWorkItems(dispatchCtx), store.Dispatch(dispatchCtx), store.ReconcileWorkProgress(dispatchCtx)); err != nil && dispatchCtx.Err() == nil {
				t.Error(err)
			}
			select {
			case <-dispatchCtx.Done():
				return
			case <-tick.C:
			}
		}
	}()
	defer func() { stopDispatch(); <-dispatchDone }()
	client := &http.Client{Timeout: 5 * time.Second}
	callFor := func(cookie, method, path string, value any, key string) map[string]any {
		t.Helper()
		var data []byte
		if value != nil {
			var err error
			data, err = json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
		}
		r, err := http.NewRequestWithContext(ctx, method, backend.URL+path, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Cookie", "kiko_session="+cookie)
		r.Header.Set("X-CSRF-Protection", "1")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		res, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		var out map[string]any
		if err = json.NewDecoder(res.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			t.Fatalf("%s %s status=%d body=%v", method, path, res.StatusCode, out)
		}
		return out
	}
	call := func(method, path string, value any, key string) map[string]any {
		return callFor(token, method, path, value, key)
	}
	base := "/api/v1/workspaces/" + workspace
	var conversation, session string
	if mode != "events" {
		conversation = call("POST", base+"/conversations", map[string]any{"agent_id": agent.ID}, "")["id"].(string)
		session = call("POST", base+"/sessions", map[string]any{"conversation_id": conversation, "mode": "chat", "input": map[string]string{"text": "remember amber"}}, "first")["id"].(string)
	}
	sessionPath := base + "/sessions/" + session
	var schema string
	if err = db.QueryRowContext(ctx, "SELECT current_schema()").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	dsn, err := url.Parse(os.Getenv("BACKEND_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	q := dsn.Query()
	q.Set("search_path", schema)
	dsn.RawQuery = q.Encode()
	fixtureRoot := root
	if mode != "events" && mode != "report" {
		fixtureRoot = confirmationRuntimeRoot(t, root, temp)
	}
	binary := os.Getenv("KIKO_TEST_ENGINE_BINARY")
	if binary == "" {
		binary = filepath.Join(temp, "agent-engine")
		build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/agent-engine")
		build.Dir = filepath.Join(root, "agent-engine")
		if output, err := build.CombinedOutput(); err != nil {
			t.Fatalf("Engine build: %v %s", err, output)
		}
	}
	stopSignal := os.Interrupt
	host := "runtime-test"
	startEngine := func() func() {
		t.Helper()
		logs, err := os.CreateTemp(temp, "engine-*.log")
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(ctx, binary)
		cmd.Env = append(os.Environ(), "DATABASE_URL="+dsn.String(), "KIKO_BACKEND_URL="+backend.URL, "KIKO_ENGINE_TOKEN="+engineToken, "KIKO_REPO_ROOT="+fixtureRoot, "KIKO_STORAGE_LOCAL_ROOT="+store.RuntimeRoot, "KIKO_ENGINE_HOST_ID="+host, "KIKO_ENGINE_IDLE_TIMEOUT=2s")
		cmd.Stderr = logs
		if err = cmd.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		return func() {
			t.Helper()
			defer func() {
				if t.Failed() {
					data, e := os.ReadFile(logs.Name())
					if e == nil {
						t.Logf("Engine diagnostics: %s", data)
					}
				}
			}()
			if err := cmd.Process.Signal(stopSignal); err != nil {
				t.Log(err)
			}
			select {
			case err := <-done:
				if err != nil && stopSignal != os.Kill {
					content, readErr := os.ReadFile(logs.Name())
					if readErr != nil {
						t.Error(readErr)
					}
					t.Errorf("Engine exit: %v %s", err, content)
				}
			case <-time.After(45 * time.Second):
				_ = cmd.Process.Kill()
				if err := <-done; err != nil {
					t.Logf("forced Engine exit: %v", err)
				}
				t.Error("Engine shutdown timed out")
			}
			if err = logs.Close(); err != nil {
				t.Error(err)
			}
		}
	}
	stopEngine := startEngine()
	defer func() {
		if stopEngine != nil {
			stopEngine()
		}
	}()
	wait := func(test func() bool) {
		t.Helper()
		timeout := 25 * time.Second
		if workMode || liveEventModel {
			timeout = 100 * time.Second
		}
		until := time.Now().Add(timeout)
		for time.Now().Before(until) {
			if test() {
				return
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(100 * time.Millisecond):
			}
		}
		var diagnostics []byte
		if e := db.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(jsonb_build_object('status',status,'error',error)),'[]'::jsonb) FROM manager_turns WHERE session_id=$1`, session).Scan(&diagnostics); e == nil {
			t.Logf("Turn diagnostics: %s", diagnostics)
		}
		if e := db.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(jsonb_build_object('id',s.id,'mode',s.mode,'background',s.background_process_count,'block',s.block_reason,'events',(SELECT jsonb_agg(event) FROM manager_session_events e WHERE e.session_id=s.id AND event->>'type'='agent.tool_result'))),'[]'::jsonb) FROM manager_sessions s`).Scan(&diagnostics); e == nil {
			t.Logf("Session/tool diagnostics: %s", diagnostics)
		}
		t.Fatal("runtime state did not converge")
	}
	waitStatus := func(status string) {
		t.Helper()
		wait(func() bool {
			turns := call("GET", sessionPath+"/turns", nil, "")["items"].([]any)
			return len(turns) > 0 && turns[len(turns)-1].(map[string]any)["status"] == status
		})
	}
	if mode == "events" {
		eventWebhookAcceptance(t, ctx, db, store, mux, workspace, actor, agent.ID, eventCommitTool, eventRepo, wait)
		return
	}
	waitStatus("completed")
	if mode == "report" {
		verifyRuntimeReportReminder(t, ctx, db, session, sessionPath, call, wait, &reportCalls)
		return
	}
	if mode == "capabilities" {
		runtimeCapabilityAcceptance(t, ctx, db, capabilities, workspace, actor, agent.ID, temp, call, wait, func() { stopEngine(); stopEngine = startEngine() })
		return
	}
	if mode == "prompts" {
		testRuntimeEmployeePrompts(t, ctx, capabilities, agents, agent, workspace, actor, conversation, session, call, wait)
		return
	}
	if mode == "crash" {
		task := call("POST", base+"/sessions", map[string]any{"conversation_id": conversation, "mode": "task", "input": map[string]string{"text": "crash-during-prompt"}}, "crash")["id"].(string)
		select {
		case <-crashRequest:
		case <-ctx.Done():
			t.Fatal("task did not reach the provider")
		}
		stopSignal = os.Kill
		stopEngine()
		stopEngine = nil
		host, stopSignal = "replacement-host", os.Interrupt
		stopEngine = startEngine()
		taskPath := base + "/sessions/" + task
		wait(func() bool {
			items := call("GET", taskPath+"/turns", nil, "")["items"].([]any)
			return len(items) == 1 && items[0].(map[string]any)["status"] == "interrupted"
		})
		call("POST", taskPath+"/inputs", map[string]string{"text": "continue after restart"}, "continue-after-crash")
		wait(func() bool {
			items := call("GET", taskPath+"/turns", nil, "")["items"].([]any)
			return len(items) == 2 && items[1].(map[string]any)["status"] == "completed"
		})
		return
	}
	if mode == "files" {
		testRuntimeProjectFiles(t, ctx, db, base, workspace, agent.ID, svc.ProjectRoot, backend.URL, token, call, wait)
		testRuntimeAsyncAttachment(t, ctx, db, base, agent.ID, backend.URL, token, call, wait)
		return
	}
	if mode == "work" {
		testRuntimeWorkItems(t, ctx, db, store, base, workspace, actor, agent.ID, conversation, temp, call, wait, work)
		return
	}
	if mode == "work-amendment" {
		testRuntimeWorkAmendments(t, ctx, db, sessionPath, call, wait, amendments)
		return
	}
	if mode == "human" {
		testRuntimeHumanRequests(t, ctx, db, base, workspace, agent.ID, temp, call, callFor, wait, askReady, releaseAsk)
		return
	}
	testRuntimeConfirmations(t, ctx, db, base, agent.ID, call, wait)
	call("POST", sessionPath+"/inputs", map[string]string{"text": "what did I say?"}, "second")
	wait(func() bool {
		turns := call("GET", sessionPath+"/turns", nil, "")["items"].([]any)
		return len(turns) == 2 && turns[1].(map[string]any)["status"] == "completed"
	})
	stopEngine()
	stopEngine = nil
	stopEngine = startEngine()
	call("POST", sessionPath+"/inputs", map[string]string{"text": "after restart"}, "third")
	wait(func() bool {
		turns := call("GET", sessionPath+"/turns", nil, "")["items"].([]any)
		return len(turns) == 3 && turns[2].(map[string]any)["status"] == "completed"
	})
	call("POST", sessionPath+"/inputs", map[string]string{"text": "slow-response"}, "slow")
	waitStatus("running")
	call("POST", sessionPath+"/stop", map[string]any{}, "")
	waitStatus("canceled")
	call("POST", sessionPath+"/inputs", map[string]string{"text": "continue"}, "continue")
	wait(func() bool {
		turns := call("GET", sessionPath+"/turns", nil, "")["items"].([]any)
		return len(turns) == 5 && turns[4].(map[string]any)["status"] == "completed"
	})
	events := call("GET", sessionPath+"/events?type=agent.message", nil, "")
	encoded, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), "reply:remembered=true:users=3") {
		t.Fatalf("native history not retained: %s", encoded)
	}
	turns := call("GET", sessionPath+"/turns", nil, "")["items"].([]any)
	firstTurn := turns[0].(map[string]any)["id"].(string)
	inputs := call("GET", sessionPath+"/events?type=input.updated&turn_id="+firstTurn, nil, "")["items"].([]any)
	if len(inputs) == 0 {
		t.Fatal("turn-filtered input history empty")
	}
	for _, raw := range inputs {
		event := raw.(map[string]any)
		content, ok := event["content"].([]any)
		if !ok || len(content) == 0 || !strings.Contains(content[0].(map[string]any)["text"].(string), "amber") {
			t.Fatal("turn-filtered input snapshot lost original content", event)
		}
	}
	streamCtx, closeStream := context.WithCancel(ctx)
	request, err := http.NewRequestWithContext(streamCtx, "GET", backend.URL+sessionPath+"/events/stream?type=agent.message", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Cookie", "kiko_session="+token)
	stream, err := (&http.Client{}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(stream.Body)
	found := false
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "data:") {
			found = true
			break
		}
	}
	closeStream()
	_ = stream.Body.Close()
	if !found {
		t.Fatal("no reliable SSE message")
	}
	// A real native steer joins the running platform Turn; queue waits for another.
	rootInput := call("POST", sessionPath+"/inputs", map[string]string{"text": "steer-base"}, "steer-base")["id"].(string)
	waitStatus("running")
	steerInput := call("POST", sessionPath+"/inputs", map[string]string{"text": "steer-new"}, "steer-new")["id"].(string)
	queuedInput := call("POST", sessionPath+"/inputs", map[string]string{"text": "queue-new", "delivery_mode": "queue"}, "queue-new")["id"].(string)
	wait(func() bool {
		var finished bool
		e := db.QueryRowContext(ctx, `SELECT count(*)=3 FROM manager_inputs WHERE id IN ($1,$2,$3) AND status='processed'`, rootInput, steerInput, queuedInput).Scan(&finished)
		return e == nil && finished
	})
	var joined bool
	if e := db.QueryRowContext(ctx, `SELECT a.turn_id=b.turn_id AND a.turn_id<>c.turn_id AND b.context_state='committed' FROM manager_inputs a,manager_inputs b,manager_inputs c WHERE a.id=$1 AND b.id=$2 AND c.id=$3`, rootInput, steerInput, queuedInput).Scan(&joined); e != nil || !joined {
		t.Fatal("steer did not join current Turn or queue ran early", joined, e)
	}
	waitConversationQuiescent(t, ctx, db, conversation, wait)
	call("POST", base+"/conversations/"+conversation+"/archive", map[string]any{}, "")
	wait(func() bool {
		v := call("GET", sessionPath, nil, "")
		return v["lifecycle"] == "archived" && v["execution"].(map[string]any)["state"] == "idle"
	})
	requestsMu.Lock()
	count := requests
	requestsMu.Unlock()
	if count < 4 {
		t.Fatal("missing actual model protocol requests")
	}
	// A second Conversation exercises the complete bare Sandbox path: two
	// independent native contexts, a shared project and per-Session processes.
	conversation = call("POST", base+"/conversations", map[string]any{"agent_id": agent.ID}, "")["id"].(string)
	createTask := func(text, key string) string {
		t.Helper()
		id := call("POST", base+"/sessions", map[string]any{"conversation_id": conversation, "mode": "task", "input": map[string]string{"text": text}}, key)["id"].(string)
		return base + "/sessions/" + id
	}
	a := createTask("task-A secret-amber start background and write shared", "task-a")
	b := createTask("task-B start background", "task-b")
	background := func(path string) float64 {
		t.Helper()
		v := call("GET", path, nil, "")["execution"].(map[string]any)
		if n, ok := v["background_process_count"].(float64); ok {
			return n
		}
		return -1
	}
	wait(func() bool { return background(a) == 1 && background(b) == 1 })
	time.Sleep(2500 * time.Millisecond)
	if background(a) != 1 || background(b) != 1 {
		t.Fatal("idle release terminated a Session with live background commands")
	}
	project := filepath.Join(temp, "state", "workspaces", workspace, "projects", "conv-"+conversation)
	data, err := os.ReadFile(filepath.Join(project, "shared.txt"))
	if err != nil || string(data) != "shared-value" {
		t.Fatalf("shared project file %q %v", data, err)
	}
	// Task A's Bash command reports its own binding directory; B uses a separate one.
	bindingDir, err := os.ReadFile(filepath.Join(project, "task-a-tmpdir.txt"))
	if err != nil {
		t.Fatal(err)
	}
	aSocket := filepath.Join(string(bindingDir), "m.sock")
	if info, err := os.Stat(aSocket); err != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatal("missing live socket for task A", err)
	}
	call("POST", a+"/stop", map[string]any{}, "")
	wait(func() bool { return background(a) == 0 })
	if background(b) != 1 {
		t.Fatal("stopping task A also stopped task B")
	}
	// Idle release must close only A's MCP listener as well as its Pi. A new
	// Input rebuilds both against the same native context, ledger and token.
	wait(func() bool {
		var engine *string
		err := db.QueryRowContext(ctx, `SELECT engine_id FROM manager_sessions WHERE id=$1`, filepath.Base(a)).Scan(&engine)
		return err == nil && engine == nil
	})
	if _, err := os.Stat(aSocket); !os.IsNotExist(err) {
		t.Fatal("idle Session retained its management socket", err)
	}
	stateDir := filepath.Join(store.RuntimeRoot, "workspaces", workspace, "sessions", filepath.Base(a), "runner")
	if _, err := os.Stat(filepath.Join(stateDir, "launch.json")); !os.IsNotExist(err) {
		t.Fatal("private launch configuration persisted", err)
	}
	tokenBefore, err := os.ReadFile(filepath.Join(stateDir, "token"))
	if err != nil {
		t.Fatal(err)
	}
	call("POST", a+"/inputs", map[string]string{"text": "task-read after idle release"}, "a-after-idle")
	wait(func() bool {
		v := call("GET", a+"/turns", nil, "")["items"].([]any)
		return len(v) == 2 && v[1].(map[string]any)["status"] == "completed"
	})
	tokenAfter, err := os.ReadFile(filepath.Join(stateDir, "token"))
	if err != nil || !bytes.Equal(tokenBefore, tokenAfter) {
		t.Fatal("idle MCP release rotated the fixed token")
	}
	func() {
		defer close(resumeTaskBModel)
		input := call("POST", b+"/inputs", map[string]string{"text": taskBFollowup}, "read-shared")["id"].(string)
		wait(func() bool {
			select {
			case <-taskBModelReady:
				return true
			default:
				return false
			}
		})
		assertTaskBRejectsOldWorkToken(t, ctx, db, mux, engineToken, filepath.Base(b), input)
	}()
	var followupTurn string
	wait(func() bool {
		v := call("GET", b+"/turns", nil, "")["items"].([]any)
		if len(v) != 2 || v[1].(map[string]any)["status"] != "completed" {
			return false
		}
		followupTurn = v[1].(map[string]any)["id"].(string)
		return true
	})
	events = call("GET", b+"/events?type=agent.message", nil, "")
	encoded, err = json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), "shared=true;secret=false") {
		t.Fatalf("shared files or independent context failed: %s", encoded)
	}
	if background(b) != 1 {
		t.Fatal("Task B lost its live background command during the follow-up")
	}
	assertTaskBBackgroundAndWorkTool(t, followupTurn, call("GET", b+"/events?type=agent.tool_result", nil, ""))
	// Normal shutdown stops live groups and permits same-host Engine transfer.
	stopEngine()
	stopEngine = nil
	tokenPath := filepath.Join(store.RuntimeRoot, "workspaces", workspace, "sessions", filepath.Base(b), "runner", "token")
	before, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	stopEngine = startEngine()
	call("POST", b+"/inputs", map[string]string{"text": "task-read after restart"}, "task-restart")
	wait(func() bool {
		v := call("GET", b+"/turns", nil, "")["items"].([]any)
		return len(v) == 3 && v[2].(map[string]any)["status"] == "completed"
	})
	after, err := os.ReadFile(tokenPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("Sandbox-lifetime token changed across restart")
	}
	waitConversationQuiescent(t, ctx, db, conversation, wait)
	call("POST", base+"/conversations/"+conversation+"/archive", map[string]any{}, "")
	wait(func() bool {
		var state string
		if err := db.QueryRowContext(ctx, `SELECT state FROM manager_conversation_sandboxes WHERE conversation_id=$1`, conversation).Scan(&state); err != nil {
			t.Fatal(err)
		}
		return state == "stopped"
	})
	if _, err = os.Stat(filepath.Join(project, "shared.txt")); err != nil {
		t.Fatal("archive removed project files", err)
	}
	// The source chat uses actual MCP tools through the unchanged Pi client.
	conversation = call("POST", base+"/conversations", map[string]any{"agent_id": agent.ID}, "")["id"].(string)
	chat := call("POST", base+"/sessions", map[string]any{"conversation_id": conversation, "mode": "chat", "input": map[string]string{"text": "delegate-create"}}, "delegate-create")["id"].(string)
	chatPath := base + "/sessions/" + chat
	wait(func() bool {
		v := call("GET", chatPath+"/turns", nil, "")["items"].([]any)
		return len(v) == 1 && v[0].(map[string]any)["status"] == "completed"
	})
	var delegated string
	if err = db.QueryRowContext(ctx, `SELECT s.id FROM manager_sessions s JOIN manager_tasks t ON t.id=s.task_id WHERE t.source_chat_session_id=$1`, chat).Scan(&delegated); err != nil {
		t.Fatal("MCP did not atomically create delegated task", err)
	}
	wait(func() bool {
		v := call("GET", base+"/sessions/"+delegated+"/turns", nil, "")["items"].([]any)
		return len(v) == 1 && v[0].(map[string]any)["status"] == "completed"
	})
	// Actual Engine HTTP callback admits and executes a source-labelled report.
	wait(func() bool {
		var reported bool
		e := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM manager_task_reports r JOIN manager_inputs i ON i.id=r.target_input_id WHERE r.target_session_id=$1 AND i.status='processed' AND i.context_state='committed' AND i.source_kind='task_report')`, chat).Scan(&reported)
		return e == nil && reported
	})
	for _, text := range []string{"delegate-list", "delegate-query", "delegate-send", "delegate-stop"} {
		call("POST", chatPath+"/inputs", map[string]string{"text": text, "delivery_mode": "queue"}, text)
		wait(func() bool {
			var complete bool
			err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM manager_inputs i JOIN manager_turns t ON t.id=i.turn_id WHERE i.session_id=$1 AND i.text=$2 AND i.status='processed' AND t.status='completed')`, chat, text).Scan(&complete)
			return err == nil && complete
		})
	}
	events = call("GET", chatPath+"/events?type=agent.tool_result", nil, "")
	encoded, err = json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"status":"failed"`) {
		t.Fatalf("delegation tool failed: %s", encoded)
	}
	for _, raw := range events["items"].([]any) {
		event := raw.(map[string]any)
		for _, field := range []string{"tool_use_id", "call_no", "name", "title", "source", "status", "input", "output", "input_state", "output_state", "summary", "error", "started_at", "finished_at"} {
			if _, ok := event[field]; !ok {
				t.Fatalf("tool result snapshot missing %s: %v", field, event)
			}
		}
	}
	uses := call("GET", chatPath+"/events?type=agent.tool_use", nil, "")["items"].([]any)
	for _, raw := range uses {
		status := raw.(map[string]any)["status"]
		if status != "pending" && status != "running" {
			t.Fatal("terminal status emitted as tool_use", status)
		}
	}
	waitConversationQuiescent(t, ctx, db, conversation, wait)
	call("POST", base+"/conversations/"+conversation+"/archive", map[string]any{}, "")
	wait(func() bool {
		var state string
		err := db.QueryRowContext(ctx, `SELECT state FROM manager_conversation_sandboxes WHERE conversation_id=$1`, conversation).Scan(&state)
		return err == nil && state == "stopped"
	})
	runtimeChannelAcceptance(t, ctx, db, store, svc, workspace, agent.ID, backend.URL, hubToken, wait)
}

// The follow-up must see the same builtin process, not just another running
// process; it also needs valid Work MCP credentials for the current Input.
func assertTaskBBackgroundAndWorkTool(t *testing.T, followupTurn string, events map[string]any) {
	t.Helper()
	data, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	type command struct {
		ID    string `json:"process_id"`
		State string `json:"state"`
	}
	var history struct {
		Items []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			TurnID string `json:"turn_id"`
			Output struct {
				Wrapped struct {
					Result struct {
						command
						Items []command `json:"items"`
					} `json:"structuredContent"`
				} `json:"structuredContent"`
			} `json:"output"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &history); err != nil {
		t.Fatal(err)
	}
	var started string
	var running []command
	var workAuthorized bool
	for _, event := range history.Items {
		if event.Status != "completed" {
			continue
		}
		switch event.Name {
		case "bash_start":
			if event.TurnID != followupTurn {
				started = event.Output.Wrapped.Result.ID
			}
		case "bash_list":
			if event.TurnID == followupTurn {
				running = event.Output.Wrapped.Result.Items
			}
		case "work_item_list_handoff_employees":
			workAuthorized = workAuthorized || event.TurnID == followupTurn
		}
	}
	if !workAuthorized {
		t.Fatal("Task B follow-up did not successfully call the current Input's Work MCP tool")
	}
	for _, entry := range running {
		if started != "" && entry.ID == started && entry.State == "running" {
			return
		}
	}
	t.Fatal("Task B follow-up did not see its original background process still running")
}

// A still-valid signature for the preceding Input may not authorize any tool
// call in the active Turn, whether the preceding or the current ID is sent.
func assertTaskBRejectsOldWorkToken(t *testing.T, ctx context.Context, db *sql.DB, handler http.Handler, secret, session, currentInput string) {
	t.Helper()
	id := manager.DelegationIdentity{SessionID: session}
	var oldInput string
	if err := db.QueryRowContext(ctx, `SELECT s.engine_id,t.assignment_id,t.generation,t.input_id
FROM manager_turns t JOIN manager_sessions s ON s.id=t.session_id
JOIN manager_turns active ON active.id=s.active_turn_id
WHERE s.id=$1 AND active.input_id=$2 AND active.status='running' AND t.turn_no=1
  AND t.assignment_id=active.assignment_id AND t.generation=active.generation`, session, currentInput).
		Scan(&id.EngineID, &id.AssignmentID, &id.Generation, &oldInput); err != nil {
		t.Fatal("Task B did not retain the prior assignment during its follow-up:", err)
	}
	token, err := manager.SignWorkTool(secret, "mcp-ordinary", oldInput, id)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{oldInput, currentInput} {
		request := httptest.NewRequest(http.MethodPost, "/mcp/work-items/ordinary", strings.NewReader(
			`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"work_item_list_handoff_employees","arguments":{}}}`))
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set(manager.WorkInputHeader, input)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
		request.Header.Set("MCP-Protocol-Version", "2025-11-25")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if input == currentInput {
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("old Work token bound to a new Input: HTTP %d", response.Code)
			}
			continue
		}
		var rpc struct {
			Result struct {
				IsError           bool `json:"isError"`
				StructuredContent struct {
					Error struct {
						Code string `json:"code"`
					} `json:"error"`
				} `json:"structuredContent"`
			} `json:"result"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &rpc); err != nil || response.Code != http.StatusOK ||
			!rpc.Result.IsError || rpc.Result.StructuredContent.Error.Code != "work_tool_forbidden" {
			t.Fatalf("preceding Input's exact Work credential must be rejected by active-Turn authorization: HTTP %d, error %t, code %q, parse %v",
				response.Code, rpc.Result.IsError, rpc.Result.StructuredContent.Error.Code, err)
		}
	}
}
