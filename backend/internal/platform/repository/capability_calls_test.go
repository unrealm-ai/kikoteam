package repository

import (
	"context"
	"errors"
	"testing"

	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func TestCapabilityReceiptCannotMoveToNextInput(t *testing.T) {
	db, svc, _, workspace, actor, agent, _ := managerFixture(t)
	ctx := context.Background()
	store := &managerrepo.Store{DB: db}
	conversation, err := svc.CreateConversation(ctx, workspace, actor, manager.CreateConversation{AgentID: agent})
	if err != nil {
		t.Fatal(err)
	}
	req := manager.CreateSession{ConversationID: conversation.ID, Mode: "task", Input: &struct {
		Text string `json:"text"`
	}{Text: "first"}}
	session, _, err := svc.CreateSession(ctx, workspace, actor, "first", req)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := svc.SubmitInput(ctx, workspace, actor, session.ID, "second", "second")
	if err != nil {
		t.Fatal(err)
	}
	engine := manager.Engine{ID: manager.NewID(), HostID: "receipt-fixture", Endpoint: "http://127.0.0.1:9000", Capacity: 1}
	if err = store.RegisterEngine(ctx, engine); err != nil {
		t.Fatal(err)
	}
	if err = store.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	identity := manager.DelegationIdentity{SessionID: session.ID, EngineID: engine.ID}
	if err = db.QueryRowContext(ctx, `SELECT assignment_id,generation FROM manager_sessions WHERE id=$1`, session.ID).Scan(&identity.AssignmentID, &identity.Generation); err != nil {
		t.Fatal(err)
	}
	start := func(input string, number int) string {
		t.Helper()
		turn := manager.NewID()
		if _, err := db.ExecContext(ctx, `INSERT INTO manager_turns(id,workspace_id,session_id,input_id,turn_no,assignment_id,generation,status) VALUES($1,$2,$3,$4,$5,$6,$7,'running')`, turn, workspace, session.ID, input, number, identity.AssignmentID, identity.Generation); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE manager_inputs SET status='started',turn_id=$2 WHERE id=$1`, input, turn); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE manager_sessions SET active_turn_id=$2,last_turn_no=$3 WHERE id=$1`, session.ID, turn, number); err != nil {
			t.Fatal(err)
		}
		return turn
	}
	firstTurn := start(*session.InitialInputID, 1)
	_, subject, originalInput, err := store.CapabilityContext(ctx, identity)
	if err != nil || subject != actor || originalInput != *session.InitialInputID {
		t.Fatal("source Input was not resolved", err)
	}
	receipt := manager.CapabilityCall{ID: manager.NewID(), InputID: originalInput, NativeID: "first-call", PluginID: manager.NewID(), Tool: "lookup", Status: "denied"}
	if err = store.RecordCapabilityCall(ctx, identity, receipt); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE manager_turns SET status='completed',finished_at=now() WHERE id=$1`, firstTurn); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE manager_inputs SET status='processed',finished_at=now() WHERE id=$1`, originalInput); err != nil {
		t.Fatal(err)
	}
	secondTurn := start(second.ID, 2)
	// A discovery request accepted during the previous Input arrives late.
	receipt.ID, receipt.NativeID = manager.NewID(), "late-from-first"
	err = store.RecordCapabilityCall(ctx, identity, receipt)
	var failure *manager.Error
	if !errors.As(err, &failure) || failure.Code != "capability_forbidden" {
		t.Fatal("late receipt attached to next Input", err)
	}
	receipt.ID, receipt.InputID, receipt.NativeID = manager.NewID(), second.ID, "second-call"
	if err = store.RecordCapabilityCall(ctx, identity, receipt); err != nil {
		t.Fatal(err)
	}
	var total, secondCount int
	if err = db.QueryRowContext(ctx, `SELECT count(*),count(*) FILTER(WHERE turn_id=$1) FROM manager_capability_calls WHERE session_id=$2`, secondTurn, session.ID).Scan(&total, &secondCount); err != nil || total != 2 || secondCount != 1 {
		t.Fatalf("unexpected receipts: %d/%d %v", total, secondCount, err)
	}
	if _, err = svc.ControlSession(ctx, workspace, actor, session.ID, false); err != nil {
		t.Fatal(err)
	}
	receipt.ID, receipt.NativeID = manager.NewID(), "after-stop"
	if err = store.RecordCapabilityCall(ctx, identity, receipt); err == nil {
		t.Fatal("closed execution window accepted a receipt")
	}
}
