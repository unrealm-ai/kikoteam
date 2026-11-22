package repository

import (
	"testing"

	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func TestWecomTaskReportDeliveryUsesOriginalAuthorizedPrivateTarget(t *testing.T) {
	db, sessions, store, _, _, _, binding := channelManagerFixture(t)
	ctx := t.Context()
	reviewExec(t, db, `UPDATE platform_channel_bindings SET provider='wecom',app_id='bot',bot_open_id='bot',external_tenant_id='bot' WHERE id=$1`, binding)
	for _, kind := range []string{"direct", "group"} {
		msg := inboundFixture(binding, "event-"+kind, "sender")
		msg.ChatType = kind
		msg.ExternalChatID = "sender"
		if kind == "group" {
			msg.ExternalChatID = "group"
		}
		msg.ExternalTenantID, msg.BotID, msg.SenderIDType, msg.ProviderDirected = "bot", "bot", "user_id", true
		inbox, err := store.ReceiveChannel(ctx, msg)
		if err != nil {
			t.Fatal(err)
		}
		if err = store.ProcessChannelInbox(ctx, sessions); err != nil {
			t.Fatal(err)
		}
		for _, deliveryKind := range []string{"reply", "task_reply:" + manager.NewID()} {
			id := manager.NewID()
			reviewExec(t, db, `INSERT INTO manager_channel_deliveries(id,inbox_id,binding_id,connection_generation,kind,text) VALUES($1,$2,$3,1,$4,'result')`, id, inbox.ID, binding, deliveryKind)
			batch, err := store.ClaimChannelDeliveries(ctx, 1, true)
			if err != nil || len(batch) != 1 || batch[0].ID != id {
				t.Fatal(batch, err)
			}
			auth, err := store.AuthorizeChannelDelivery(ctx, id, batch[0].LeaseToken)
			if err != nil || !auth.Allowed {
				t.Fatal(auth, err)
			}
			want := msg.ProviderMessageID
			if kind == "direct" && deliveryKind != "reply" {
				want = ""
			}
			if auth.ReplyToMessageID != want || batch[0].ReplyToMessageID != want || auth.ChatID != msg.ExternalChatID || auth.TargetDigest == "" {
				t.Fatal(kind, deliveryKind, auth)
			}
		}
	}
	// A task result with a valid original recipient is still denied after revocation.
	msg := inboundFixture(binding, "delayed", "sender")
	msg.ExternalChatID, msg.ExternalTenantID, msg.BotID, msg.SenderIDType = "sender", "bot", "bot", "user_id"
	box, err := store.ReceiveChannel(ctx, msg)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.ProcessChannelInbox(ctx, sessions); err != nil {
		t.Fatal(err)
	}
	id := manager.NewID()
	reviewExec(t, db, `INSERT INTO manager_channel_deliveries(id,inbox_id,binding_id,connection_generation,kind,text) VALUES($1,$2,$3,1,$4,'result')`, id, box.ID, binding, "task_reply:"+manager.NewID())
	batch, err := store.ClaimChannelDeliveries(ctx, 1, true)
	if err != nil || len(batch) != 1 || batch[0].ID != id {
		t.Fatal(batch, err)
	}
	reviewExec(t, db, `UPDATE platform_channel_bindings SET enabled=false WHERE id=$1`, binding)
	_, err = store.AuthorizeChannelDelivery(ctx, id, batch[0].LeaseToken)
	assertChannelError(t, err, "access_revoked")
}
