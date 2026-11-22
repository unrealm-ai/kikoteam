package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"strings"
	"testing"
)

func triggerNoticeFixture(t *testing.T) (*sql.DB, *managerrepo.Store, string, string, string, string, string) {
	t.Helper()
	db, w, actor, agent, g := triggerRuntimeFixture(t)
	ctx := context.Background()
	store := &managerrepo.Store{DB: db}
	group, conv := manager.NewID(), manager.NewID()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := db.Exec(q, args...); e != nil {
			t.Fatal(e)
		}
	}
	exec(`INSERT INTO platform_web_groups(id,workspace_id,title,created_by)VALUES($1,$2,'notice group',$3)`, group, w, actor)
	exec(`INSERT INTO platform_web_group_members(group_id,workspace_id,user_id,role,status)VALUES($1,$2,$3,'owner','active')`, group, w, actor)
	exec(`INSERT INTO platform_web_group_agents(group_id,workspace_id,agent_id,status)VALUES($1,$2,$3,'active')`, group, w, agent)
	exec(`INSERT INTO manager_conversations(id,workspace_id,agent_id,created_by,web_group_id,web_group_epoch,web_group_agent_epoch)VALUES($1,$2,$3,$4,$5,1,1)`, conv, w, agent, actor, group)
	exec(`INSERT INTO manager_sessions(id,workspace_id,agent_id,conversation_id,created_by,mode,configuration)VALUES($1,$2,$3,$4,$5,'chat','{}')`, manager.NewID(), w, agent, conv, actor)
	var task string
	if e := db.QueryRow(`SELECT task_id FROM manager_trigger_events WHERE id=$1`, g.EventID).Scan(&task); e != nil {
		t.Fatal(e)
	}
	raw := json.RawMessage(`[{"conversation_id":"` + conv + `"}]`)
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = managerrepo.ValidateTriggerTargetsTx(ctx, tx, w, actor, agent, raw); e != nil {
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			t.Fatal(rollbackErr)
		}
		t.Fatal(e)
	}
	if e = managerrepo.SnapshotTriggerNotificationsTx(ctx, tx, g.TriggerID, task, 1, raw); e != nil {
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			t.Fatal(rollbackErr)
		}
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	return db, store, w, actor, agent, conv, g.InputID
}
func endNoticeExecution(t *testing.T, db *sql.DB, input string) {
	t.Helper()
	for _, q := range []string{`UPDATE manager_turns SET status='completed',result_summary='public result <think>private thought</think> token=secret /Users/a/private',finished_at=now()WHERE input_id=$1`, `UPDATE manager_inputs SET status='processed',finished_at=now()WHERE id=$1`, `UPDATE manager_sessions SET active_turn_id=NULL,background_process_count=0 WHERE id=(SELECT session_id FROM manager_inputs WHERE id=$1)`} {
		if _, e := db.Exec(q, input); e != nil {
			t.Fatal(e)
		}
	}
}
func TestTriggerNotificationWebStableAndIdempotent(t *testing.T) {
	db, store, w, actor, agent, conv, input := triggerNoticeFixture(t)
	ctx := context.Background()
	if e := store.MaterializeTriggerNotifications(ctx); e != nil {
		t.Fatal(e)
	}
	var count int
	if e := db.QueryRow(`SELECT count(*)FROM manager_trigger_notifications`).Scan(&count); e != nil || count != 0 {
		t.Fatal(count, e)
	}
	endNoticeExecution(t, db, input)
	if e := store.MaterializeTriggerNotifications(ctx); e != nil {
		t.Fatal(e)
	}
	if e := store.MaterializeTriggerNotifications(ctx); e != nil {
		t.Fatal(e)
	}
	var status, content, contextState string
	if e := db.QueryRow(`SELECT status,text,context_status FROM manager_trigger_notifications WHERE source_input_id=$1`, input).Scan(&status, &content, &contextState); e != nil {
		t.Fatal(e)
	}
	if status != "sent" || contextState != "admitted" || !strings.HasPrefix(content, "[系统消息]") || strings.Contains(content, "private thought") || strings.Contains(content, "token=secret") || strings.Contains(content, "/Users/") {
		t.Fatal(status, contextState, content)
	}
	if e := db.QueryRow(`SELECT count(*)FROM manager_web_group_messages`).Scan(&count); e != nil || count != 1 {
		t.Fatal(count, e)
	}
	candidates, _, e := store.TriggerNotificationCandidates(ctx, w, actor, agent, 100)
	if e != nil || len(candidates) != 1 || candidates[0].ConversationID != conv {
		t.Fatal(candidates, e)
	}
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Test read transaction.
	if managerrepo.ValidateTriggerTargetsTx(ctx, tx, w, actor, agent, json.RawMessage(`[{"conversation_id":"`+conv+`","chat_id":"forged"}]`)) == nil {
		t.Fatal("forged raw target")
	}
}
func TestTriggerNotificationReceiptUnknownNeverResends(t *testing.T) {
	db, store, _, _, _, _, input := triggerNoticeFixture(t)
	ctx := context.Background()
	endNoticeExecution(t, db, input)
	const summary = "**代码变更**\n\n- 提交 `40d77a3`\n  - token=secret"
	reviewExec(t, db, `UPDATE manager_turns SET result_summary=$2 WHERE input_id=$1`, input, summary)
	// Keep the source immutable but choose an external snapshot before discovery.
	if _, e := db.Exec(`UPDATE manager_trigger_notification_targets SET target=target||'{"provider":"feishu","binding_id":"11111111-1111-4111-8111-111111111111","chat_id":"real-group","generation":1}'::jsonb`); e != nil {
		t.Fatal(e)
	}
	if e := store.MaterializeTriggerNotifications(ctx); e != nil {
		t.Fatal(e)
	}
	ds, e := store.ClaimTriggerNotifications(ctx, 10)
	if e != nil || len(ds) != 1 {
		t.Fatal(ds, e)
	}
	d := ds[0]
	const want = "[系统消息]本次执行已结束（不代表整项任务完成）\n**代码变更**\n\n- 提交 `40d77a3`\n  - [已隐藏]"
	if d.Format != "markdown" || d.Text != want || d.ArtifactID != "" || d.Provider != "feishu" || d.ChatID != "real-group" {
		t.Fatalf("unexpected claimed notification: format=%q provider=%q chat=%q", d.Format, d.Provider, d.ChatID)
	}
	// A fixture represents a send already authorized before a later revocation.
	digest := strings.Repeat("a", 64)
	if _, e = db.Exec(`UPDATE manager_trigger_notifications SET status='sending',attempt_target_digest=$2,authorized_at=now(),lease_until=now()-interval '1 minute'WHERE id=$1`, d.ID, digest); e != nil {
		t.Fatal(e)
	}
	ds, e = store.ClaimTriggerNotifications(ctx, 10)
	if e != nil || len(ds) != 0 {
		t.Fatal("unknown resent", ds, e)
	}
	v := manager.DeliveryCompletion{LeaseToken: d.LeaseToken, TargetDigest: digest, Status: "sent", ProviderMessageID: "real-message", EvidenceType: "send_response", EvidenceID: "real-message"}
	bad := v
	bad.TargetDigest = strings.Repeat("b", 64)
	if store.CompleteTriggerNotification(ctx, d.ID, bad) == nil {
		t.Fatal("bad digest")
	}
	if e = store.CompleteTriggerNotification(ctx, d.ID, v); e != nil {
		t.Fatal(e)
	}
	if e = store.CompleteTriggerNotification(ctx, d.ID, v); e != nil {
		t.Fatal(e)
	}
	bad = v
	bad.ProviderMessageID = "different-message"
	bad.EvidenceID = "different-message"
	if store.CompleteTriggerNotification(ctx, d.ID, bad) == nil {
		t.Fatal("conflicting message evidence accepted")
	}
	var status string
	var receipts int
	if e = db.QueryRow(`SELECT status FROM manager_trigger_notifications WHERE id=$1`, d.ID).Scan(&status); e != nil || status != "sent" {
		t.Fatal(status, e)
	}
	if e = db.QueryRow(`SELECT count(*)FROM manager_trigger_notification_receipts WHERE notification_id=$1 AND NOT authorization_current`, d.ID).Scan(&receipts); e != nil || receipts != 1 {
		t.Fatal(receipts, e)
	}
	ds, e = store.ClaimTriggerNotifications(ctx, 10)
	if e != nil || len(ds) != 0 {
		t.Fatal(ds, e)
	}
}
