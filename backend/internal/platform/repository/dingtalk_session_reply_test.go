package repository

import (
	"database/sql"
	"testing"

	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func dingSessionReplyFixture(t *testing.T, kind, format string) (*sql.DB, *managerrepo.Store, manager.ChannelDelivery) {
	t.Helper()
	ctx := t.Context()
	db, sessions, store, _, _, _, binding := channelManagerFixture(t)
	reviewExec(t, db, `UPDATE platform_channel_bindings SET provider='dingtalk' WHERE id=$1`, binding)
	in := inboundFixture(binding, "stream-event", "sender")
	in.ProviderMessageID, in.BotID, in.SenderIDType, in.SenderTenant = "original-message", "bot", "user_id", in.ExternalTenantID
	receipt, err := store.ReceiveChannel(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.ProcessChannelInbox(ctx, sessions); err != nil {
		t.Fatal(err)
	}
	id := manager.NewID()
	reviewExec(t, db, `INSERT INTO manager_channel_deliveries(id,inbox_id,binding_id,connection_generation,kind,text,content_format)
 VALUES($1,$2,$3,1,$4,'ordinary reply',$5)`, id, receipt.ID, binding, kind, format)
	claimed, err := store.ClaimChannelDeliveries(ctx, 1, true)
	if err != nil || len(claimed) != 1 || claimed[0].ID != id {
		t.Fatal(claimed, err)
	}
	if claimed[0].ReplyToMessageID != in.ProviderMessageID || claimed[0].SessionWebhookEligible {
		t.Fatalf("wrong claim-time hint for %s: %+v", kind, claimed[0])
	}
	authorized, err := store.AuthorizeChannelDelivery(ctx, id, claimed[0].LeaseToken)
	if err != nil || !authorized.Allowed || len(authorized.TargetDigest) != 64 || authorized.ReplyToMessageID != in.ProviderMessageID || authorized.SessionWebhookEligible != (kind == "reply") {
		t.Fatal(authorized, err)
	}
	return db, store, authorized
}

func TestDingTalkSessionReplyReceiptBoundToOriginalAndSending(t *testing.T) {
	db, store, d := dingSessionReplyFixture(t, "reply", "markdown")
	ctx := t.Context()
	v := manager.DeliveryCompletion{LeaseToken: d.LeaseToken, TargetDigest: d.TargetDigest, Status: "sent",
		EvidenceType: "session_webhook", EvidenceID: "original-message"}
	for name, change := range map[string]func(*manager.DeliveryCompletion){
		"event not original message": func(r *manager.DeliveryCompletion) { r.EvidenceID = "stream-event" },
		"invalid status":             func(r *manager.DeliveryCompletion) { r.Status = "unknown" },
		"fabricated outbound id":     func(r *manager.DeliveryCompletion) { r.ProviderMessageID = "outbound" },
		"active request id":          func(r *manager.DeliveryCompletion) { r.ProviderRequestID = "request" },
		"provider session id":        func(r *manager.DeliveryCompletion) { r.ProviderSessionID = "session" },
		"query lease":                func(r *manager.DeliveryCompletion) { r.QueryToken = manager.NewID() },
	} {
		t.Run(name, func(t *testing.T) {
			bad := v
			change(&bad)
			assertChannelError(t, store.CompleteChannelDelivery(ctx, d.ID, bad), "receipt_evidence_required")
		})
	}
	reviewExec(t, db, `UPDATE manager_channel_deliveries SET lease_until=now()-interval '1 second' WHERE id=$1`, d.ID)
	assertChannelError(t, store.CompleteChannelDelivery(ctx, d.ID, v), "receipt_mismatch")
	reviewExec(t, db, `UPDATE manager_channel_deliveries SET lease_until=now()+interval '30 seconds' WHERE id=$1`, d.ID)
	for range 2 {
		if err := store.CompleteChannelDelivery(ctx, d.ID, v); err != nil {
			t.Fatal(err)
		}
	}
	var state string
	var evidence bool
	if err := db.QueryRowContext(ctx, `SELECT status,provider_message_id IS NULL AND provider_request_id IS NULL AND next_query_at IS NULL
 FROM manager_channel_deliveries WHERE id=$1`, d.ID).Scan(&state, &evidence); err != nil || state != "sent" || !evidence {
		t.Fatal(state, evidence, err)
	}
	if items, err := store.ClaimChannelDeliveryQueries(ctx, 1); err != nil || len(items) > 0 {
		t.Fatal(items, err)
	}
}

func TestDingTalkNonReplyCannotUseSessionWebhook(t *testing.T) {
	for _, kind := range []string{"fixed", "task_reply:" + manager.NewID(), "human_confirmation"} {
		t.Run(kind, func(t *testing.T) {
			_, store, d := dingSessionReplyFixture(t, kind, "text")
			v := manager.DeliveryCompletion{LeaseToken: d.LeaseToken, TargetDigest: d.TargetDigest, Status: "sent",
				EvidenceType: "session_webhook", EvidenceID: "original-message"}
			assertChannelError(t, store.CompleteChannelDelivery(t.Context(), d.ID, v), "receipt_evidence_required")
		})
	}
}

func TestDingTalkSessionReplyCannotReplaceActiveQueryReceipt(t *testing.T) {
	_, store, d := dingSessionReplyFixture(t, "reply", "text")
	v := manager.DeliveryCompletion{LeaseToken: d.LeaseToken, TargetDigest: d.TargetDigest, Status: "accepted",
		EvidenceType: "send_response", EvidenceID: "active-request", ProviderRequestID: "active-request"}
	if err := store.CompleteChannelDelivery(t.Context(), d.ID, v); err != nil {
		t.Fatal(err)
	}
	v.Status, v.EvidenceType, v.EvidenceID, v.ProviderRequestID = "sent", "session_webhook", "original-message", ""
	assertChannelError(t, store.CompleteChannelDelivery(t.Context(), d.ID, v), "receipt_mismatch")
}
