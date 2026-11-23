package repository

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
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
	if got := get(attach.ID); !strings.Contains(got, `"status":"downloading"`) || strings.Contains(got, `"path":`) || !strings.Contains(got, "不要立即反复查询") {
		t.Fatal(got)
	}
	failedUpload, err := m.BeginWebAttachment(ctx, scope.WorkspaceID, scope.ActorID, scope.ConversationID, failed.ID)
	must(err)
	must(m.FailWebAttachment(ctx, failedUpload))
	if got := get(failed.ID); !strings.Contains(got, `"status":"failed"`) || strings.Contains(got, `"path":`) || !strings.Contains(got, "不要自行替换") {
		t.Fatal(got)
	}
	for _, tt := range []struct{ id, code string }{{forbidden.ID, "attachment_forbidden"}, {"not-an-id", "invalid_argument"}, {attach.ID + ":32", "invalid_argument"}} {
		candidates, truncated := inputAttachmentCandidatesFromMCP(t, get(tt.id), tt.code)
		if truncated || len(candidates) != 2 || candidates[0] != (manager.InputAttachmentCandidate{ID: attach.ID, Name: "proof.txt"}) ||
			candidates[1] != (manager.InputAttachmentCandidate{ID: failed.ID, Name: "failed.txt"}) {
			t.Fatalf("wrong current attachment candidates: %+v (%v)", candidates, truncated)
		}
	}
	upload, err := m.BeginWebAttachment(ctx, scope.WorkspaceID, scope.ActorID, scope.ConversationID, attach.ID)
	must(err)
	item, err := m.PublishWebAttachment(ctx, upload, "proof.txt", int64(len(payload)), strings.NewReader(payload))
	must(err)
	if got := get(attach.ID); !strings.Contains(got, `"status":"ready"`) || !strings.Contains(got, `"path":"`+item.Path+`"`) {
		t.Fatal(got)
	}
	// Add enough live backing rows to prove cap/order and dedup independent
	// of UUID ordering. Invalid and path-only records are not candidates.
	reviewExec(t, db, `WITH seeded AS (
 INSERT INTO manager_web_attachments(id,workspace_id,conversation_id,actor_id,request_key,name,size,expires_at)
 SELECT gen_random_uuid(),$2,$3,$4,'fixture-'||n,'file-'||n,1,now()+interval '1 hour' FROM generate_series(1,36) n
 RETURNING id, name
 ) UPDATE manager_inputs SET attachments=attachments||(
 SELECT jsonb_agg(jsonb_build_object('id',id,'name',name) ORDER BY split_part(name,'-',2)::int) FROM seeded
 )||jsonb_build_array(jsonb_build_object('id',$5::text,'name','duplicate'),jsonb_build_object('id','invalid'),jsonb_build_object('path','uploads/private.txt'))
 WHERE id=$1`, input, scope.WorkspaceID, scope.ConversationID, scope.ActorID, attach.ID)
	candidates, truncated := inputAttachmentCandidatesFromMCP(t, get(forbidden.ID), "attachment_forbidden")
	if !truncated || len(candidates) != 32 || candidates[0].ID != attach.ID || candidates[1].ID != failed.ID {
		t.Fatalf("wrong bounded candidates: %+v (%v)", candidates, truncated)
	}
	for i, item := range candidates[2:] {
		if !manager.ValidID(item.ID) || item.Name != "file-"+strconv.Itoa(i+1) {
			t.Fatalf("wrong deterministic candidate %d: %+v", i, item)
		}
	}
	stale := identity
	stale.Generation++
	token, err = manager.SignInputAttachment(secret, stale)
	must(err)
	if got := get("not-an-id"); !strings.Contains(got, "invalid_argument") || strings.Contains(got, `"candidates"`) {
		t.Fatal("stale assignment exposed candidates", got)
	}
	token, err = manager.SignInputAttachment(secret, identity)
	must(err)
	// Break only the advisory multi-source query. A malformed or ungranted
	// Web ID keeps its original error; a real failed Channel lookup differs.
	reviewExec(t, db, `ALTER TABLE manager_channel_media RENAME TO recovery_media_unavailable`)
	for _, x := range []struct{ id, code string }{{"not-an-id", "invalid_argument"}, {forbidden.ID, "attachment_forbidden"}, {attach.ID + ":0", "dependency_unavailable"}} {
		if got := get(x.id); !strings.Contains(got, x.code) || strings.Contains(got, `"candidates"`) {
			t.Fatal("unexpected lookup/recovery infrastructure error mapping", got)
		}
	}
	reviewExec(t, db, `ALTER TABLE recovery_media_unavailable RENAME TO manager_channel_media`)
	reviewExec(t, db, `UPDATE manager_sessions SET stop_revision=stop_revision+1 WHERE id=$1`, session)
	if got := get(attach.ID); !strings.Contains(got, "attachment_forbidden") || strings.Contains(got, item.Path) || strings.Contains(got, `"candidates"`) {
		t.Fatal(got)
	}
}

func inputAttachmentCandidatesFromMCP(t *testing.T, wire, wantCode string) ([]manager.InputAttachmentCandidate, bool) {
	t.Helper()
	var response struct {
		Result struct {
			IsError bool `json:"isError"`
			Content struct {
				manager.InputAttachmentRecovery
				Error map[string]string `json:"error"`
			} `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(wire), &response); err != nil {
		t.Fatal(err)
	}
	got := response.Result.Content
	if !response.Result.IsError || got.Error["code"] != wantCode || got.Advice == "" || strings.Contains(wire, `"path":`) {
		t.Fatal("bad recovery response", wire)
	}
	return got.Candidates, got.Truncated
}
