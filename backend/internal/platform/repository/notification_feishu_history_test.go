package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"os"
	"strings"
	"testing"
)

func feishuNoticeFixture(t *testing.T) (*sql.DB, *managerrepo.Store, string, string, string, string) {
	t.Helper()
	db, m, s, w, actor, agent, binding := channelManagerFixture(t)
	ctx := context.Background()
	v := inboundFixture(binding, "om_first", "ou_user")
	v.ChatType = "group"
	v.BotMentioned = true
	v.CreatedAtMS = 1000
	if _, err := s.ReceiveChannel(ctx, v); err != nil {
		t.Fatal(err)
	}
	if err := s.ProcessChannelInbox(ctx, m); err != nil {
		t.Fatal(err)
	}
	var conv, input string
	if err := db.QueryRow(`SELECT conversation_id,input_id FROM manager_channel_inbox WHERE provider_message_id='om_first'`).Scan(&conv, &input); err != nil {
		t.Fatal(err)
	}
	trigger, space, task, notice := manager.NewID(), manager.NewID(), manager.NewID(), manager.NewID()
	reviewExec(t, db, `INSERT INTO manager_triggers(id,workspace_id,agent_id,kind,enabled,config,allowed_actions,created_by)VALUES($1,$2,$3,'schedule',true,'{}','[]',$4)`, trigger, w, agent, actor)
	reviewExec(t, db, `INSERT INTO manager_task_spaces(id,workspace_id,created_by)VALUES($1,$2,$3)`, space, w, actor)
	reviewExec(t, db, `INSERT INTO manager_tasks(id,workspace_id,task_space_id,agent_id,trigger_id,trigger_version)VALUES($1,$2,$3,$4,$5,1)`, task, w, space, agent, trigger)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = managerrepo.SnapshotTriggerNotificationsTx(ctx, tx, trigger, task, 1, json.RawMessage(`[{"conversation_id":"`+conv+`"}]`))
	if err != nil {
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			t.Log(rollbackErr)
		}
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	reviewExec(t, db, `INSERT INTO manager_trigger_notifications(id,task_id,source_input_id,conversation_id,trigger_id,trigger_version,target,text,result_status,status,provider_message_id) SELECT $1,task_id,$2,conversation_id,trigger_id,trigger_version,target,'[系统消息]result','completed','sent','om_result' FROM manager_trigger_notification_targets WHERE task_id=$3`, notice, input, task)
	reviewExec(t, db, `UPDATE platform_agents SET usage_config=jsonb_set(usage_config,'{history}','{"enabled":true,"limit":20,"channels":["feishu"]}'::jsonb) WHERE id=$1`, agent)
	return db, s, notice, binding, conv, v.ExternalChatID
}
func TestFeishuNoticeFormatUpgradePreservesSentText(t *testing.T) {
	db, s, notice, _, _, _ := feishuNoticeFixture(t)
	raw, err := os.ReadFile("../../../../event-hub/database/postgres/migrations/000088_trigger_notification_format.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, found := strings.Cut(string(raw), "-- +goose Down")
	if !found {
		t.Fatal("missing down migration")
	}
	// Recreate the prior ledger layout with a real sent notice awaiting readback.
	reviewExec(t, db, down)
	reviewExec(t, db, up)
	jobs, err := s.ClaimNotificationContexts(context.Background(), 1)
	if err != nil || len(jobs) != 1 || jobs[0].ID != notice || jobs[0].TriggerFormat != "text" || jobs[0].TriggerText != "[系统消息]result" {
		t.Fatal(jobs, err)
	}
}

func TestFeishuNoticeHistoryLeaseAndAtomicBoundary(t *testing.T) {
	db, s, notice, binding, conv, chat := feishuNoticeFixture(t)
	const body = "[系统消息]结果\n\n- **完成**"
	reviewExec(t, db, `UPDATE manager_trigger_notifications SET content_format='markdown',text=$2 WHERE id=$1`, notice, body)
	ctx := context.Background()
	jobs, err := s.ClaimNotificationContexts(ctx, 1)
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	j := jobs[0]
	if j.TriggerFormat != "markdown" || j.TriggerText != body {
		t.Fatal(j)
	}
	again, err := s.ClaimNotificationContexts(ctx, 1)
	if err != nil || len(again) != 0 {
		t.Fatal("double lease", again, err)
	}
	if authorized, err := s.AuthorizeNotificationContext(ctx, notice, j.LeaseToken); err != nil || authorized != j {
		t.Fatal(authorized, err)
	}
	completion := manager.NotificationContextCompletion{LeaseToken: j.LeaseToken, AnchorID: "om_result", AnchorTime: 3000, ChatID: chat, Snapshot: manager.ChannelHistorySnapshot{Status: "complete", Items: []manager.ChannelHistoryItem{{MessageID: "om_history", SenderID: "ou_user", SenderType: "user", SenderName: "成员", Text: "prior", CreatedAtMS: 2000}}}}
	wrong := completion
	wrong.ChatID = "another_group"
	if err = s.CompleteNotificationContext(ctx, notice, wrong); err == nil {
		t.Fatal("cross group accepted")
	}
	if err = s.CompleteNotificationContext(ctx, notice, completion); err != nil {
		t.Fatal(err)
	}
	var count, triggerCount int
	var allowed bool
	if err = db.QueryRow(`SELECT count(*),count(*)FILTER(WHERE trigger_agent_loop),bool_and(manager_input_actor_allowed(id)) FROM manager_inputs WHERE notification_id=$1`, notice).Scan(&count, &triggerCount, &allowed); err != nil || count != 2 || triggerCount != 0 || !allowed {
		t.Fatal(count, triggerCount, allowed, err)
	}
	var anchor string
	var source *string
	if err = db.QueryRow(`SELECT trigger_message_id,inbox_id FROM manager_channel_history_boundaries WHERE conversation_id=$1`, conv).Scan(&anchor, &source); err != nil || anchor != "om_result" || source != nil {
		t.Fatal(anchor, source, err)
	}
	if err = s.CompleteNotificationContext(ctx, notice, completion); err == nil {
		t.Fatal("completed lease replay accepted")
	}
	reviewExec(t, db, `UPDATE platform_channel_bindings SET connection_generation=connection_generation+1 WHERE id=$1`, binding)
	if err = db.QueryRow(`SELECT bool_and(manager_input_actor_allowed(id)) FROM manager_inputs WHERE notification_id=$1`, notice).Scan(&allowed); err != nil || allowed {
		t.Fatal("old generation authority", allowed, err)
	}
}

func TestFeishuNoticeContextFailureDoesNotAdvance(t *testing.T) {
	for _, mode := range []string{"unavailable", "expired", "boundary", "revoked", "rollback"} {
		t.Run(mode, func(t *testing.T) {
			db, s, id, binding, conv, chat := feishuNoticeFixture(t)
			ctx := context.Background()
			jobs, err := s.ClaimNotificationContexts(ctx, 1)
			if err != nil || len(jobs) != 1 {
				t.Fatal(jobs, err)
			}
			j := jobs[0]
			v := manager.NotificationContextCompletion{LeaseToken: j.LeaseToken, AnchorID: "om_result", AnchorTime: 3000, ChatID: chat, Snapshot: manager.ChannelHistorySnapshot{Status: "complete", Items: []manager.ChannelHistoryItem{}}}
			switch mode {
			case "unavailable":
				v.Snapshot.Status = "unavailable"
			case "expired":
				reviewExec(t, db, `UPDATE manager_trigger_notifications SET context_lease_until=now()-interval '1 second' WHERE id=$1`, id)
			case "boundary":
				reviewExec(t, db, `UPDATE manager_channel_history_boundaries SET trigger_message_id='newer',trigger_created_at_ms=4000 WHERE conversation_id=$1`, conv)
			case "revoked":
				reviewExec(t, db, `UPDATE platform_channel_bindings SET enabled=false WHERE id=$1`, binding)
			case "rollback":
				reviewExec(t, db, `CREATE FUNCTION fail_notice_test() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.notification_id IS NOT NULL THEN RAISE EXCEPTION 'injected'; END IF;RETURN NEW;END $$;CREATE TRIGGER fail_notice_test BEFORE INSERT ON manager_inputs FOR EACH ROW EXECUTE FUNCTION fail_notice_test()`)
			}
			err = s.CompleteNotificationContext(ctx, id, v)
			if mode == "unavailable" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("unsafe completion accepted")
			}
			var count int
			var status, anchor string
			if err = db.QueryRow(`SELECT context_status FROM manager_trigger_notifications WHERE id=$1`, id).Scan(&status); err != nil {
				t.Fatal(err)
			}
			if mode == "unavailable" && status != "unavailable" {
				t.Fatal("unavailable rolled back", status)
			}
			if err = db.QueryRow(`SELECT count(*) FROM manager_inputs WHERE notification_id=$1`, id).Scan(&count); err != nil || count != 0 {
				t.Fatal(count, err)
			}
			if err = db.QueryRow(`SELECT trigger_message_id FROM manager_channel_history_boundaries WHERE conversation_id=$1`, conv).Scan(&anchor); err != nil {
				t.Fatal(err)
			}
			want := "om_first"
			if mode == "boundary" {
				want = "newer"
			}
			if anchor != want {
				t.Fatal(anchor, want)
			}
		})
	}
}
