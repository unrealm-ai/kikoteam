package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	controller "github.com/unrealm-ai/kikoteam/backend/internal/manager/controller"
	delegation "github.com/unrealm-ai/kikoteam/backend/internal/manager/mcp/taskdelegation"
	repository "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func TestDelegationScopeAtomicDedupAndRunnerGrants(t *testing.T) {
	db, s, _, w, actor, agent, _ := managerFixture(t)
	ctx := context.Background()
	store := &repository.Store{DB: db}
	secret := strings.Repeat("e", 40)
	c, err := s.CreateConversation(ctx, w, actor, manager.CreateConversation{AgentID: agent})
	if err != nil {
		t.Fatal(err)
	}
	req := manager.CreateSession{ConversationID: c.ID, Mode: "chat"}
	req.Input = &struct {
		Text string `json:"text"`
	}{Text: "delegate"}
	source, _, err := s.CreateSession(ctx, w, actor, "source-input", req)
	if err != nil {
		t.Fatal(err)
	}
	var initialInstruction string
	if err = db.QueryRowContext(ctx, `SELECT configuration->>'instruction' FROM manager_sessions WHERE id=$1`, source.ID).Scan(&initialInstruction); err != nil || initialInstruction != manager.PlatformChatInstruction {
		t.Fatalf("new chat did not snapshot platform policy: %v", err)
	}
	engine := manager.NewID()
	if err = store.RegisterEngine(ctx, manager.Engine{ID: engine, HostID: "test-host", Endpoint: "http://127.0.0.1:9001", Capacity: 20}); err != nil {
		t.Fatal(err)
	}
	if err = store.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	identity := manager.DelegationIdentity{SessionID: source.ID, EngineID: engine}
	if err = db.QueryRowContext(ctx, `SELECT assignment_id,generation FROM manager_sessions WHERE id=$1`, source.ID).Scan(&identity.AssignmentID, &identity.Generation); err != nil {
		t.Fatal(err)
	}
	turn := manager.NewID()
	if _, err = db.ExecContext(ctx, `INSERT INTO manager_turns(id,workspace_id,session_id,input_id,turn_no,assignment_id,generation,status) VALUES($1,$2,$3,$4,1,$5,$6,'running')`, turn, w, source.ID, *source.InitialInputID, identity.AssignmentID, identity.Generation); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE manager_inputs SET status='started',turn_id=$2 WHERE id=$1`, *source.InitialInputID, turn); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE manager_sessions SET active_turn_id=$2,last_turn_no=1 WHERE id=$1`, source.ID, turn); err != nil {
		t.Fatal(err)
	}
	// A pre-upgrade chat receives the latest platform policy on its next
	// authorized config read, without mutating its persisted snapshot.
	if _, err = db.ExecContext(ctx, `UPDATE manager_sessions SET configuration=jsonb_set(configuration,'{instruction}','"legacy chat policy"') WHERE id=$1`, source.ID); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	(&controller.Server{Service: s, Engines: store, EngineToken: secret, ResolveModelKey: func(_ context.Context, _, _ string, _ manager.Configuration) (string, error) {
		return "fixture-key", nil
	}}).Routes(mux)
	internal := func(method, path, credential, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+credential)
		out := httptest.NewRecorder()
		mux.ServeHTTP(out, r)
		return out
	}
	path := "/api/internal/v1/sessions/" + source.ID + "/harness-config?engine_id=" + engine + "&assignment_id=" + identity.AssignmentID + "&generation=" + strconv.FormatInt(identity.Generation, 10) + "&input_id=" + *source.InitialInputID
	response := internal("GET", path, secret, "")
	var cfg manager.HarnessConfig
	if err = json.Unmarshal(response.Body.Bytes(), &cfg); err != nil || response.Code != 200 || !strings.Contains(cfg.Configuration.Instruction, manager.PlatformChatInstruction) || !strings.Contains(cfg.Configuration.Instruction, "private employee instruction") || strings.Contains(cfg.Configuration.Instruction, "legacy chat policy") {
		t.Fatalf("legacy chat prompt not refreshed: status=%d, prompt=%q, err=%v", response.Code, cfg.Configuration.Instruction, err)
	}
	var saved string
	if err = db.QueryRowContext(ctx, `SELECT configuration->>'instruction' FROM manager_sessions WHERE id=$1`, source.ID).Scan(&saved); err != nil || saved != "legacy chat policy" {
		t.Fatalf("runtime policy rewrote history snapshot: %v", err)
	}
	token, err := manager.SignDelegation(secret, identity)
	if err != nil {
		t.Fatal(err)
	}
	d := &manager.Delegation{Manager: s, Store: store, Secret: secret}
	scope, err := d.Authorize(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	failures := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			value, e := d.Call(ctx, scope, "create_session", manager.DelegationArgs{Instruction: "one task"})
			if e != nil {
				failures <- e
				return
			}
			ids <- value.(map[string]any)["session_id"].(string)
		}()
	}
	wg.Wait()
	close(ids)
	close(failures)
	for e := range failures {
		t.Error(e)
	}
	if t.Failed() {
		return
	}
	target := ""
	for id := range ids {
		if target != "" && target != id {
			t.Fatal("concurrent MCP retry created another task")
		}
		target = id
	}
	webTasks, err := s.ListTasks(ctx, w, actor, "", manager.TaskFilter{Conversation: c.ID})
	if err != nil || len(webTasks.Items) != 1 || webTasks.Items[0].SessionID != target || webTasks.Items[0].Source.ChatSessionID == nil || *webTasks.Items[0].Source.ChatSessionID != source.ID {
		t.Fatalf("MCP task not projected into Web task records: %v %v", webTasks, err)
	}
	webDetail, err := s.GetTask(ctx, w, actor, webTasks.Items[0].TaskID)
	if err != nil || webDetail.SessionID != target || webDetail.InitialInput == nil {
		t.Fatalf("MCP/Web detail identity mismatch: %v %v", webDetail, err)
	}
	own, _, err := s.CreateSession(ctx, w, actor, "", manager.CreateSession{ConversationID: c.ID, Mode: "task"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.Call(ctx, scope, "get_session", manager.DelegationArgs{SessionID: own.ID}); err == nil {
		t.Fatal("undelegated same-Conversation task visible")
	}
	if _, err = d.Call(ctx, scope, "create_session", manager.DelegationArgs{EmployeeID: manager.NewID(), Instruction: "other employee"}); err == nil {
		t.Fatal("cross-employee delegation allowed")
	}
	page, err := d.Call(ctx, scope, "list_sessions", manager.DelegationArgs{})
	if err != nil || len(page.(manager.Page[manager.Session]).Items) != 1 {
		t.Fatalf("delegated list %v %v", page, err)
	}
	// Supplement keeps the original Session and exact authorized constraints;
	// an unknown-result retry adds no duplicate Input or new Task.
	const supplement = "只读取报告；不要修改文件，也不要发送。补充统计近七天数据。"
	for range 2 {
		out, e := d.Call(ctx, scope, "send_message", manager.DelegationArgs{SessionID: target, Text: supplement})
		if e != nil || out.(manager.Input).Status != "queued" {
			t.Fatalf("supplement not queued: %v %v", out, e)
		}
	}
	var inputs, tasks int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM manager_inputs WHERE session_id=$1 AND text=$2`, target, supplement).Scan(&inputs); err != nil || inputs != 1 {
		t.Fatalf("supplement changed content or duplicated: %d %v", inputs, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM manager_tasks WHERE source_chat_session_id=$1`, source.ID).Scan(&tasks); err != nil || tasks != 1 {
		t.Fatalf("supplement created another Task: %d %v", tasks, err)
	}
	h, err := delegation.Handler(d, nil)
	if err != nil {
		t.Fatal(err)
	}
	callTool := func(rpcID, name, args string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest("POST", "http://localhost/mcp/task-delegation", strings.NewReader(`{"jsonrpc":"2.0","id":`+rpcID+`,"method":"tools/call","params":{"name":"`+name+`","arguments":`+args+`}}`))
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
		request.Header.Set("MCP-Protocol-Version", "2025-11-25")
		out := httptest.NewRecorder()
		h.ServeHTTP(out, request)
		return out
	}
	var original map[string]any
	for _, rpcID := range []string{"1", "2"} {
		out := callTool(rpcID, "task_delegation_create_session", `{"instruction":"one task"}`)
		var receipt struct {
			Result struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
				Structured map[string]any `json:"structuredContent"`
				IsError    bool           `json:"isError"`
			} `json:"result"`
		}
		if err := json.Unmarshal(out.Body.Bytes(), &receipt); err != nil || out.Code != 200 || receipt.Result.IsError {
			t.Fatalf("MCP retry failed: %d %s", out.Code, out.Body.String())
		}
		v := receipt.Result.Structured
		if len(v) != 3 || v["accepted"] != true || v["session_id"] != nil || v["task_id"] != webTasks.Items[0].TaskID ||
			!manager.ValidID(fmt.Sprint(v["task_id"])) || !manager.ValidID(fmt.Sprint(v["input_id"])) {
			t.Fatalf("invalid MCP acceptance payload: %v", v)
		}
		var text map[string]any
		if len(receipt.Result.Content) != 1 || receipt.Result.Content[0].Type != "text" ||
			json.Unmarshal([]byte(receipt.Result.Content[0].Text), &text) != nil || !reflect.DeepEqual(v, text) {
			t.Fatalf("MCP model text diverged from the accepted receipt: %s", out.Body.String())
		}
		if original == nil {
			original = v
		} else if !reflect.DeepEqual(original, v) {
			t.Fatalf("MCP replay changed accepted IDs: %v, %v", original, v)
		}
	}
	for _, field := range []string{`"workspace_id":"forged"`, `"employee_id":"` + agent + `"`, `"request_key":"caller-supplied"`} {
		response = callTool("3", "task_delegation_create_session", `{"instruction":"x",`+field+`}`)
		if response.Code != 200 || !strings.Contains(response.Body.String(), `"isError":true`) ||
			strings.Contains(response.Body.String(), `"accepted":true`) {
			t.Fatalf("unexpected field %s accepted: %d %s", field, response.Code, response.Body.String())
		}
	}
	// An old in-flight selector is accepted, but even its result only exposes the task ID.
	legacy := callTool("4", "task_delegation_get_session", `{"session_id":"`+target+`"}`)
	if legacy.Code != 200 || strings.Contains(legacy.Body.String(), target) || !strings.Contains(legacy.Body.String(), webTasks.Items[0].TaskID) {
		t.Fatalf("legacy call failed or reflected Session identity: %d %s", legacy.Code, legacy.Body.String())
	}
	// Failed lookups must retain scoped recovery arguments in the actual MCP text.
	missingTurn := manager.NewID()
	response = callTool("5", "task_delegation_get_session", `{"task_id":"`+webTasks.Items[0].TaskID+`","turn_id":"`+missingTurn+`"}`)
	var failed struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &failed); err != nil || response.Code != 200 ||
		!failed.Result.IsError || len(failed.Result.Content) != 1 {
		t.Fatalf("missing MCP lookup error: %d %s", response.Code, response.Body.String())
	}
	var lookup struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
		Recovery struct {
			Tool         string            `json:"tool"`
			Arguments    map[string]string `json:"arguments"`
			Instructions string            `json:"instructions"`
		} `json:"recovery"`
	}
	if err = json.Unmarshal([]byte(failed.Result.Content[0].Text), &lookup); err != nil ||
		lookup.Error.Code != "resource_not_found" || lookup.Recovery.Tool != "task_delegation_get_session" ||
		len(lookup.Recovery.Arguments) != 1 || lookup.Recovery.Arguments["task_id"] != webTasks.Items[0].TaskID ||
		!strings.Contains(lookup.Recovery.Instructions, "去掉 turn_id 和 cursor") {
		t.Fatalf("missing exact recovery in MCP text: %s", failed.Result.Content[0].Text)
	}
	if err = store.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	out := internal("GET", "/api/internal/v1/engines/"+engine+"/sandboxes", secret, "")
	var configs struct {
		Items []manager.Sandbox `json:"items"`
	}
	if err = json.Unmarshal(out.Body.Bytes(), &configs); err != nil || out.Code != 200 || len(configs.Items) != 1 {
		t.Fatal(out.Code, out.Body.String(), err)
	}
	env := configs.Items[0]
	path = "/api/internal/v1/sandbox-instances/" + env.ID + "/config?engine_id=" + engine
	if out = internal("GET", path, env.ConfigToken, ""); out.Code != 200 {
		t.Fatal(out.Code, out.Body.String())
	}
	if out = internal("GET", path, secret, ""); out.Code != 401 {
		t.Fatal("Engine token accepted as limited Runner credential")
	}
	if out = internal("GET", "/api/internal/v1/engines/"+engine+"/assignments", env.ConfigToken, ""); out.Code != 401 {
		t.Fatal("Runner credential expanded to Engine authority")
	}
	if out = internal("POST", "/api/internal/v1/sandbox-instances/"+env.ID+"/state", secret, `{"engine_id":"`+engine+`","state":"stopped"}`); out.Code != 409 {
		t.Fatal("live environment reported stopped", out.Code, out.Body.String())
	}
	if _, err = s.ControlSession(ctx, w, actor, source.ID, false); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM manager_inputs WHERE session_id=$1 AND status='queued'`, target).Scan(&inputs); err != nil || inputs != 2 {
		t.Fatalf("stopping source chat canceled delegated work: %d %v", inputs, err)
	}
	if _, err = d.Authorize(ctx, token); err == nil {
		t.Fatal("stopped source retained delegation authority")
	}
	if _, err = d.Call(ctx, scope, "create_session", manager.DelegationArgs{Instruction: "late"}); err == nil {
		t.Fatal("cached trusted scope bypassed current authorization")
	}
}

func TestDelegationCreateDedupUsesAuthorizedSourceInput(t *testing.T) {
	db, m, d, first := plannedFixture(t)
	ctx := context.Background()
	args := manager.DelegationArgs{Instruction: "prepare the same report"}
	create := func(scope manager.DelegationScope) string {
		t.Helper()
		result, err := d.Call(ctx, scope, "create_session", args)
		if err != nil {
			t.Fatal(err)
		}
		return result.(map[string]any)["session_id"].(string)
	}
	firstTask := create(first)
	var turn string
	if err := db.QueryRowContext(ctx, `SELECT active_turn_id FROM manager_sessions WHERE id=$1`, first.Identity.SessionID).Scan(&turn); err != nil {
		t.Fatal(err)
	}
	reviewFinishTurn(t, db, first.Identity.SessionID, first.InputID, turn)
	next, _, err := m.SubmitInput(ctx, first.WorkspaceID, first.ActorID, first.Identity.SessionID, "new-user-input", "Run the same report again.")
	if err != nil {
		t.Fatal(err)
	}
	reviewStartTurn(t, db, first.WorkspaceID, first.Identity.SessionID, next.ID, first.Identity.AssignmentID, first.Identity.Generation)
	second, err := d.Store.DelegationSource(ctx, first.Identity)
	if err != nil || second.InputID != next.ID || second.ActorID != first.ActorID {
		t.Fatalf("failed to advance source input: %+v, %v", second, err)
	}
	if _, err = d.Call(ctx, first, "create_session", args); err == nil {
		t.Fatal("a previously authorized request crossed into the next input")
	}
	// Exercise the write boundary independently of the earlier service check.
	store := &repository.Store{DB: db}
	err = store.WithSession(ctx, manager.Target{
		Workspace: first.WorkspaceID, Actor: first.ActorID, Conversation: first.ConversationID,
		Source: &first.Identity, SourceInputID: first.InputID,
	}, func(manager.SessionTx) error {
		t.Error("transaction admitted an obsolete source input")
		return nil
	})
	var failure *manager.Error
	if !errors.As(err, &failure) || failure.Code != "delegation_input_changed" {
		t.Fatalf("wrong transaction outcome: %v", err)
	}
	secondTask := create(second)
	if secondTask == firstTask || create(second) != secondTask {
		t.Fatal("same args should deduplicate within one input, but create anew for another input")
	}
	var delegatedKey string
	if err = db.QueryRowContext(ctx, `SELECT request_key FROM manager_input_dedup WHERE session_id=$1`, secondTask).Scan(&delegatedKey); err != nil {
		t.Fatal(err)
	}
	req := manager.CreateSession{ConversationID: second.ConversationID, Mode: "task"}
	req.Input = &struct {
		Text string `json:"text"`
	}{Text: args.Instruction}
	public, duplicate, err := m.CreateSession(ctx, second.WorkspaceID, second.ActorID, delegatedKey, req)
	if err != nil || duplicate || public.ID == secondTask || create(second) != secondTask {
		t.Fatalf("delegated dedup key leaked into public creation: duplicate=%t, err=%v", duplicate, err)
	}
}
