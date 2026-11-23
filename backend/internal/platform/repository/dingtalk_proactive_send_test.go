package repository

import (
	"database/sql"
	"encoding/json"
	"testing"

	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestDingTalkProactivePermissionObservationOrdering(t *testing.T) {
	db, store, first := confirmationFixture(t, "dingtalk", "direct", "")
	ctx := t.Context()
	observed := func(want bool) { t.Helper(); assertDingPermissionObservation(t, db, first.BindingID, want) }
	complete := func(id string, v manager.DeliveryCompletion) {
		t.Helper()
		if err := store.CompleteChannelDelivery(ctx, id, v); err != nil {
			t.Fatal(err)
		}
	}
	observed(false)
	deny := dingPermissionDenial(first)
	bad := deny
	bad.LeaseToken = manager.NewID()
	assertChannelError(t, store.CompleteChannelDelivery(ctx, first.ID, bad), "receipt_mismatch")
	observed(false)
	complete(first.ID, deny)
	observed(true)
	var state string
	if err := db.QueryRow(`SELECT status FROM manager_channel_deliveries WHERE id=$1`, first.ID).Scan(&state); err != nil || state != "unknown" {
		t.Fatal("permission observation changed transport status", state, err)
	}
	late := dingObservedChannelAttempt(t, db, first, "late", "-2 seconds", 1)
	success := dingObservedChannelAttempt(t, db, first, "success", "2 seconds", 1)
	accepted := manager.DeliveryCompletion{LeaseToken: success.LeaseToken, TargetDigest: success.TargetDigest, Status: "accepted",
		ProviderRequestID: "active-query", EvidenceType: "send_response", EvidenceID: "active-query"}
	complete(success.ID, accepted)
	observed(true)

	webhook := dingObservedChannelAttempt(t, db, first, "reply", "3 seconds", 1)
	reviewExec(t, db, `UPDATE manager_channel_inbox SET provider_message_id='original-message' WHERE id=(SELECT inbox_id FROM manager_channel_deliveries WHERE id=$1)`, webhook.ID)
	complete(webhook.ID, manager.DeliveryCompletion{LeaseToken: webhook.LeaseToken, TargetDigest: webhook.TargetDigest,
		Status: "sent", EvidenceType: "session_webhook", EvidenceID: "original-message"})
	observed(true)

	reviewExec(t, db, `UPDATE manager_channel_deliveries SET next_query_at=now() WHERE id=$1`, success.ID)
	queries, err := store.ClaimChannelDeliveryQueries(ctx, 1)
	if err != nil || len(queries) != 1 || queries[0].ID != success.ID {
		t.Fatal(queries, err)
	}
	accepted.Status, accepted.EvidenceType, accepted.QueryToken = "sent", "status_query", queries[0].QueryToken
	complete(success.ID, accepted)
	observed(false)
	complete(late.ID, dingPermissionDenial(late))
	observed(false)

	for _, code := range []string{"dingtalk_recipient_rejected", "dingtalk_proactive_rejected", "dingtalk_status_unknown", "dingtalk_message_recalled"} {
		d := dingObservedChannelAttempt(t, db, first, "ignored-"+code, "4 seconds", 1)
		v := dingPermissionDenial(d)
		v.ErrorCode = code
		complete(d.ID, v)
		observed(false)
	}

	fresh := dingObservedChannelAttempt(t, db, first, "fresh", "5 seconds", 1)
	complete(fresh.ID, dingPermissionDenial(fresh))
	observed(true)
	retired := dingObservedChannelAttempt(t, db, first, "retired", "1 day", 1)
	reviewExec(t, db, `UPDATE platform_channel_bindings SET enabled=false WHERE id=$1`, first.BindingID)
	observed(false)
	reviewExec(t, db, `UPDATE platform_channel_bindings SET enabled=true,connection_generation=2 WHERE id=$1`, first.BindingID)
	observed(false)
	complete(retired.ID, dingPermissionDenial(retired))
	observed(false)
	if err := db.QueryRow(`SELECT connection_generation FROM manager_dingtalk_proactive_send_observations WHERE binding_id=$1`, first.BindingID).Scan(&state); err != nil || state != "1" {
		t.Fatal("old receipt modified generation watermark", state, err)
	}
}

func dingObservedChannelAttempt(t *testing.T, db *sql.DB, original manager.ChannelDelivery, kind, age string, generation int64) manager.ChannelDelivery {
	t.Helper()
	d := original
	d.ID, d.LeaseToken = manager.NewID(), manager.NewID()
	reviewExec(t, db, `INSERT INTO manager_channel_deliveries(id,inbox_id,binding_id,connection_generation,kind,text,provider,status,lease_token,
 lease_until,attempt_target_digest,authorized_at)
 SELECT $2,inbox_id,binding_id,$3,$4,'synthetic','dingtalk','sending',$5,now()+interval '45 sec',attempt_target_digest,authorized_at+$6::interval
 FROM manager_channel_deliveries WHERE id=$1`, original.ID, d.ID, generation, kind, d.LeaseToken, age)
	return d
}

func dingPermissionDenial(d manager.ChannelDelivery) manager.DeliveryCompletion {
	return manager.DeliveryCompletion{LeaseToken: d.LeaseToken, TargetDigest: d.TargetDigest, Status: "unknown",
		EvidenceType: "send_response", ErrorCode: "dingtalk_proactive_permission_denied"}
}

func assertDingPermissionObservation(t *testing.T, db *sql.DB, binding string, want bool) {
	t.Helper()
	var workspace, owner, agent string
	if err := db.QueryRow(`SELECT b.workspace_id,a.user_id,b.agent_id FROM platform_channel_bindings b JOIN platform_members a
 ON a.workspace_id=b.workspace_id AND a.role='owner' WHERE b.id=$1`, binding).Scan(&workspace, &owner, &agent); err != nil {
		t.Fatal(err)
	}
	channels := service.ChannelService{Store: &ChannelStore{DB: db}}
	observed, err := channels.ObservedProvider(t.Context(), workspace, owner, agent, "dingtalk")
	if err != nil || observed.DingTalkProactiveSendPermissionMissing != want {
		t.Fatal("unexpected permission observation", observed.DingTalkProactiveSendPermissionMissing, err)
	}
	raw, err := json.Marshal(observed)
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]json.RawMessage
	if err = json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	value, exists := obj["dingtalk_proactive_send_permission_missing"]
	if exists != want || want && string(value) != "true" {
		t.Fatal("unexpected boolean wire representation", string(value), exists)
	}
}

func completeDingNoticeForTest(t *testing.T, store *managerrepo.Store, ledger, id string, v manager.DeliveryCompletion) {
	t.Helper()
	var err error
	switch ledger {
	case "human":
		err = store.CompleteHumanNotice(t.Context(), id, v)
	case "trigger":
		err = store.CompleteTriggerNotification(t.Context(), id, v)
	default:
		t.Fatalf("unknown test ledger %s", ledger)
	}
	if err != nil {
		t.Fatal(err)
	}
}

func TestDingTalkProactivePermissionAcrossNoticeLedgers(t *testing.T) {
	db, store, workspace, _, agent, _, input := triggerNoticeFixture(t)
	ctx := t.Context()
	binding, request := manager.NewID(), manager.NewID()
	reviewExec(t, db, `INSERT INTO platform_channel_bindings(id,workspace_id,agent_id,provider,enabled,app_id,external_tenant_id,credential_ciphertext,
 connection_generation,rules_version,config_revision,trigger,history,access)
 VALUES($1,$2,$3,'dingtalk',true,'fixture-app','fixture-tenant',''::bytea,1,1,1,'{}','{}','{}')`, binding, workspace, agent)
	reviewExec(t, db, `INSERT INTO manager_human_requests(id,workspace_id,agent_id,session_id,turn_id,input_id,
 authority_input_id,recipient,rules_version,question,stop_revision)
 SELECT $2,i.workspace_id,s.agent_id,s.id,i.turn_id,i.id,i.id,'{}',1,'{}',0 FROM manager_inputs i JOIN manager_sessions s ON s.id=i.session_id
 WHERE i.id=$1`, input, request)
	dm := manager.ChannelDelivery{ID: manager.NewID(), LeaseToken: manager.NewID(), TargetDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	reviewExec(t, db, `INSERT INTO manager_human_notices(id,request_id,request_version,recipient,workspace_id,agent_id,binding_id,provider,
 tenant_id,connection_generation,config_revision,identity_epoch,status,lease_token,attempt_target_digest,authorized_at)
 VALUES($1,$2,1,'{}',$3,$4,$5,'dingtalk','fixture-tenant',1,1,1,'sending',$6,$7,now()-interval '5 seconds')`,
		dm.ID, request, workspace, agent, binding, dm.LeaseToken, dm.TargetDigest)
	completeDingNoticeForTest(t, store, "human", dm.ID, dingPermissionDenial(dm))
	assertDingPermissionObservation(t, db, binding, true)

	endNoticeExecution(t, db, input)
	reviewExec(t, db, `UPDATE manager_trigger_notification_targets SET target=target||jsonb_build_object('provider','dingtalk','binding_id',$1::text,'chat_id','fixture-group','generation',1,'tenant_id','fixture-tenant')`, binding)
	if err := store.MaterializeTriggerNotifications(ctx); err != nil {
		t.Fatal(err)
	}
	var group manager.ChannelDelivery
	group.LeaseToken, group.TargetDigest = manager.NewID(), dm.TargetDigest
	key, query := "group-query", manager.NewID()
	reviewExec(t, db, `UPDATE manager_trigger_notifications SET status='accepted',lease_token=$2,attempt_target_digest=$3,
 provider_request_id=$4,query_token=$5,authorized_at=now() WHERE source_input_id=$1`, input, group.LeaseToken, group.TargetDigest, key, query)
	if err := db.QueryRow(`SELECT id FROM manager_trigger_notifications WHERE source_input_id=$1`, input).Scan(&group.ID); err != nil {
		t.Fatal(err)
	}
	sent := manager.DeliveryCompletion{LeaseToken: group.LeaseToken, TargetDigest: group.TargetDigest, Status: "sent",
		EvidenceType: "status_query", EvidenceID: key, ProviderRequestID: key, QueryToken: query}
	completeDingNoticeForTest(t, store, "trigger", group.ID, sent)
	assertDingPermissionObservation(t, db, binding, false)
}
