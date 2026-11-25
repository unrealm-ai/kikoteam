package repository

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func verifyHumanNotice(t *testing.T, db *sql.DB, store *managerrepo.Store, ref service.SelectionRef, request string) {
	t.Helper()
	ctx := context.Background()
	items, err := store.ClaimHumanNotices(ctx, 10, false)
	if err != nil || len(items) != 1 {
		t.Fatalf("expected one notice, got %d: %v", len(items), err)
	}
	d := items[0]
	if d.Provider != ref.Provider || d.ExternalTenantID != ref.TenantID || d.RecipientID != ref.ExternalID || d.ChatType != "direct" || d.BindingID != ref.BindingID || !strings.Contains(d.Text, manager.HumanShortCode(request, 1)) {
		t.Fatal("wrong direct confirmation target")
	}
	if items, err = store.ClaimHumanNotices(ctx, 10, false); err != nil || len(items) != 0 {
		t.Fatal("duplicate active lease", err)
	}
	d, err = store.AuthorizeHumanNotice(ctx, d.ID, d.LeaseToken, false)
	if err != nil || !d.Allowed || d.TargetDigest == "" {
		t.Fatal("notice not authorized", err)
	}
	if _, err = store.AuthorizeHumanNotice(ctx, d.ID, d.LeaseToken, true); err != nil {
		t.Fatal("dispatch check", err)
	}
	reviewExec(t, db, `UPDATE platform_channel_bindings SET config_revision=config_revision+1 WHERE id=$1`, ref.BindingID)
	if _, err = store.AuthorizeHumanNotice(ctx, d.ID, d.LeaseToken, true); err == nil {
		t.Fatal("dispatch accepted a stale confirmation")
	}
	reviewExec(t, db, `UPDATE platform_channel_bindings SET config_revision=config_revision-1 WHERE id=$1`, ref.BindingID)
	v := manager.DeliveryCompletion{LeaseToken: d.LeaseToken, TargetDigest: d.TargetDigest, Status: "sent"}
	switch ref.Provider {
	case "feishu":
		v.HumanConfirmationCard = true
		v.EvidenceType = "send_response"
		v.ProviderChatID = "chat-" + ref.ExternalID
		v.ProviderMessageID = "om-confirmation"
		v.EvidenceID = v.ProviderMessageID
		reviewExec(t, db, `UPDATE manager_human_notices SET lease_until=now()-interval '1 second' WHERE id=$1`, d.ID)
		if items, err = store.ClaimHumanNotices(ctx, 10, false); err != nil || len(items) != 0 {
			t.Fatal("ambiguous send was re-leased", err)
		}
	case "wecom":
		v.EvidenceType = "business_ack"
		v.ProviderRequestID = "ack-123"
		v.ProviderSessionID = "socket-123"
		v.EvidenceID = v.ProviderRequestID
	case "dingtalk":
		v.Status = "accepted"
		v.EvidenceType = "send_response"
		v.ProviderRequestID = "query-key"
		v.EvidenceID = v.ProviderRequestID
		if err = store.CompleteHumanNotice(ctx, d.ID, v); err != nil {
			t.Fatal("DingTalk initial acceptance", err)
		}
		reviewExec(t, db, `UPDATE manager_human_notices SET next_query_at=now()-interval '1 second' WHERE id=$1`, d.ID)
		items, err = store.ClaimHumanNotices(ctx, 10, true)
		if err != nil || len(items) != 1 || items[0].ID != d.ID || items[0].Text != "" || items[0].QueryToken == "" {
			t.Fatalf("expected one evidence-only DingTalk query, got %d: %v", len(items), err)
		}
		v.Status = "sent"
		v.EvidenceType = "status_query"
		v.QueryToken = items[0].QueryToken
	}
	if ref.Provider != "feishu" {
		forged := v
		forged.ProviderMessageID = "not-a-real-quote-id"
		if err = store.CompleteHumanNotice(ctx, d.ID, forged); err == nil {
			t.Fatal("forged quote evidence accepted")
		}
	}
	if err = store.CompleteHumanNotice(ctx, d.ID, v); err != nil {
		t.Fatal("persist notice", err)
	}
	if err = store.CompleteHumanNotice(ctx, d.ID, v); err != nil {
		t.Fatal("replay persisted receipt", err)
	}
	if ref.Provider == "feishu" {
		action := manager.HumanCardAction{BindingID: d.BindingID, Provider: d.Provider, ExternalTenant: d.ExternalTenantID,
			ExternalActor: d.RecipientID, Generation: d.ConnectionGeneration, Revision: d.ConfigRevision,
			ProviderCardID: v.ProviderMessageID, ProviderChatID: v.ProviderChatID}
		if _, err = store.ReadHumanCard(ctx, d.ID, action); err != nil {
			t.Fatal("read verified native card", err)
		}
		wrong := action
		wrong.ExternalActor = "ou_wrong"
		if _, err = store.ReadHumanCard(ctx, d.ID, wrong); err == nil {
			t.Fatal("other actor read card")
		}
		wrong = action
		wrong.Revision++
		wrong.Decision = "reject"
		if _, err = store.AnswerHumanCard(ctx, d.ID, wrong); err == nil {
			t.Fatal("stale callback could submit")
		}
		bad := action
		bad.Decision = "approve"
		got, e := store.AnswerHumanCard(ctx, d.ID, bad)
		if e != nil || got.(map[string]any)["state"] != "needs_more" {
			t.Fatalf("native answer bypassed required fields: %v %v", got, e)
		}
	}
	var status, message, chat string
	if err = db.QueryRowContext(ctx, `SELECT status,COALESCE(provider_message_id,''),COALESCE(provider_chat_id,'') FROM manager_human_notices WHERE id=$1`, d.ID).Scan(&status, &message, &chat); err != nil || status != "sent" || message != v.ProviderMessageID || chat != v.ProviderChatID {
		t.Fatal("wrong durable notice evidence", err)
	}
}

func TestLegacyHumanReplyLateReceiptPreservesHistory(t *testing.T) {
	db, sessions, store, _, _, _, binding := channelManagerFixture(t)
	ctx := context.Background()
	reviewExec(t, db, `UPDATE platform_channel_bindings SET bot_open_id='bot' WHERE id=$1`, binding)
	inbound := inboundFixture(binding, "legacy-human-inbound", "recipient")
	inbound.SenderIDType = "open_id"
	inbox, err := store.ReceiveChannel(ctx, inbound)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.ProcessChannelInbox(ctx, sessions); err != nil {
		t.Fatal(err)
	}
	// Recreate an accepted Inbox and its captured text from the old fixed-reply path.
	reviewExec(t, db, `UPDATE manager_channel_inbox SET input_id=NULL WHERE id=$1`, inbox.ID)
	reviewExec(t, db, `INSERT INTO manager_human_dm_context(source_id,conversation_id,principal_id,connection_generation,admission_epoch,text,is_system)
 SELECT n.id,n.conversation_id,n.principal_id,n.connection_generation,a.admission_epoch,n.text,false
 FROM manager_channel_inbox n JOIN platform_channel_bindings b ON b.id=n.binding_id JOIN platform_agents a ON a.id=b.agent_id WHERE n.id=$1`, inbox.ID)
	id, lease := service.NewID(), service.NewID()
	const body = "答复已记录。"
	// An already authorized attempt keeps its identity for a late receipt.
	digest := strings.Repeat("a", 64)
	reviewExec(t, db, `INSERT INTO manager_channel_deliveries(id,inbox_id,binding_id,connection_generation,kind,text,status,provider,lease_token,lease_until,authorized_at,attempt_target_digest)
 VALUES($1,$2,$3,1,'human_confirmation',$4,'sending','feishu',$5,now()+interval '1 minute',now(),$6)`, id, inbox.ID, binding, body, lease, digest)
	receipt := manager.DeliveryCompletion{Status: "sent", LeaseToken: lease, TargetDigest: digest, ProviderMessageID: "legacy-provider-message", EvidenceType: "send_response", EvidenceID: "legacy-provider-message"}
	for range 2 {
		if err := store.CompleteChannelDelivery(ctx, id, receipt); err != nil {
			t.Fatal("legacy receipt could not be reconciled", err)
		}
	}
	var gotStatus, gotMessage, gotBody string
	var triggerAgentLoop bool
	if err = db.QueryRow(`SELECT d.status,d.provider_message_id,i.text,i.trigger_agent_loop FROM manager_channel_deliveries d
 JOIN manager_human_dm_context h ON h.source_id=d.id JOIN manager_inputs i ON i.id=h.input_id WHERE d.id=$1`, id).
		Scan(&gotStatus, &gotMessage, &gotBody, &triggerAgentLoop); err != nil || gotStatus != "sent" || gotMessage != receipt.ProviderMessageID || gotBody != "[系统消息]"+body || triggerAgentLoop {
		t.Fatal("legacy receipt did not restore passive history", gotStatus, gotMessage, gotBody, triggerAgentLoop, err)
	}
}
