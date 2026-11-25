package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	managercontroller "github.com/unrealm-ai/kikoteam/backend/internal/manager/controller"
	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestAskHumanModeConflictsDoNotFenceAndPureApprovalCanReplay(t *testing.T) {
	db, sessions, _, w, owner, agent, _ := managerFixture(t)
	ctx := context.Background()
	store := sessions.Store.(*managerrepo.Store)
	rules, err := store.HumanRecipients(ctx, w, owner, agent, &manager.HumanRecipients{Recipients: []manager.HumanRecipient{{
		Ref:       service.SelectionRef{Provider: "web", Kind: "user", WorkspaceID: w, AgentID: agent, ExternalID: owner},
		IsDefault: true,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	conv, err := sessions.ResolveWebConversation(ctx, w, owner, agent)
	if err != nil {
		t.Fatal(err)
	}
	task, _, err := sessions.CreateSession(ctx, w, owner, "ask-human-modes", manager.CreateSession{
		ConversationID: conv.ID, Mode: "task",
		Input: &struct {
			Text string `json:"text"`
		}{Text: "在上线前找人确认"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var input string
	if err = db.QueryRow(`SELECT initial_input_id FROM manager_sessions WHERE id=$1`, task.ID).Scan(&input); err != nil {
		t.Fatal(err)
	}
	id := manager.DelegationIdentity{SessionID: task.ID, EngineID: service.NewID(), AssignmentID: service.NewID(), Generation: 1}
	if err = store.RegisterEngine(ctx, manager.Engine{ID: id.EngineID, HostID: "ask-human", Endpoint: "http://127.0.0.1:1", Capacity: 2}); err != nil {
		t.Fatal(err)
	}
	turn := service.NewID()
	reviewExec(t, db, `INSERT INTO manager_turns(id,workspace_id,session_id,input_id,turn_no,assignment_id,generation,status) VALUES($1,$2,$3,$4,1,$5,1,'running')`, turn, w, task.ID, input, id.AssignmentID)
	reviewExec(t, db, `UPDATE manager_inputs SET status='started',turn_id=$2 WHERE id=$1`, input, turn)
	reviewExec(t, db, `UPDATE manager_sessions SET active_turn_id=$2,engine_id=$3,assignment_id=$4,generation=1 WHERE id=$1`, task.ID, turn, id.EngineID, id.AssignmentID)
	secret := strings.Repeat("fixture", 6)
	token, err := manager.SignHumanRuntime(secret, input, id)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	(&managercontroller.Server{Service: sessions, Engines: store, EngineToken: secret}).Routes(mux)
	submit := func(q manager.HumanQuestion) *httptest.ResponseRecorder {
		t.Helper()
		wireQuestion := struct {
			manager.HumanQuestion
			Questions []manager.HumanQuestionItem `json:"questions"`
		}{HumanQuestion: q, Questions: q.Questions}
		wire, e := json.Marshal(map[string]any{"operation": "request", "question": wireQuestion})
		if e != nil {
			t.Fatal(e)
		}
		r := httptest.NewRequest(http.MethodPost, "/api/internal/v1/sessions/"+task.ID+"/human-tools", bytes.NewReader(wire))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-Kiko-Human-Input", input)
		out := httptest.NewRecorder()
		mux.ServeHTTP(out, r)
		return out
	}
	yes, no := true, false
	approval := manager.HumanQuestion{
		CandidateID: rules.Recipients[0].ID, RulesVersion: rules.Version, QuestionScope: "上线", Goal: "同意发布测试环境吗？",
		Background: "测试已通过", KnownFacts: []manager.HumanFact{}, DecisionRequired: &yes,
		Questions: []manager.HumanQuestionItem{}, CompletionCriteria: []string{"获得明确决定"},
		ProposedAction: "执行灰度发布", ImpactScope: "仅测试环境", IfNotConfirmed: "不发布",
	}
	mixed := approval
	mixed.Questions = []manager.HumanQuestionItem{{ID: "publish", Text: approval.Goal, Required: &yes, AnswerType: "choice",
		Options: []manager.HumanQuestionOption{{ID: "approve", Label: "同意"}, {ID: "reject", Label: "拒绝"}},
	}}
	emptyInformation := approval
	emptyInformation.DecisionRequired = &no
	unspecified := approval
	unspecified.DecisionRequired = nil
	for _, q := range []manager.HumanQuestion{mixed, emptyInformation, unspecified} {
		out := submit(q)
		if out.Code != 400 || !strings.Contains(out.Body.String(), "human_question_mode_conflict") || !strings.Contains(out.Body.String(), "decisionRequired") {
			t.Fatalf("wrong mode conflict response: %d %s", out.Code, out.Body)
		}
	}
	var requests, actions int
	if err = db.QueryRow(`SELECT (SELECT count(*) FROM manager_human_requests WHERE session_id=$1),
 jsonb_array_length(required_actions) FROM manager_sessions WHERE id=$1`, task.ID).Scan(&requests, &actions); err != nil || requests != 0 || actions != 0 {
		t.Fatalf("invalid ask changed state: requests=%d actions=%d err=%v", requests, actions, err)
	}
	// Send an explicit empty array on the wire, rather than Go's omitempty.
	// The Manager stores it canonically; retry must return the same request.
	var requestID string
	for range 2 {
		out := submit(approval)
		var v manager.HumanRequest
		if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &v) != nil || v.State != "preparing" ||
			len(v.Question.Questions) != 0 || v.Question.AnswerType != "decision" || v.Question.Question != approval.Goal {
			t.Fatalf("corrected approval rejected: %d %s", out.Code, out.Body)
		}
		if requestID != "" && requestID != v.ID {
			t.Fatalf("same zero-field request was recreated: %s vs %s", requestID, v.ID)
		}
		requestID = v.ID
	}
	reviewExec(t, db, `UPDATE manager_turns SET status='completed',finished_at=now() WHERE id=$1`, turn)
	reviewExec(t, db, `UPDATE manager_inputs SET status='processed',finished_at=now() WHERE id=$1`, input)
	reviewExec(t, db, `UPDATE manager_sessions SET active_turn_id=NULL WHERE id=$1`, task.ID)
	reviewExec(t, db, `UPDATE manager_human_requests SET state='pending',fenced_at=now() WHERE id=$1`, requestID)
	answer, err := store.AnswerHumanRequest(ctx, w, owner, requestID, manager.HumanAnswer{Version: 1, Decision: "approve"})
	if err != nil || answer.(map[string]string)["state"] != "approved" {
		t.Fatalf("pure approval required unexpected fields: %v %v", answer, err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM manager_inputs WHERE session_id=$1 AND human_request_id=$2 AND source_kind='human_confirmation'`, task.ID, requestID).Scan(&requests); err != nil || requests != 1 {
		t.Fatalf("pure approval continuation count=%d error=%v", requests, err)
	}
}
