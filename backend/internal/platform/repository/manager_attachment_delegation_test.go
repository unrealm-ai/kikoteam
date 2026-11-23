package repository

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func TestDelegationTransfersOnlyAuthorizedSourceAttachmentGrants(t *testing.T) {
	db, m, d, scope := plannedFixture(t)
	ctx := context.Background()
	a := manager.Attachment{ID: manager.NewID(), Name: "a.txt", Status: "ready"}
	b := manager.Attachment{ID: manager.NewID(), Name: "b.txt", Status: "ready"}
	pending := manager.Attachment{ID: manager.NewID(), Name: "pending.txt", Status: "downloading", Path: "should-not-forward"}
	raw, err := json.Marshal([]manager.Attachment{a, b, pending})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE manager_inputs SET attachments=$2 WHERE id=$1`, scope.InputID, raw); err != nil {
		t.Fatal(err)
	}

	other, err := m.CreateConversation(ctx, scope.WorkspaceID, scope.ActorID, manager.CreateConversation{AgentID: scope.AgentID})
	if err != nil {
		t.Fatal(err)
	}
	req := manager.CreateSession{ConversationID: other.ID, Mode: "chat", Input: &struct {
		Text string `json:"text"`
	}{Text: "other chat"}}
	foreign, _, err := m.CreateSession(ctx, scope.WorkspaceID, scope.ActorID, "other-chat-key", req)
	if err != nil {
		t.Fatal(err)
	}
	foreignID := manager.NewID()
	raw, err = json.Marshal([]manager.Attachment{{ID: foreignID, Name: "foreign.txt", Status: "ready"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE manager_inputs SET attachments=$2 WHERE id=$1`, *foreign.InitialInputID, raw); err != nil {
		t.Fatal(err)
	}

	otherActor := manager.NewID()
	insertWorkspaceAccount(t, db, otherActor, "attachment-other@example.test")
	privateID := manager.NewID()
	raw, err = json.Marshal([]manager.Attachment{{ID: privateID, Name: "another actor", Status: "ready"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO manager_inputs(id,workspace_id,session_id,submitted_by,admission_epoch,seq,text,attachments)
    SELECT $2,workspace_id,session_id,$3,admission_epoch,seq+100,'other actor input',$4
    FROM manager_inputs WHERE id=$1`, scope.InputID, manager.NewID(), otherActor, raw); err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{foreignID, privateID} {
		_, err = d.Call(ctx, scope, "create_session", manager.DelegationArgs{Instruction: "do not transfer", AttachmentIDs: []string{id}})
		var failure *manager.Error
		if !errors.As(err, &failure) || failure.Code != "permission_denied" {
			t.Fatalf("want permission denial: %v", err)
		}
	}

	inspect := func(input string, want ...manager.Attachment) {
		t.Helper()
		var saved []byte
		var got []manager.Attachment
		if e := db.QueryRowContext(ctx, `SELECT attachments FROM manager_inputs WHERE id=$1`, input).Scan(&saved); e != nil {
			t.Fatal(e)
		}
		if e := json.Unmarshal(saved, &got); e != nil || !slices.Equal(got, want) {
			t.Fatalf("want %#v got %#v: %v", want, got, e)
		}
	}
	args := manager.DelegationArgs{Instruction: "read files", AttachmentIDs: []string{b.ID, pending.ID, a.ID}}
	v, err := d.Call(ctx, scope, "create_session", args)
	if err != nil {
		t.Fatal(err)
	}
	immediate := v.(map[string]any)
	pending.Path = ""
	inspect(*immediate["input_id"].(*string), b, pending, a)
	v, err = d.Call(ctx, scope, "send_message", manager.DelegationArgs{SessionID: immediate["session_id"].(string), AttachmentIDs: []string{a.ID}})
	if err != nil {
		t.Fatal(err)
	}
	inspect(v.(manager.Input).ID, a)

	v, err = d.Call(ctx, scope, "create_todo", manager.DelegationArgs{RequestKey: "create-refs", Instruction: "read later", AttachmentIDs: []string{a.ID}})
	if err != nil {
		t.Fatal(err)
	}
	todo := v.(manager.PlannedTask)
	v, err = d.Call(ctx, scope, "supplement_todo", manager.DelegationArgs{SessionID: todo.SessionID, ExpectedVersion: todo.Version, RequestKey: "supplement-refs", Instruction: "read both", AttachmentIDs: []string{a.ID, b.ID}})
	if err != nil {
		t.Fatal(err)
	}
	todo = v.(manager.PlannedTask)
	if !slices.Equal(todo.Attachments, []manager.Attachment{a, b}) {
		t.Fatalf("unexpected planned refs: %#v", todo.Attachments)
	}
	v, err = d.Call(ctx, scope, "start_todo", manager.DelegationArgs{SessionID: todo.SessionID, ExpectedVersion: todo.Version, RequestKey: "start-refs"})
	if err != nil {
		t.Fatal(err)
	}
	inspect(*v.(manager.PlannedTask).InitialInputID, a, b)
}
