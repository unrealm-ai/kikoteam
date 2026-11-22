package repository

import (
	"context"
	"database/sql"
	"sync"
	"testing"

	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func noticeContextFixture(t *testing.T) (*sql.DB, *managerrepo.Store, string, string, string, string) {
	t.Helper()
	db, m, s, w, actor, agent, group := webMessageFixture(t)
	ctx := context.Background()
	msg, _, err := m.PostWebGroupMessage(ctx, w, actor, group, "context-origin", manager.WebGroupPost{Text: "initial", MentionAgentIDs: []string{agent}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ProcessWebGroupInbox(ctx); err != nil {
		t.Fatal(err)
	}
	var conversation, input, session string
	if err = db.QueryRow(`SELECT n.conversation_id,n.input_id,i.session_id FROM manager_web_group_inbox n JOIN manager_inputs i ON i.id=n.input_id WHERE n.message_id=$1`, msg.ID).Scan(&conversation, &input, &session); err != nil {
		t.Fatal(err)
	}
	trigger, task, space, notice, result := manager.NewID(), manager.NewID(), manager.NewID(), manager.NewID(), manager.NewID()
	reviewExec(t, db, `INSERT INTO manager_triggers(id,workspace_id,agent_id,kind,enabled,config,allowed_actions,created_by)VALUES($1,$2,$3,'schedule',true,'{}','[]',$4)`, trigger, w, agent, actor)
	reviewExec(t, db, `INSERT INTO manager_task_spaces(id,workspace_id,created_by)VALUES($1,$2,$3)`, space, w, actor)
	reviewExec(t, db, `INSERT INTO manager_tasks(id,workspace_id,task_space_id,agent_id,trigger_id,trigger_version)VALUES($1,$2,$3,$4,$5,1)`, task, w, space, agent, trigger)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte(`[{"conversation_id":"` + conversation + `"}]`)
	if err = managerrepo.SnapshotTriggerNotificationsTx(ctx, tx, trigger, task, 1, raw); err != nil {
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			t.Log(rollbackErr)
		}
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	reviewExec(t, db, `INSERT INTO manager_web_group_messages(id,group_id,workspace_id,seq,author_kind,sender_id,display_name,text)VALUES($1,$2,$3,2,'user',$4,'Member','before result')`, manager.NewID(), group, w, actor)
	reviewExec(t, db, `INSERT INTO manager_web_group_messages(id,group_id,workspace_id,seq,author_kind,agent_id,display_name,text)VALUES($1,$2,$3,3,'system',$4,'Agent','[系统消息]task finished')`, result, group, w, agent)
	reviewExec(t, db, `UPDATE manager_web_group_streams SET last_seq=3 WHERE group_id=$1`, group)
	reviewExec(t, db, `UPDATE platform_agents SET usage_config=jsonb_set(usage_config,'{history}','{"enabled":true,"limit":20,"channels":["web"]}'::jsonb) WHERE id=$1`, agent)
	reviewExec(t, db, `INSERT INTO manager_trigger_notifications(id,task_id,source_input_id,conversation_id,trigger_id,trigger_version,target,text,result_status,status,web_message_id)SELECT $1,task_id,$2,conversation_id,trigger_id,trigger_version,target,'[系统消息]task finished','completed','sent',$3 FROM manager_trigger_notification_targets WHERE task_id=$4`, notice, input, result, task)
	return db, s, notice, session, conversation, group
}
func TestNotificationWebContextSilentAtomicAndIdempotent(t *testing.T) {
	db, s, id, session, conversation, _ := noticeContextFixture(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- s.ProcessTriggerNotificationContexts(ctx) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var state string
	var total, triggers, results int
	if err := db.QueryRow(`SELECT context_status FROM manager_trigger_notifications WHERE id=$1`, id).Scan(&state); err != nil || state != "admitted" {
		t.Fatal(state, err)
	}
	if err := db.QueryRow(`SELECT count(*),count(*)FILTER(WHERE trigger_agent_loop),count(*)FILTER(WHERE source_kind='task_result' AND text='[系统消息]task finished') FROM manager_inputs WHERE notification_id=$1`, id).Scan(&total, &triggers, &results); err != nil || total != 2 || triggers != 0 || results != 1 {
		t.Fatal(total, triggers, results, err)
	}
	var boundary int64
	if err := db.QueryRow(`SELECT web_group_boundary FROM manager_conversations WHERE id=$1`, conversation).Scan(&boundary); err != nil || boundary != 3 {
		t.Fatal(boundary, err)
	}
	var good bool
	if err := db.QueryRow(`SELECT bool_and(manager_input_actor_allowed(id) AND submitted_by IS NULL AND channel_principal_id IS NULL AND trigger_event_id IS NULL) FROM manager_inputs WHERE notification_id=$1 AND session_id=$2`, id, session).Scan(&good); err != nil || !good {
		t.Fatal("notification provenance", good, err)
	}
	if _, err := db.Exec(`UPDATE manager_inputs SET trigger_agent_loop=true WHERE notification_id=$1`, id); err == nil {
		t.Fatal("notification can trigger model")
	}
	if err := s.ProcessTriggerNotificationContexts(ctx); err != nil {
		t.Fatal(err)
	}
}
func TestNotificationContextDoesNotInventBoundaries(t *testing.T) {
	for _, mode := range []string{"unknown", "missing_anchor", "revoked", "passed"} {
		t.Run(mode, func(t *testing.T) {
			db, s, id, _, conversation, group := noticeContextFixture(t)
			switch mode {
			case "unknown":
				reviewExec(t, db, `UPDATE manager_trigger_notifications SET status='unknown' WHERE id=$1`, id)
			case "missing_anchor":
				reviewExec(t, db, `UPDATE manager_trigger_notifications SET web_message_id=NULL WHERE id=$1`, id)
			case "revoked":
				reviewExec(t, db, `UPDATE platform_web_group_agents SET status='removed',admission_epoch=admission_epoch+1 WHERE group_id=$1`, group)
			case "passed":
				reviewExec(t, db, `UPDATE manager_conversations SET web_group_boundary=4 WHERE id=$1`, conversation)
			}
			if err := s.ProcessTriggerNotificationContexts(context.Background()); err != nil {
				t.Fatal(err)
			}
			var count int
			var boundary int64
			if err := db.QueryRow(`SELECT count(*) FROM manager_inputs WHERE notification_id=$1`, id).Scan(&count); err != nil || count != 0 {
				t.Fatal("invented context", count, err)
			}
			expected := int64(1)
			if mode == "passed" {
				expected = 4
			}
			if err := db.QueryRow(`SELECT web_group_boundary FROM manager_conversations WHERE id=$1`, conversation).Scan(&boundary); err != nil || boundary != expected {
				t.Fatal(boundary, expected, err)
			}
		})
	}
}

// Failure between visible group insertion and silent admission rolls both back.
func TestNotificationWebSendContextRollbackTogether(t *testing.T) {
	db, s, id, _, _, group := noticeContextFixture(t)
	reviewExec(t, db, `UPDATE manager_trigger_notifications SET status='pending',web_message_id=NULL WHERE id=$1`, id)
	reviewExec(t, db, `DELETE FROM manager_web_group_messages WHERE group_id=$1 AND seq=3`, group)
	reviewExec(t, db, `UPDATE manager_web_group_streams SET last_seq=2 WHERE group_id=$1`, group)
	reviewExec(t, db, `CREATE FUNCTION reject_notice_context_test() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.notification_id IS NOT NULL THEN RAISE EXCEPTION 'injected context persistence failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_notice_context_test BEFORE INSERT ON manager_inputs FOR EACH ROW EXECUTE FUNCTION reject_notice_context_test()`)
	if err := s.MaterializeTriggerNotifications(context.Background()); err == nil {
		t.Fatal("injected failure not observed")
	}
	var status string
	var count int
	if err := db.QueryRow(`SELECT status FROM manager_trigger_notifications WHERE id=$1`, id).Scan(&status); err != nil || status != "pending" {
		t.Fatal(status, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM manager_web_group_messages WHERE group_id=$1 AND seq>2`, group).Scan(&count); err != nil || count != 0 {
		t.Fatal("visible delivery survived failed context", count, err)
	}
	reviewExec(t, db, `DROP TRIGGER reject_notice_context_test ON manager_inputs`)
	var wg sync.WaitGroup
	errs := make(chan error, 3)
	for range 3 {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- s.MaterializeTriggerNotifications(context.Background()) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := db.QueryRow(`SELECT count(*) FROM manager_web_group_messages WHERE group_id=$1 AND seq>2`, group).Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate visible delivery", count, err)
	}
	if err := db.QueryRow(`SELECT status||'/'||context_status FROM manager_trigger_notifications WHERE id=$1`, id).Scan(&status); err != nil || status != "sent/admitted" {
		t.Fatal(status, err)
	}
}

func TestNotificationWebQueueBackpressureDoesNotSend(t *testing.T) {
	db, s, id, session, _, group := noticeContextFixture(t)
	reviewExec(t, db, `UPDATE manager_trigger_notifications SET status='pending',web_message_id=NULL WHERE id=$1`, id)
	reviewExec(t, db, `DELETE FROM manager_web_group_messages WHERE group_id=$1 AND seq=3`, group)
	reviewExec(t, db, `UPDATE manager_web_group_streams SET last_seq=2 WHERE group_id=$1`, group)
	reviewExec(t, db, `INSERT INTO manager_inputs(id,workspace_id,session_id,submitted_by,admission_epoch,seq,text)SELECT gen_random_uuid(),workspace_id,session_id,submitted_by,admission_epoch,n,'queued' FROM manager_inputs CROSS JOIN generate_series(2,100)n WHERE session_id=$1 AND seq=1`, session)
	reviewExec(t, db, `UPDATE manager_sessions SET last_input_seq=100 WHERE id=$1`, session)
	if err := s.MaterializeTriggerNotifications(context.Background()); err == nil {
		t.Fatal("full context queue did not backpressure")
	}
	var state string
	var count int
	if err := db.QueryRow(`SELECT status FROM manager_trigger_notifications WHERE id=$1`, id).Scan(&state); err != nil || state != "pending" {
		t.Fatal(state, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM manager_web_group_messages WHERE group_id=$1 AND seq>2`, group).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}
