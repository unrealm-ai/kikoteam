package repository

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	inputattachments "github.com/unrealm-ai/kikoteam/backend/internal/manager/mcp/inputattachments"
	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func TestInputAttachmentLiveStatusThroughManagerMCP(t *testing.T) {
	db, m, delegate, scope := plannedFixture(t)
	store := m.Store.(*managerrepo.Store)
	m.ProjectRoot = t.TempDir()
	ctx := t.Context()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	reviewExec(t, db, `UPDATE manager_conversations SET web_user_id=$2 WHERE id=$1`, scope.ConversationID, scope.ActorID)
	payload := "INPUT_ATTACHMENT_ROUNDTRIP"
	attach, _, err := m.ReserveWebAttachment(ctx, scope.WorkspaceID, scope.ActorID, scope.ConversationID, "reserve-ready", "proof.txt", int64(len(payload)))
	must(err)
	failed, _, err := m.ReserveWebAttachment(ctx, scope.WorkspaceID, scope.ActorID, scope.ConversationID, "reserve-failed", "failed.txt", 1)
	must(err)
	forbidden, _, err := m.ReserveWebAttachment(ctx, scope.WorkspaceID, scope.ActorID, scope.ConversationID, "reserve-not-granted", "forbidden.txt", 1)
	must(err)
	_, _, err = m.SubmitInputModeWithAttachments(ctx, scope.WorkspaceID, scope.ActorID, scope.Identity.SessionID, "attach-before-sending", "", "", []string{attach.ID, failed.ID})
	must(err)
	task, err := delegate.Call(ctx, scope, "create_session", manager.DelegationArgs{Instruction: "read authorized file", AttachmentIDs: []string{attach.ID, failed.ID}})
	must(err)
	out := task.(map[string]any)
	session, input := out["session_id"].(string), *out["input_id"].(*string)
	identity := manager.DelegationIdentity{SessionID: session, EngineID: scope.Identity.EngineID, AssignmentID: manager.NewID(), Generation: 1}
	turn := manager.NewID()
	reviewExec(t, db, `INSERT INTO manager_turns(id,workspace_id,session_id,input_id,turn_no,assignment_id,generation,status) VALUES($1,$2,$3,$4,1,$5,1,'running')`, turn, scope.WorkspaceID, session, input, identity.AssignmentID)
	reviewExec(t, db, `UPDATE manager_inputs SET status='started',turn_id=$2 WHERE id=$1`, input, turn)
	reviewExec(t, db, `UPDATE manager_sessions SET active_turn_id=$2,engine_id=$3,assignment_id=$4,generation=1 WHERE id=$1`, session, turn, identity.EngineID, identity.AssignmentID)
	mux := http.NewServeMux()
	secret := strings.Repeat("a", 40)
	inputattachments.Register(mux, store, m, secret)
	token, err := manager.SignInputAttachment(secret, identity)
	must(err)
	get := func(id string) string {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/mcp/input-attachments", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"attachment_get","arguments":{"attachment_id":"`+id+`"}}}`))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("MCP response %d: %s", w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	if got := get(attach.ID); !strings.Contains(got, `"status":"downloading"`) || strings.Contains(got, `"path":`) {
		t.Fatal(got)
	}
	failedUpload, err := m.BeginWebAttachment(ctx, scope.WorkspaceID, scope.ActorID, scope.ConversationID, failed.ID)
	must(err)
	must(m.FailWebAttachment(ctx, failedUpload))
	if got := get(failed.ID); !strings.Contains(got, `"status":"failed"`) || strings.Contains(got, `"path":`) {
		t.Fatal(got)
	}
	if got := get(forbidden.ID); !strings.Contains(got, "attachment_forbidden") || strings.Contains(got, "forbidden.txt") {
		t.Fatal(got)
	}
	upload, err := m.BeginWebAttachment(ctx, scope.WorkspaceID, scope.ActorID, scope.ConversationID, attach.ID)
	must(err)
	item, err := m.PublishWebAttachment(ctx, upload, "proof.txt", int64(len(payload)), strings.NewReader(payload))
	must(err)
	if got := get(attach.ID); !strings.Contains(got, `"status":"ready"`) || !strings.Contains(got, `"path":"`+item.Path+`"`) {
		t.Fatal(got)
	}
	reviewExec(t, db, `UPDATE manager_sessions SET stop_revision=stop_revision+1 WHERE id=$1`, session)
	if got := get(attach.ID); !strings.Contains(got, "attachment_forbidden") || strings.Contains(got, item.Path) {
		t.Fatal(got)
	}
}
