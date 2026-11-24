package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/mcp/taskdelegation"
	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
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

func TestChatTriggerTaskResultScope(t *testing.T) {
	db, store, w, owner, agent, conversation, source := triggerNoticeFixture(t)
	ctx := t.Context()
	member := manager.NewID()
	insertWorkspaceAccount(t, db, member, "trigger-result-member@example.test")
	reviewExec(t, db, `INSERT INTO platform_members(workspace_id,user_id,role,status) VALUES($1,$2,'member','active')`, w, member)
	reviewExec(t, db, `UPDATE platform_agents SET usage_config=jsonb_set(usage_config,'{access}','{"mode":"allowlist","group_members_enabled":true,"allow":[],"deny":[]}'::jsonb) WHERE id=$1`, agent)
	var group string
	if err := db.QueryRow(`SELECT web_group_id FROM manager_conversations WHERE id=$1`, conversation).Scan(&group); err != nil {
		t.Fatal(err)
	}
	secondGroup, otherGroup := manager.NewID(), manager.NewID()
	for _, g := range []string{secondGroup, otherGroup} {
		reviewExec(t, db, `INSERT INTO platform_web_groups(id,workspace_id,title,created_by) VALUES($1,$2,'other group',$3)`, g, w, owner)
		reviewExec(t, db, `INSERT INTO platform_web_group_agents(group_id,workspace_id,agent_id,status) VALUES($1,$2,$3,'active')`, g, w, agent)
	}
	for _, g := range []string{group, secondGroup, otherGroup} {
		reviewExec(t, db, `INSERT INTO platform_web_group_members(group_id,workspace_id,user_id,role,status) VALUES($1,$2,$3,'member','active')`, g, w, member)
	}
	secondConversation := manager.NewID()
	reviewExec(t, db, `INSERT INTO manager_conversations(id,workspace_id,agent_id,created_by,web_group_id,web_group_epoch,web_group_agent_epoch)
 VALUES($1,$2,$3,$4,$5,1,1)`, secondConversation, w, agent, owner, secondGroup)
	reviewExec(t, db, `INSERT INTO manager_sessions(id,workspace_id,agent_id,conversation_id,created_by,mode,configuration)
 VALUES($1,$2,$3,$4,$5,'chat','{}')`, manager.NewID(), w, agent, secondConversation, owner)
	var task, trigger string
	if err := db.QueryRow(`SELECT s.task_id,s.trigger_id FROM manager_inputs i JOIN manager_sessions s ON s.id=i.session_id WHERE i.id=$1`, source).Scan(&task, &trigger); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Roll back the test write on failure.
	targets := json.RawMessage(fmt.Sprintf(`[{"conversation_id":%q}]`, secondConversation))
	if err := managerrepo.SnapshotTriggerNotificationsTx(ctx, tx, trigger, task, 1, targets); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	endNoticeExecution(t, db, source)
	reviewExec(t, db, `UPDATE manager_turns SET status='failed',error='{"code":"event_persistence_failed","message":"private runtime error"}' WHERE input_id=$1`, source)
	if err := store.MaterializeTriggerNotifications(ctx); err != nil {
		t.Fatal(err)
	}
	var notice, taskSession string
	if err := db.QueryRow(`SELECT n.id,i.session_id FROM manager_trigger_notifications n JOIN manager_inputs i ON i.id=n.source_input_id
 WHERE n.source_input_id=$1 AND n.conversation_id=$2`, source, conversation).Scan(&notice, &taskSession); err != nil {
		t.Fatal(err)
	}
	var secondNotice string
	if err := db.QueryRow(`SELECT id FROM manager_trigger_notifications WHERE source_input_id=$1 AND conversation_id=$2`, source, secondConversation).Scan(&secondNotice); err != nil {
		t.Fatal(err)
	}
	reminderTrigger, reminderEvent, reminderNotice := manager.NewID(), manager.NewID(), manager.NewID()
	reviewExec(t, db, `INSERT INTO manager_triggers(id,workspace_id,agent_id,kind,config,allowed_actions,created_by)
 VALUES($1,$2,$3,'schedule','{}','["send_message"]',$4)`, reminderTrigger, w, agent, owner)
	reviewExec(t, db, `INSERT INTO manager_trigger_events(id,trigger_id,workspace_id,agent_id,binding_version,event_id,body_hash,action,payload,occurred_at)
 VALUES($1,$2,$3,$4,1,'reminder',$5,'send_message','{}',now())`, reminderEvent, reminderTrigger, w, agent, strings.Repeat("a", 64))
	reviewExec(t, db, `INSERT INTO manager_trigger_notifications(id,reminder_event_id,conversation_id,trigger_id,trigger_version,target,text,result_status,status,confirmed_at)
 SELECT $1,$2,conversation_id,$3,1,target,'fixed reminder','scheduled_reminder','sent',now() FROM manager_trigger_notifications WHERE id=$4`,
		reminderNotice, reminderEvent, reminderTrigger, notice)

	newInput := manager.NewID()
	reviewExec(t, db, `INSERT INTO manager_inputs(id,workspace_id,session_id,submitted_by,admission_epoch,seq,text,status)
 VALUES($1,$2,$3,$4,1,2,'new private input','processed')`, newInput, w, taskSession, owner)
	reviewExec(t, db, `INSERT INTO manager_turns(id,workspace_id,session_id,input_id,turn_no,assignment_id,generation,status,result_summary,finished_at)
 SELECT $1,workspace_id,session_id,$2,2,assignment_id,generation,'completed','new private result',now() FROM manager_turns WHERE input_id=$3`,
		manager.NewID(), newInput, source)

	engine := manager.NewID()
	secret := strings.Repeat("q", 40)
	m := &manager.Service{Store: store, CursorSecret: []byte(secret)}
	if err := store.RegisterEngine(ctx, manager.Engine{ID: engine, HostID: "trigger-results", Endpoint: "http://127.0.0.1:1", Capacity: 10}); err != nil {
		t.Fatal(err)
	}
	h, err := taskdelegation.Handler(&manager.Delegation{Manager: m, Store: store, Secret: secret}, nil)
	if err != nil {
		t.Fatal(err)
	}
	startChat := func(g string) string {
		t.Helper()
		msg, _, e := m.PostWebGroupMessage(ctx, w, member, g, "ask-for-trigger-result", manager.WebGroupPost{Text: "刚才那次任务为什么失败", MentionAgentIDs: []string{agent}})
		if e != nil {
			t.Fatal(e)
		}
		if e = store.ProcessWebGroupInbox(ctx); e != nil {
			t.Fatal(e)
		}
		if e = store.Dispatch(ctx); e != nil {
			t.Fatal(e)
		}
		id := manager.DelegationIdentity{EngineID: engine}
		var input string
		e = db.QueryRow(`SELECT s.id,s.assignment_id,s.generation,i.id FROM manager_web_group_inbox q
 JOIN manager_inputs i ON i.id=q.input_id JOIN manager_sessions s ON s.id=i.session_id WHERE q.message_id=$1`, msg.ID).
			Scan(&id.SessionID, &id.AssignmentID, &id.Generation, &input)
		if e != nil {
			t.Fatal(e)
		}
		reviewStartTurn(t, db, w, id.SessionID, input, id.AssignmentID, id.Generation)
		token, e := manager.SignDelegation(secret, id)
		if e != nil {
			t.Fatal(e)
		}
		return token
	}
	call := func(token, args string) string {
		t.Helper()
		body := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"trigger_task_get_result","arguments":%s}}`, args)
		r := httptest.NewRequest(http.MethodPost, "/mcp/task-delegation?catalog=stable", strings.NewReader(body))
		r.Header.Set("Accept", "application/json, text/event-stream")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+token)
		out := httptest.NewRecorder()
		h.ServeHTTP(out, r)
		if out.Code != http.StatusOK {
			t.Fatalf("status %d: %s", out.Code, out.Body)
		}
		return out.Body.String()
	}
	token := startChat(group)
	exact := fmt.Sprintf(`{"notification_id":%q}`, notice)
	for _, args := range []string{`{}`, exact} {
		reply := call(token, args)
		if !strings.Contains(reply, notice) || !strings.Contains(reply, "执行事件保存失败") ||
			!strings.Contains(reply, `"outcome":"failed"`) || strings.Contains(reply, "private") || strings.Contains(reply, reminderNotice) || strings.Contains(reply, secondNotice) {
			t.Fatal(reply)
		}
	}
	secondToken := startChat(secondGroup)
	secondExact := fmt.Sprintf(`{"notification_id":%q}`, secondNotice)
	for _, args := range []string{`{}`, secondExact} {
		if reply := call(secondToken, args); !strings.Contains(reply, secondNotice) || !strings.Contains(reply, "执行事件保存失败") || strings.Contains(reply, notice) {
			t.Fatal(reply)
		}
	}
	if reply := call(secondToken, exact); !strings.Contains(reply, "resource_not_found") {
		t.Fatal(reply)
	}
	if reply := call(token, fmt.Sprintf(`{"notification_id":%q}`, reminderNotice)); !strings.Contains(reply, "resource_not_found") {
		t.Fatal(reply)
	}
	if reply := call(startChat(otherGroup), exact); !strings.Contains(reply, "resource_not_found") {
		t.Fatal(reply)
	}
	reviewExec(t, db, `UPDATE manager_trigger_notifications SET status='unknown' WHERE id=$1`, notice)
	if reply := call(token, exact); !strings.Contains(reply, "resource_not_found") {
		t.Fatal(reply)
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
