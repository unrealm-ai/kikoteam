package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func TestManagerWecomControlReceiptAfterCommit(t *testing.T) {
	db, _, binding, _ := wecomContactFixture(t)
	const privateText = "fixture-private-text 123456"
	ctx := context.Background()
	for _, reply := range []manager.WecomControlReply{
		"", manager.WecomControlVerified, manager.WecomControlUsed, manager.WecomControlExpired, manager.WecomControlRevoked,
	} {
		input := contactInbound(binding, privateText, "sender")
		store := &managerrepo.Store{DB: db, ConsumeWecomControl: func(_ context.Context, _ *sql.Tx, current manager.ChannelInbound, epoch int64) (manager.WecomControlResult, error) {
			if current.ProviderEventID != input.ProviderEventID || epoch != binding.IdentityEpoch {
				t.Fatal("consumer not called with trusted ingress identity")
			}
			return manager.WecomControlResult{Handled: true, Reply: reply}, nil
		}}
		out, err := store.ReceiveChannel(ctx, input)
		if err != nil || out.ControlReply != reply || out.Status != "accepted" || !manager.ValidID(out.ID) {
			t.Fatalf("unexpected control receipt: %+v %v", out, err)
		}
		assertWecomControlNotQueued(t, db, input.ProviderEventID)
		wire, err := json.Marshal(out)
		if err != nil || strings.Contains(string(wire), privateText) ||
			strings.Contains(string(wire), `"control_reply"`) != (reply != "") {
			t.Fatal("control response must expose only allowlisted metadata")
		}
	}
	input := contactInbound(binding, privateText, "sender")
	cancelCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	store := &managerrepo.Store{DB: db, ConsumeWecomControl: func(context.Context, *sql.Tx, manager.ChannelInbound, int64) (manager.WecomControlResult, error) {
		cancel()
		return manager.WecomControlResult{Handled: true, Reply: manager.WecomControlVerified}, nil
	}}
	out, err := store.ReceiveChannel(cancelCtx, input)
	if err == nil || out.ControlReply != "" {
		t.Fatalf("uncommitted reply exposed: %+v %v", out, err)
	}
	assertWecomControlNotQueued(t, db, input.ProviderEventID)
}

func TestManagerWecomControlRejectsInvalidProducerAndWrongProvider(t *testing.T) {
	db, _, binding, _ := wecomContactFixture(t)
	ctx := context.Background()
	for _, result := range []manager.WecomControlResult{
		{Reply: manager.WecomControlVerified},
		{Handled: true, Reply: "untrusted-provider-text"},
	} {
		store := &managerrepo.Store{DB: db, ConsumeWecomControl: func(context.Context, *sql.Tx, manager.ChannelInbound, int64) (manager.WecomControlResult, error) {
			return result, nil
		}}
		input := contactInbound(binding, "fixture-private-code 123456", "sender")
		out, err := store.ReceiveChannel(ctx, input)
		if err == nil || out.ControlReply != "" {
			t.Fatalf("invalid producer result exposed: %+v %v", out, err)
		}
		assertWecomControlNotQueued(t, db, input.ProviderEventID)
	}
	var feishuBinding string
	var generation, revision, rules int64
	if err := db.QueryRow(`SELECT id,connection_generation,config_revision,rules_version
 FROM platform_channel_bindings WHERE agent_id=$1 AND provider='feishu'`, binding.AgentID).Scan(&feishuBinding, &generation, &revision, &rules); err != nil {
		t.Fatal(err)
	}
	store := &managerrepo.Store{DB: db, ConsumeWecomControl: func(context.Context, *sql.Tx, manager.ChannelInbound, int64) (manager.WecomControlResult, error) {
		t.Fatal("wrong provider entered WeCom control consumer")
		return manager.WecomControlResult{}, nil
	}}
	input := inboundFixture(feishuBinding, manager.NewID(), "feishu-sender")
	input.ConnectionGeneration, input.ConfigRevision, input.RulesVersion = generation, revision, rules
	input.ControlEligible = true
	out, err := store.ReceiveChannel(ctx, input)
	var rejected *manager.Error
	if !errors.As(err, &rejected) || rejected.Status != 400 || out.ControlReply != "" {
		t.Fatalf("unsupported provider control was not rejected: %+v %v", out, err)
	}
	assertWecomControlNotQueued(t, db, input.ProviderEventID)
}

func assertWecomControlNotQueued(t *testing.T, db *sql.DB, event string) {
	t.Helper()
	var queued bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM manager_channel_inbox WHERE provider_event_id=$1)
 OR EXISTS(SELECT 1 FROM manager_inputs)`, event).Scan(&queued); err != nil || queued {
		t.Fatalf("control message entered Inbox/Input: %t %v", queued, err)
	}
}
