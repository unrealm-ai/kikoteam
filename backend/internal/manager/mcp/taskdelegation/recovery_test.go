package taskdelegation

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func TestDelegationRecoveryUsesScopedReadBeforeWrite(t *testing.T) {
	const id = "123e4567-e89b-42d3-a456-426614174000"
	args := manager.DelegationArgs{SessionID: id}
	for _, tc := range []struct {
		op, code, action, tool, outcome, hintText string
	}{
		{"get_session", "resource_not_found", "query", "task_delegation_list_sessions", "", "列表每项的 id"},
		{"send_message", "task_not_started", "query", "task_delegation_get_session", "not_applied", "supplement_todo"},
		{"stop_session", "task_not_started", "query", "task_delegation_get_session", "not_applied", "不能把停止自动当成取消"},
		{"supplement_todo", "version_conflict", "query", "task_delegation_get_session", "not_applied", "planning_version"},
		{"send_message", "dependency_unavailable", "query", "task_delegation_get_session", "unknown", "不要重发"},
		{"stop_session", "dependency_unavailable", "query", "task_delegation_get_session", "unknown", "看不到回执不代表未写入"},
		{"cancel_todo", "already_started", "query", "task_delegation_get_session", "not_applied", "须确认用户确实授权"},
		{"stop_session", "access_revoked", "stop", "", "", "不要重试"},
	} {
		t.Run(tc.op+"/"+tc.code, func(t *testing.T) {
			hint := recoveryForTask(tc.op, tc.code, args, nil)
			if hint == nil || hint.Action != tc.action || hint.Tool != tc.tool || hint.Outcome != tc.outcome || !strings.Contains(hint.Instructions, tc.hintText) {
				t.Fatalf("unexpected recovery: %#v", hint)
			}
			if hint.Tool == "task_delegation_get_session" && hint.Arguments["session_id"] != id {
				t.Fatal("expected exact lookup arguments")
			}
			if hint.Tool != "task_delegation_get_session" && len(hint.Arguments) != 0 {
				t.Fatal("unexpected arguments")
			}
		})
	}
}

func TestCanceledTodoRecoveryPreservesErrorAndDoesNotSuggestMutation(t *testing.T) {
	var err error = manager.CanceledTodoError{}
	var original *manager.Error
	if !errors.As(err, &original) || original.Code != "task_not_started" || original.Status != 409 {
		t.Fatalf("unexpected compatibility error: %v", err)
	}
	for _, op := range []string{"send_message", "stop_session"} {
		hint := recoveryForTask(op, original.Code, manager.DelegationArgs{}, err)
		if hint == nil || hint.Action != "stop" || hint.Tool != "" {
			t.Fatalf("unexpected canceled todo recovery: %#v", hint)
		}
	}
}

func TestDelegationRecoveryOmitsUntrustedValuesAndUnrelatedTools(t *testing.T) {
	for _, op := range []string{"work_item_amend_requirements", "artifact_select", "create_session", "create_todo"} {
		if hint := recoveryForTask(op, "resource_not_found", manager.DelegationArgs{}, nil); hint != nil {
			t.Fatalf("%s: scope creep: %#v", op, hint)
		}
	}
	hint := recoveryForTask("send_message", "invalid_argument", manager.DelegationArgs{SessionID: "private-fake-id"}, nil)
	payload, err := json.Marshal(hint)
	if err != nil || hint == nil || strings.Contains(string(payload), "private-fake-id") || hint.Tool != "task_delegation_list_sessions" {
		t.Fatalf("unexpected payload: %s, %v", payload, err)
	}
}

func TestArtifactRecoveryProjectsOnlyCurrentCandidates(t *testing.T) {
	const id = "123e4567-e89b-42d3-a456-426614174000"
	args := manager.DelegationArgs{ArtifactIDs: []string{"fake-or-foreign"}}
	failure := &manager.ArtifactSelectionError{
		Err:        manager.Fail("artifact_forbidden", 403, "产物不属于这次回复的候选清单"),
		Candidates: []manager.ArtifactSelectionCandidate{{ID: id, Name: "test.pdf"}},
	}
	got := failedToolResult("artifact_select", "artifact_forbidden", failure.Error(), args, failure)
	b, err := json.Marshal(got)
	if err != nil || !strings.Contains(string(b), `"artifact_id":"`+id+`"`) ||
		!strings.Contains(string(b), `"action":"retry_or_reply_text"`) || strings.Contains(string(b), "fake-or-foreign") {
		t.Fatalf("unexpected artifact recovery: %s (%v)", b, err)
	}
	got = failedToolResult("artifact_select", "dependency_unavailable", "unavailable", args, errors.New("database unavailable"))
	if _, exists := got["artifact_candidates"]; exists || got["recovery"].(*recoveryHint).Outcome != "unknown" {
		t.Fatalf("operational error is not a safe candidate response: %#v", got)
	}
}

func TestMissingSpecificTurnRecoversWithSessionOnly(t *testing.T) {
	const id = "123e4567-e89b-42d3-a456-426614174000"
	hint := recoveryForTask("get_session", "resource_not_found", manager.DelegationArgs{SessionID: id, TurnID: id, Cursor: "old"}, nil)
	if hint == nil || hint.Tool != "task_delegation_get_session" || len(hint.Arguments) != 1 || hint.Arguments["session_id"] != id {
		t.Fatalf("unexpected recovery: %#v", hint)
	}
}
