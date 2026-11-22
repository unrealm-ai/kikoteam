package repository

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	managercontroller "github.com/unrealm-ai/kikoteam/backend/internal/manager/controller"
	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func confirmationFixture(t *testing.T, provider, chatType, threadID string) (*sql.DB, *managerrepo.Store, manager.ChannelDelivery) {
	t.Helper()
	db, _, store, _, _, _, binding := channelManagerFixture(t)
	ctx := context.Background()
	inbound := inboundFixture(binding, "delivery-confirmation", "sender")
	inbound.ChatType, inbound.ThreadID = chatType, threadID
	inbound.BotMentioned = chatType == "group"
	receipt, e := store.ReceiveChannel(ctx, inbound)
	if e != nil {
		t.Fatal(e)
	}
	if provider != "feishu" {
		if _, e := db.Exec(`UPDATE platform_channel_bindings SET provider=$2 WHERE id=$1`, binding, provider); e != nil {
			t.Fatal(e)
		}
	}
	id := manager.NewID()
	if _, e = db.Exec(`INSERT INTO manager_channel_deliveries(id,inbox_id,binding_id,connection_generation,kind,text)VALUES($1,$2,$3,1,'fixed','fixed notice')`, id, receipt.ID, binding); e != nil {
		t.Fatal(e)
	}
	deliveries, e := store.ClaimChannelDeliveries(ctx, 1, true)
	if e != nil || len(deliveries) != 1 {
		t.Fatal(deliveries, e)
	}
	d, e := store.AuthorizeChannelDelivery(ctx, id, deliveries[0].LeaseToken)
	if e != nil || !d.Allowed || len(d.TargetDigest) != 64 || d.Provider != provider {
		t.Fatal(d, e)
	}
	return db, store, d
}
func TestDeliveryLateReceiptIsEvidenceNotReauthorization(t *testing.T) {
	db, store, d := confirmationFixture(t, "feishu", "direct", "")
	ctx := context.Background()
	if _, e := db.Exec(`UPDATE manager_channel_deliveries SET lease_until=now()-interval '1 minute' WHERE id=$1`, d.ID); e != nil {
		t.Fatal(e)
	}
	if rows, e := store.ClaimChannelDeliveries(ctx, 5, true); e != nil || len(rows) != 0 {
		t.Fatal("unknown resend", rows, e)
	}
	if _, e := db.Exec(`UPDATE platform_channel_bindings SET enabled=false,connection_generation=connection_generation+1 WHERE id=$1`, d.BindingID); e != nil {
		t.Fatal(e)
	}
	v := manager.DeliveryCompletion{LeaseToken: d.LeaseToken, TargetDigest: d.TargetDigest, Status: "sent", ProviderMessageID: "real-message", EvidenceType: "send_response", EvidenceID: "real-message"}
	bad := v
	bad.TargetDigest = "0000000000000000000000000000000000000000000000000000000000000000"
	assertChannelError(t, store.CompleteChannelDelivery(ctx, d.ID, bad), "receipt_mismatch")
	bad = v
	bad.LeaseToken = manager.NewID()
	assertChannelError(t, store.CompleteChannelDelivery(ctx, d.ID, bad), "receipt_mismatch")
	bad = v
	bad.EvidenceID = "other"
	assertChannelError(t, store.CompleteChannelDelivery(ctx, d.ID, bad), "receipt_evidence_required")
	for range 2 {
		if e := store.CompleteChannelDelivery(ctx, d.ID, v); e != nil {
			t.Fatal(e)
		}
	}
	var state string
	var current bool
	var count int
	if e := db.QueryRow(`SELECT status FROM manager_channel_deliveries WHERE id=$1`, d.ID).Scan(&state); e != nil || state != "sent" {
		t.Fatal(state, e)
	}
	if e := db.QueryRow(`SELECT count(*),bool_or(authorization_current) FROM manager_channel_delivery_receipts WHERE delivery_id=$1`, d.ID).Scan(&count, &current); e != nil || count != 1 || current {
		t.Fatal(count, current, e)
	}
	v.Status = "unknown"
	v.EvidenceType = ""
	v.EvidenceID = ""
	if e := store.CompleteChannelDelivery(ctx, d.ID, v); e != nil {
		t.Fatal(e)
	}
	if e := db.QueryRow(`SELECT status FROM manager_channel_deliveries WHERE id=$1`, d.ID).Scan(&state); e != nil || state != "sent" {
		t.Fatal("late unknown downgraded sent", state, e)
	}
	if _, e := store.AuthorizeChannelDelivery(ctx, d.ID, d.LeaseToken); e == nil {
		t.Fatal("late receipt authorized another send")
	}
}
func TestDingTalkAcceptedOnlyQueriesOriginalRequest(t *testing.T) {
	db, store, d := confirmationFixture(t, "dingtalk", "direct", "")
	ctx := context.Background()
	v := manager.DeliveryCompletion{LeaseToken: d.LeaseToken, TargetDigest: d.TargetDigest, Status: "accepted", ProviderRequestID: "query-key", EvidenceType: "send_response", EvidenceID: "query-key"}
	if e := store.CompleteChannelDelivery(ctx, d.ID, v); e != nil {
		t.Fatal(e)
	}
	if rows, e := store.ClaimChannelDeliveries(ctx, 5, true); e != nil || len(rows) != 0 {
		t.Fatal("accepted resend", rows, e)
	}
	if _, e := db.Exec(`UPDATE manager_channel_deliveries SET next_query_at=now()-interval '1 second' WHERE id=$1`, d.ID); e != nil {
		t.Fatal(e)
	}
	rows, e := store.ClaimChannelDeliveryQueries(ctx, 5)
	if e != nil || len(rows) != 1 || rows[0].ProviderRequestID != "query-key" || rows[0].LeaseToken != d.LeaseToken || rows[0].Text != "" {
		t.Fatal(rows, e)
	}
	query := rows[0]
	v.QueryToken = query.QueryToken
	v.Status = "sent"
	v.EvidenceType = "status_query"
	bad := v
	bad.ProviderRequestID = "wrong-query-key"
	bad.EvidenceID = "wrong-query-key"
	assertChannelError(t, store.CompleteChannelDelivery(ctx, d.ID, bad), "receipt_mismatch")
	if e = store.CompleteChannelDelivery(ctx, d.ID, v); e != nil {
		t.Fatal(e)
	}
	if e = store.CompleteChannelDelivery(ctx, d.ID, v); e != nil {
		t.Fatal("completion retry failed", e)
	}
	if rows, e = store.ClaimChannelDeliveryQueries(ctx, 5); e != nil || len(rows) != 0 {
		t.Fatal(rows, e)
	}
}
func TestWeComRequiresMatchedBusinessACKShape(t *testing.T) {
	_, store, d := confirmationFixture(t, "wecom", "direct", "")
	ctx := context.Background()
	v := manager.DeliveryCompletion{LeaseToken: d.LeaseToken, TargetDigest: d.TargetDigest, Status: "sent", ProviderRequestID: "req-id", EvidenceType: "business_ack", EvidenceID: "req-id"}
	assertChannelError(t, store.CompleteChannelDelivery(ctx, d.ID, v), "receipt_evidence_required")
	v.ProviderSessionID = "verified-session"
	if e := store.CompleteChannelDelivery(ctx, d.ID, v); e != nil {
		t.Fatal(e)
	}
}
func TestDeliveryNoReturnToPendingAndQueryWindowBounded(t *testing.T) {
	db, store, d := confirmationFixture(t, "dingtalk", "direct", "")
	ctx := context.Background()
	v := manager.DeliveryCompletion{LeaseToken: d.LeaseToken, TargetDigest: d.TargetDigest, Status: "retry"}
	assertChannelError(t, store.CompleteChannelDelivery(ctx, d.ID, v), "invalid_argument")
	v.Status = "accepted"
	v.ProviderRequestID = "query-key"
	v.EvidenceType = "send_response"
	v.EvidenceID = "query-key"
	if e := store.CompleteChannelDelivery(ctx, d.ID, v); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Exec(`UPDATE manager_channel_deliveries SET query_attempts=5,next_query_at=now()-interval '1 second' WHERE id=$1`, d.ID); e != nil {
		t.Fatal(e)
	}
	if rows, e := store.ClaimChannelDeliveryQueries(ctx, 5); e != nil || len(rows) != 0 {
		t.Fatal(rows, e)
	}
	var state string
	if e := db.QueryRow(`SELECT status FROM manager_channel_deliveries WHERE id=$1`, d.ID).Scan(&state); e != nil || state != "unknown" {
		t.Fatal(state, e)
	}
}

func TestDingTalkGroupDeliveryHasNoPrivateRecipient(t *testing.T) {
	_, _, d := confirmationFixture(t, "dingtalk", "group", "")
	if d.ChatType != "group" || d.RecipientID != "" || d.ChatID == "" {
		t.Fatal(d)
	}
}

func TestFeishuTopicDeliveryConfirmationOverHTTP(t *testing.T) {
	for _, kind := range []string{"group", "direct"} {
		t.Run(kind, func(t *testing.T) {
			db, store, d := confirmationFixture(t, "feishu", kind, "omt_target")
			if d.ThreadID != "omt_target" || d.ChatID == "" || d.ReplyToMessageID == "" {
				t.Fatal("missing authorized topic", d)
			}
			token := strings.Repeat("h", 40)
			mux := http.NewServeMux()
			(&managercontroller.ChannelServer{Store: store, Token: token}).Routes(mux)
			complete := func(v manager.DeliveryCompletion, wantStatus, wantID, wantCode string) {
				t.Helper()
				body, err := json.Marshal(v)
				if err != nil {
					t.Fatal(err)
				}
				req := httptest.NewRequest(http.MethodPost, "/api/internal/v1/channel-deliveries/"+d.ID+"/complete", bytes.NewReader(body))
				req.Header.Set("Authorization", "Bearer "+token)
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, req)
				if w.Code != http.StatusOK {
					t.Fatalf("complete returned %d: %s", w.Code, w.Body.String())
				}
				var state, id, code string
				err = db.QueryRow(`SELECT status,COALESCE(provider_message_id,''),COALESCE(error_code,'') FROM manager_channel_deliveries WHERE id=$1`, d.ID).Scan(&state, &id, &code)
				if err != nil || state != wantStatus || id != wantID || code != wantCode {
					t.Fatalf("receipt %+v yielded %s, %q, %q: %v", v, state, id, code, err)
				}
			}
			const msgID = "om_reply"
			v := manager.DeliveryCompletion{LeaseToken: d.LeaseToken, TargetDigest: d.TargetDigest, Status: "sent", ProviderMessageID: msgID, EvidenceType: "send_response", EvidenceID: msgID}
			for _, scope := range [][2]string{{"", ""}, {d.ChatID, ""}, {"", d.ThreadID}, {"other_chat", d.ThreadID}, {d.ChatID, "other_thread"}} {
				v.ProviderChatID, v.ProviderThreadID = scope[0], scope[1]
				complete(v, "unknown", "", "thread_reply_receipt_unconfirmed")
			}
			ambiguous := v
			ambiguous.Status, ambiguous.ErrorCode = "unknown", "thread_reply_receipt_unconfirmed"
			complete(ambiguous, "unknown", "", ambiguous.ErrorCode)
			var recorded bool
			err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM manager_channel_delivery_receipts
 WHERE delivery_id=$1 AND receipt->>'status'='unknown' AND receipt->>'provider_message_id'=$2 AND receipt->>'provider_thread_id'=$3)`,
				d.ID, msgID, ambiguous.ProviderThreadID).Scan(&recorded)
			if err != nil || !recorded {
				t.Fatal("missing original provider evidence", err)
			}
			if rows, err := store.ClaimChannelDeliveries(context.Background(), 5, true); err != nil || len(rows) != 0 {
				t.Fatal("unconfirmed send was reclaimable", rows, err)
			}
			// A later receipt for this attempt can confirm the destination without a timestamp.
			v.ProviderChatID, v.ProviderThreadID = d.ChatID, d.ThreadID
			complete(v, "sent", msgID, "")
			v.ProviderThreadID = "other_thread"
			complete(v, "sent", msgID, "")
		})
	}
}
