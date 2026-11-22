package repository

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestScheduledReminderNoModelAndDurableOccurrence(t *testing.T) {
	db, store, w, actor, agent, conv, _ := triggerNoticeFixture(t)
	ctx := context.Background()

	raw := json.RawMessage(`{"action":"send_message","cron":"* * * * *","timezone":"Asia/Shanghai","description":"固定提醒"}`)
	targets := json.RawMessage(`[{"conversation_id":"` + conv + `"}]`)
	b, err := store.SaveTrigger(ctx, w, actor, agent, "", manager.TriggerWrite{Kind: "schedule", Enabled: true, Config: raw, AllowedActions: []string{"send_message"}, NotificationTargets: targets})
	if err != nil {
		t.Fatal(err)
	}
	due := time.Now().UTC().Truncate(time.Minute).Add(-time.Minute)
	q := manager.TriggerScheduleTick{Version: b.Version, ExpectedDue: due, RecoveryStartedAt: due.Add(-time.Minute)}
	for n := 0; n < 2; n++ {
		if _, err = db.Exec(`UPDATE manager_triggers SET next_due=$2 WHERE id=$1`, b.ID, due); err != nil {
			t.Fatal(err)
		}
		r, e := store.TickTriggerSchedule(ctx, b.ID, q)
		if e != nil || r.Receipt == nil || r.Receipt.TaskID != "" || r.Receipt.Duplicate != (n == 1) {
			t.Fatal(r, e)
		}
	}
	var events, notices, tasks int
	if err = db.QueryRow(`SELECT (SELECT count(*) FROM manager_trigger_events WHERE trigger_id=$1),(SELECT count(*) FROM manager_trigger_notifications WHERE trigger_id=$1),(SELECT count(*) FROM manager_tasks WHERE trigger_id=$1)`, b.ID).Scan(&events, &notices, &tasks); err != nil || events != 1 || notices != 1 || tasks != 0 {
		t.Fatal(events, notices, tasks, err)
	}
	var status, text, format string
	if err = db.QueryRow(`SELECT status,text,content_format FROM manager_trigger_notifications WHERE trigger_id=$1`, b.ID).Scan(&status, &text, &format); err != nil || status != "pending" || text != "[系统消息]固定提醒" || format != "text" {
		t.Fatal(status, text, format, err)
	}
	if err = store.MaterializeTriggerNotifications(ctx); err != nil {
		t.Fatal(err)
	}
	var kind string
	var trigger bool
	if err = db.QueryRow(`SELECT n.status,i.source_kind,i.trigger_agent_loop FROM manager_trigger_notifications n JOIN manager_inputs i ON i.notification_id=n.id WHERE n.trigger_id=$1`, b.ID).Scan(&status, &kind, &trigger); err != nil || status != "sent" || kind != "system" || trigger {
		var cs, ce string
		if e := db.QueryRow(`SELECT status,context_status||':'||COALESCE(context_error,'') FROM manager_trigger_notifications WHERE trigger_id=$1`, b.ID).Scan(&cs, &ce); e != nil {
			t.Fatal(e)
		}
		t.Fatal(status, kind, trigger, err, cs, ce)
	}
}

func TestScheduledReminderSkipsReplacedChannelGroup(t *testing.T) {
	db, store, w, actor, agent, webGroup, _ := triggerNoticeFixture(t)
	ctx := context.Background()
	channels := &service.ChannelService{Store: NewChannelStore(db), Secret: []byte(strings.Repeat("t", 32))}
	secret := "synthetic-reminder-credential"
	put := service.ChannelPut{AppID: "cli_old", AppSecret: &secret, Enabled: true, History: service.ChannelHistory{Limit: 20}, Access: service.ChannelAccess{Mode: "allowlist"}}
	old, err := channels.Put(ctx, w, actor, agent, put)
	if err != nil {
		t.Fatal(err)
	}
	if err := channels.Apply(ctx, old.ID, service.ChannelApplied{Generation: old.Generation, Revision: old.Revision, RulesVersion: old.RulesVersion, Status: "connected", ObservedTenantID: "tenant", ObservedBotID: "ou_bot"}); err != nil {
		t.Fatal(err)
	}
	channelGroup := service.NewID()
	reviewExec(t, db, `INSERT INTO manager_conversations(id,workspace_id,agent_id,system_owned,channel_binding_id,channel_identity_epoch,channel_tenant_id,channel_chat_type,external_chat_id) VALUES($1,$2,$3,true,$4,$5,'tenant','group','oc_old')`, channelGroup, w, agent, old.ID, old.IdentityEpoch)
	targets, err := json.Marshal([]manager.TriggerNotificationTarget{{ConversationID: channelGroup}, {ConversationID: webGroup}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.SaveTrigger(ctx, w, actor, agent, "", manager.TriggerWrite{Kind: "schedule", Enabled: true,
		Name: "Reminder", Config: json.RawMessage(`{"action":"send_message","cron":"* * * * *","description":"Reminder"}`), AllowedActions: []string{"send_message"}, NotificationTargets: targets})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := channels.ProviderWithRebindImpact(ctx, w, actor, agent, "feishu")
	if err != nil || preview.RebindImpact == nil || strings.Join(preview.RebindImpact.NotificationNames, ",") != "Reminder" {
		t.Fatal(preview.RebindImpact, err)
	}
	put.Version, put.AppID = old.Revision, "cli_new"
	if _, err := channels.Put(ctx, w, actor, agent, put); err != nil {
		t.Fatal(err)
	}
	due := time.Now().UTC().Truncate(time.Minute).Add(-time.Minute)
	reviewExec(t, db, `UPDATE manager_triggers SET next_due=$2 WHERE id=$1`, b.ID, due)
	result, err := store.TickTriggerSchedule(ctx, b.ID, manager.TriggerScheduleTick{Version: b.Version, ExpectedDue: due, RecoveryStartedAt: due.Add(-time.Minute)})
	if err != nil || result.Receipt == nil {
		t.Fatal(result, err)
	}
	var total, matching int
	if err := db.QueryRow(`SELECT count(*),count(*) FILTER(WHERE conversation_id=$2) FROM manager_trigger_notifications WHERE trigger_id=$1`, b.ID, webGroup).Scan(&total, &matching); err != nil || total != 1 || matching != 1 {
		t.Fatal(total, matching, err)
	}
}

func TestScheduledReminderRejectsTaskParametersAndMissingTargets(t *testing.T) {
	_, store, w, actor, agent, _, _ := triggerNoticeFixture(t)
	for _, config := range []string{
		`{"action":"send_message","cron":"* * * * *","description":"固定提醒"}`,
		`{"action":"send_message","cron":"* * * * *","description":"固定提醒","businessKey":{"mode":"fixed","value":"not-a-task"}}`,
	} {
		_, err := store.SaveTrigger(context.Background(), w, actor, agent, "", manager.TriggerWrite{Kind: "schedule", Enabled: true, Config: json.RawMessage(config), AllowedActions: []string{"send_message"}})
		if err == nil {
			t.Fatal("invalid fixed reminder configuration accepted")
		}
	}
}
