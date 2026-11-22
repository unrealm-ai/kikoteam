package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	platform "github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func runtimeHumanReply(t *testing.T, w http.ResponseWriter, req runtimeRequest, beforeAsk func() bool) bool {
	t.Helper()
	var user, tool string
	toolCount := 0
	for _, message := range req.Messages {
		switch message.Role {
		case "user":
			user, tool = fmt.Sprint(message.Content), ""
			toolCount = 0
		case "tool":
			tool = fmt.Sprint(message.Content)
			toolCount++
		}
	}
	for _, tool := range req.Tools {
		switch tool.Function.Name {
		case "list_human_confirmation_scopes":
			if !strings.Contains(user, "人工决定：退回修改") {
				t.Errorf("candidate tool was unexpectedly exposed locally: %s", tool.Function.Name)
			}
		case "list_pending_human_confirmations", "submit_human_confirmation", "get_human_confirmation_receipt":
			t.Errorf("a migrated tool was also exposed locally: %s", tool.Function.Name)
		}
	}
	switch {
	case strings.Contains(user, "human-task-fixture"):
		if tool == "" {
			humanToolStream(t, w, []string{"mcp__human_confirmation__list_human_confirmation_scopes"}, []any{map[string]any{}})
			return true
		}
		var list struct {
			Version string                         `json:"version"`
			Items   []struct{ CandidateID string } `json:"items"`
			Default *struct{ CandidateID string }  `json:"default_recipient"`
		}
		if err := json.Unmarshal([]byte(tool), &list); err != nil || len(list.Items) != 0 || list.Default == nil || list.Default.CandidateID == "" {
			t.Errorf("unexpected candidate result: %s %v", tool, err)
			w.WriteHeader(500)
			return true
		}
		var write string
		for _, item := range req.Tools {
			if strings.HasSuffix(item.Function.Name, "__write") {
				write = item.Function.Name
			}
		}
		if write == "" {
			t.Error("builtin write is unavailable")
			w.WriteHeader(500)
			return true
		}
		if !beforeAsk() {
			return true
		}
		humanToolStream(t, w, []string{write, "ask_human", write}, []any{
			map[string]string{"path": "human-forbidden-before.txt", "content": "unapproved"},
			map[string]any{
				"candidateId": list.Default.CandidateID, "rules_version": list.Version, "questionScope": "报表",
				"background": "fixture-private-background", "goal": "确认报表口径", "knownFacts": []any{},
				"questions":          []any{map[string]any{"id": "refund", "text": "确认报表是否包含退款？", "required": false, "answerType": "boolean"}},
				"completionCriteria": []string{"明确是否纳入"}, "proposedAction": "生成报表", "impactScope": "报表", "ifNotConfirmed": "保持等待",
			},
			map[string]string{"path": "human-forbidden-after.txt", "content": "unapproved"},
		})
	case strings.Contains(user, "人工决定：退回修改"):
		exposed := map[string]bool{}
		for _, item := range req.Tools {
			exposed[item.Function.Name] = true
		}
		if len(exposed) != 2 || !exposed["ask_human"] || !exposed["list_human_confirmation_scopes"] {
			t.Errorf("unexpected correction tools: %v", exposed)
			w.WriteHeader(500)
			return true
		}
		if tool == "" {
			humanToolStream(t, w, []string{"list_human_confirmation_scopes"}, []any{map[string]any{}})
			return true
		}
		var list struct {
			Version string                        `json:"version"`
			Default *struct{ CandidateID string } `json:"default_recipient"`
		}
		if err := json.Unmarshal([]byte(tool), &list); err != nil || list.Default == nil || list.Default.CandidateID == "" {
			t.Errorf("unexpected correction tool response: %s %v", tool, err)
			w.WriteHeader(500)
			return true
		}
		humanToolStream(t, w, []string{"ask_human"}, []any{map[string]any{
			"candidateId": list.Default.CandidateID, "rules_version": list.Version, "questionScope": "报表",
			"background": "按意见将范围缩至已到账退款", "goal": "确认修改后的报表范围", "knownFacts": []any{},
			"questions":          []any{map[string]any{"id": "revised-refund", "text": "确认报表只统计已到账退款？", "required": false, "answerType": "boolean"}},
			"completionCriteria": []string{"批准修订范围"}, "proposedAction": "仅统计已到账退款", "impactScope": "报表", "ifNotConfirmed": "保持等待",
			"decisionRequired": true,
		}})
	case strings.Contains(user, "human-inbox-fixture"):
		if tool == "" {
			humanToolStream(t, w, []string{"mcp__human_confirmation__list_pending_human_confirmations"}, []any{map[string]any{"page": 1, "pageSize": 1}})
		} else {
			var list struct {
				PageSize       int                   `json:"pageSize"`
				Total          int                   `json:"total"`
				CurrentMessage struct{ Text string } `json:"currentMessage"`
			}
			if err := json.Unmarshal([]byte(tool), &list); err != nil || list.PageSize != 1 || list.Total != 1 ||
				list.CurrentMessage.Text != "human-inbox-fixture" || !strings.Contains(tool, "fixture-private-background") {
				t.Errorf("assigned question or pagination missing from chat list: %s %v", tool, err)
			}
			humanTextStream(t, w, "inbox listed")
		}
	case strings.Contains(user, "报表退款方案可以照做") || strings.Contains(user, "改完再给我确认"):
		switch {
		case tool == "":
			humanToolStream(t, w, []string{"mcp__human_confirmation__list_pending_human_confirmations"}, []any{map[string]any{}})
		case strings.Contains(tool, "questionDigest"):
			var list struct {
				Items []manager.HumanRequest `json:"items"`
			}
			if json.Unmarshal([]byte(tool), &list) != nil || len(list.Items) != 1 {
				t.Errorf("unexpected confirmation evidence: %s", tool)
				w.WriteHeader(500)
				return true
			}
			item := list.Items[0]
			answer := map[string]any{
				"request_id": item.ID, "version": "1", "decision": "approve", "questionDigest": item.QuestionDigest,
				"association": "current_message", "answers": []any{}, "operationId": "submit-1",
			}
			if strings.Contains(user, "改完再给我确认") {
				answer["decision"], answer["text"] = "revise", "报表只统计已到账退款，改完再给我确认"
			}
			humanToolStream(t, w, []string{"mcp__human_confirmation__submit_human_confirmation"}, []any{answer})
		case toolCount == 2:
			var receipt struct{ ID, OperationID string }
			if err := json.Unmarshal([]byte(tool), &receipt); err != nil || receipt.ID == "" || receipt.OperationID != "submit-1" {
				t.Errorf("expected submission receipt: %s (%v)", tool, err)
				w.WriteHeader(500)
				return true
			}
			humanToolStream(t, w, []string{"mcp__human_confirmation__get_human_confirmation_receipt"}, []any{
				map[string]string{"request_id": receipt.ID, "operationId": receipt.OperationID},
			})
		default:
			state, response := "approved", "approval recorded"
			if strings.Contains(user, "改完再给我确认") {
				state, response = "revised", "revision recorded"
			}
			if !strings.Contains(tool, state) || !strings.Contains(tool, "continuation") {
				t.Errorf("decision was not accepted: %s", tool)
			}
			humanTextStream(t, w, response)
		}
	case strings.Contains(user, "确认报表只统计已到账退款") && strings.Contains(user, "approve"):
		for _, field := range []string{"proposedAction", "仅统计已到账退款", "impactScope", "completionCriteria", "questionDigest"} {
			if !strings.Contains(user, field) {
				t.Errorf("continuation omitted approved context: %s", field)
			}
		}
		humanTextStream(t, w, "resumed on original task")
	case strings.Contains(user, "human-ordinary-fixture"):
		humanTextStream(t, w, "ordinary input was processed")
	default:
		return false
	}
	return true
}

func humanToolStream(t *testing.T, w http.ResponseWriter, names []string, args []any) {
	t.Helper()
	calls := make([]map[string]any, len(names))
	for i, name := range names {
		raw, err := json.Marshal(args[i])
		if err != nil {
			t.Error(err)
			return
		}
		calls[i] = map[string]any{"index": i, "id": fmt.Sprintf("human-call-%s-%d", name, i), "type": "function",
			"function": map[string]string{"name": name, "arguments": string(raw)}}
	}
	humanCompletion(t, w, map[string]any{"role": "assistant", "tool_calls": calls}, "tool_calls")
}

func humanTextStream(t *testing.T, w http.ResponseWriter, text string) {
	t.Helper()
	humanCompletion(t, w, map[string]any{"role": "assistant", "content": text}, "stop")
}

func humanCompletion(t *testing.T, w http.ResponseWriter, delta map[string]any, finish string) {
	t.Helper()
	w.Header().Set("Content-Type", "text/event-stream")
	for _, item := range []map[string]any{{"index": 0, "delta": delta, "finish_reason": nil}, {"index": 0, "delta": map[string]any{}, "finish_reason": finish}} {
		chunk, err := json.Marshal(map[string]any{"id": "human-fixture", "choices": []any{item}})
		if err != nil {
			t.Error(err)
			return
		}
		if _, err = fmt.Fprintf(w, "data: %s\n\n", chunk); err != nil {
			t.Error(err)
			return
		}
	}
	if _, err := fmt.Fprint(w, "data: [DONE]\n\n"); err != nil {
		t.Error(err)
	}
}

func testRuntimeHumanRequests(t *testing.T, ctx context.Context, db *sql.DB, base, workspace, agent, temp string,
	call func(string, string, any, string) map[string]any,
	callFor func(string, string, string, any, string) map[string]any, wait func(func() bool), askReady <-chan struct{}, releaseAsk chan<- struct{},
) {
	t.Helper()
	approver := platform.NewID()
	if _, err := db.ExecContext(ctx, `INSERT INTO platform_accounts(id,name,email) VALUES($1,'Approver','approver@example.invalid')`, approver); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO platform_members(workspace_id,user_id,role) VALUES($1,$2,'member')`, workspace, approver); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE platform_agents SET usage_config=jsonb_set(usage_config,'{access}', '{"mode":"all","allow":[],"deny":[]}'::jsonb) WHERE id=$1`, agent); err != nil {
		t.Fatal(err)
	}
	const token = "human-approver-fixture"
	if _, err := db.ExecContext(ctx, `INSERT INTO platform_sessions(token_hash,account_id,expires_at) VALUES($1,$2,$3)`,
		platform.HashToken(token), approver, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	answerer := func(method, path string, value any, key string) map[string]any {
		return callFor(token, method, path, value, key)
	}
	rcPath := base + "/agents/" + agent + "/human-recipients"
	ref := platform.SelectionRef{Kind: "user", Provider: "web", WorkspaceID: workspace, AgentID: agent, ExternalID: approver}
	rules := call("PATCH", rcPath, manager.HumanRecipients{Recipients: []manager.HumanRecipient{{Ref: ref, IsDefault: true}}}, "")
	if rules["version"] != "1" {
		t.Fatalf("unexpected rules: %v", rules)
	}
	conversation := call("POST", base+"/conversations", map[string]string{"agent_id": agent}, "")["id"]
	task := call("POST", base+"/sessions", map[string]any{"conversation_id": conversation, "mode": "task", "input": map[string]string{"text": "human-task-fixture"}}, "ask-human")["id"].(string)
	taskPath := base + "/sessions/" + task
	wait(func() bool {
		select {
		case <-askReady:
			return true
		default:
			return false
		}
	})
	ordinary := call("POST", taskPath+"/inputs", map[string]string{"text": "human-ordinary-fixture", "delivery_mode": "steer"}, "ordinary-human")["id"].(string)
	wait(func() bool {
		return call("GET", taskPath+"/inputs/"+ordinary, nil, "")["status"] == "started"
	})
	close(releaseAsk)
	inboxPath := base + "/human-requests"
	var id string
	wait(func() bool {
		items := answerer("GET", inboxPath, nil, "")["items"].([]any)
		if len(items) != 1 {
			return false
		}
		id = items[0].(map[string]any)["id"].(string)
		return true
	})
	if items := call("GET", inboxPath, nil, "")["items"].([]any); len(items) != 0 {
		t.Fatal("task initiator read another person's question")
	}
	if err := filepath.WalkDir(temp, func(path string, d fs.DirEntry, err error) error {
		if strings.HasPrefix(filepath.Base(path), "human-forbidden-") {
			t.Errorf("same-batch write escaped the fence: %s", path)
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	toolEvents := call("GET", taskPath+"/events?type=agent.tool_result", nil, "")["items"]
	if strings.Contains(fmt.Sprint(toolEvents), "fixture-private-background") {
		t.Fatal("private question was exposed in the task event stream")
	}
	if input := call("GET", taskPath+"/inputs/"+ordinary, nil, ""); input["status"] != "queued" {
		t.Fatal("ordinary input bypassed pending confirmation")
	}
	direct := answerer("POST", base+"/conversations/web-direct", map[string]string{"agent_id": agent}, "")["id"].(string)
	chat := answerer("POST", base+"/sessions", map[string]any{"conversation_id": direct, "mode": "chat", "input": map[string]string{"text": "human-inbox-fixture"}}, "human-list")["id"].(string)
	chatPath := base + "/sessions/" + chat
	wait(func() bool {
		return strings.Contains(fmt.Sprint(answerer("GET", chatPath+"/events", nil, "")), "inbox listed")
	})
	original := id
	answerer("POST", chatPath+"/inputs", map[string]string{"text": "报表只统计已到账退款，改完再给我确认", "delivery_mode": "queue"}, "human-revise")
	wait(func() bool {
		items := answerer("GET", inboxPath, nil, "")["items"].([]any)
		if len(items) != 1 || items[0].(map[string]any)["id"] == original {
			return false
		}
		id = items[0].(map[string]any)["id"].(string)
		return true
	})
	var predecessor, sourceAuthority, revisedAuthority, answerType string
	err := db.QueryRowContext(ctx, `SELECT prior.state,prior.authority_input_id,next.authority_input_id,next.question->>'answer_type'
 FROM manager_human_requests prior JOIN manager_human_requests next ON next.session_id=prior.session_id AND next.input_id=prior.resume_input_id
 WHERE prior.id=$1 AND next.id=$2`, original, id).Scan(&predecessor, &sourceAuthority, &revisedAuthority, &answerType)
	if err != nil || predecessor != "revised" || sourceAuthority != revisedAuthority || answerType != "decision" {
		t.Fatalf("new question did not retain original authority and require approval: %q %q %q %q %v", predecessor, sourceAuthority, revisedAuthority, answerType, err)
	}
	var gateCount int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM manager_sessions s CROSS JOIN LATERAL jsonb_array_elements(s.required_actions) q
 WHERE s.id=$1 AND q->>'type'='human_confirmation' AND q->>'request_id'=$2`, task, id).Scan(&gateCount); err != nil || gateCount != 1 {
		t.Fatalf("new approval gate missing: %d %v", gateCount, err)
	}
	if input := call("GET", taskPath+"/inputs/"+ordinary, nil, ""); input["status"] != "queued" {
		t.Fatal("ordinary input bypassed the replacement confirmation gate")
	}
	answerer("POST", chatPath+"/inputs", map[string]string{"text": "报表退款方案可以照做", "delivery_mode": "queue"}, "human-answer")
	wait(func() bool {
		return call("GET", taskPath+"/inputs/"+ordinary, nil, "")["status"] == "processed" &&
			strings.Contains(fmt.Sprint(answerer("GET", chatPath+"/events?type=agent.message", nil, "")), "approval recorded")
	})
	for _, kind := range []string{"agent.tool_use", "agent.tool_result"} {
		events := fmt.Sprint(answerer("GET", chatPath+"/events?type="+kind, nil, "")["items"])
		if !strings.Contains(events, "human_confirmation") || !strings.Contains(events, "submit_human_confirmation") ||
			!strings.Contains(events, "get_human_confirmation_receipt") ||
			strings.Contains(events, id) || strings.Contains(events, "fixture-private-background") {
			t.Fatalf("expected redacted MCP %s events: %s", kind, events)
		}
	}
	replay := answerer("POST", inboxPath+"/"+id+"/answer", manager.HumanAnswer{Version: 1, Decision: "approve"}, "")
	if output := fmt.Sprint(call("GET", taskPath+"/events?type=agent.message", nil, "")); !strings.Contains(output, "resumed on original task") {
		t.Fatal("the original native session did not receive the answer")
	}
	if replay["state"] != "approved" {
		t.Fatal(replay)
	}
	var resumed int
	var ordered, originalActor bool
	err = db.QueryRowContext(ctx, `SELECT count(*),COALESCE(bool_and(resume.turn_id IS NOT NULL AND turn.turn_no<ordinary_turn.turn_no),false),
 COALESCE(bool_and(resume.submitted_by=root.submitted_by AND resume.submitted_by<>$3),false)
 FROM manager_inputs resume JOIN manager_human_requests request ON request.id=resume.human_request_id
 JOIN manager_inputs root ON root.id=request.authority_input_id JOIN manager_turns turn ON turn.id=resume.turn_id
 JOIN manager_inputs ordinary ON ordinary.id=$2 JOIN manager_turns ordinary_turn ON ordinary_turn.id=ordinary.turn_id
 WHERE request.id=$1`, id, ordinary, approver).Scan(&resumed, &ordered, &originalActor)
	if err != nil || resumed != 1 || !ordered || !originalActor {
		t.Fatalf("resume count=%d ordered=%t original_actor=%t error=%v", resumed, ordered, originalActor, err)
	}
	_, err = (&managerrepo.Store{DB: db}).AnswerHumanRequest(ctx, workspace, approver, id, manager.HumanAnswer{Version: 1, Decision: "reject"})
	if err == nil {
		t.Fatal("a conflicting answer was accepted")
	}
}
