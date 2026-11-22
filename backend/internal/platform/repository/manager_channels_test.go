package repository

import (
	"context"
	"database/sql"
	"errors"
	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"strings"
	"sync"
	"testing"
)

func channelManagerFixture(t *testing.T) (*sql.DB, *manager.Service, *managerrepo.Store, string, string, string, string) {
	t.Helper()
	db, m, _, w, actor, agent, _ := managerFixture(t)
	store := m.Store.(*managerrepo.Store)
	binding := manager.NewID()
	_, e := db.Exec(`INSERT INTO platform_channel_bindings(id,workspace_id,agent_id,provider,connection_generation,rules_version,config_revision,enabled,app_id,external_tenant_id,credential_ciphertext,trigger,history,access) VALUES($1,$2,$3,'feishu',1,1,1,true,'cli_test','tenant',decode('00','hex'),'{"mention":true,"keywords":["help"],"regexes":[]}','{"enabled":false,"limit":20}','{"mode":"all","allow_ids":[],"deny_ids":[],"allow_chat_ids":[],"deny_chat_ids":[]}')`, binding, w, agent)
	if e != nil {
		t.Fatal(e)
	}
	return db, m, store, w, actor, agent, binding
}
func inboundFixture(binding, event, sender string) manager.ChannelInbound {
	return manager.ChannelInbound{BindingID: binding, ConnectionGeneration: 1, ConfigRevision: 1, RulesVersion: 1, ProviderEventID: event, ProviderMessageID: event, ExternalTenantID: "tenant", ExternalChatID: "chat-" + sender, ChatType: "direct", SenderID: sender, Text: "hello", MessageType: "text"}
}
func assertChannelError(t *testing.T, e error, code string) {
	t.Helper()
	var v *manager.Error
	if !errors.As(e, &v) || v.Code != code {
		t.Fatalf("want %s got %v", code, e)
	}
}
func TestManagerChannelInboxIndependentIdentityAndPrivacy(t *testing.T) {
	db, m, s, w, owner, _, binding := channelManagerFixture(t)
	ctx := context.Background()
	v := inboundFixture(binding, "evt-1", "ou_a")
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for n := 0; n < 12; n++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := s.ReceiveChannel(ctx, v); errs <- e }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	if e := s.ProcessChannelInbox(ctx, m); e != nil {
		t.Fatal(e)
	}
	var input, session, conversation, principal string
	var web sql.NullString
	if e := db.QueryRow(`SELECT i.id,i.session_id,s.conversation_id,i.channel_principal_id,i.submitted_by FROM manager_inputs i JOIN manager_sessions s ON s.id=i.session_id JOIN manager_channel_inbox n ON n.input_id=i.id WHERE n.provider_event_id='evt-1'`).Scan(&input, &session, &conversation, &principal, &web); e != nil {
		t.Fatal(e)
	}
	if web.Valid {
		t.Fatal("channel input impersonated Web account")
	}
	var count int
	if e := db.QueryRow(`SELECT count(*) FROM manager_channel_inbox`).Scan(&count); e != nil || count != 1 {
		t.Fatal(count, e)
	}
	_, e := s.Conversation(ctx, w, owner, conversation)
	assertChannelError(t, e, "resource_not_found")
	_, e = s.Session(ctx, w, owner, session)
	assertChannelError(t, e, "resource_not_found")
	_, _, e = m.SubmitInput(ctx, w, owner, session, "web-attack", "read")
	assertChannelError(t, e, "resource_not_found")
	_, e = m.ControlSession(ctx, w, owner, session, false)
	assertChannelError(t, e, "resource_not_found")
	channelCtx := manager.WithChannelActor(ctx, manager.ChannelActor{ID: principal, Generation: 1})
	events, e := m.ListEvents(channelCtx, w, principal, session, "", "", "", 100, "backward")
	if e != nil || len(events.Items) < 2 {
		t.Fatalf("missing unified history: %v %v", events, e)
	}
	var eventText string
	for _, raw := range events.Items {
		eventText += string(raw)
	}
	eventText = strings.ReplaceAll(eventText, " ", "")
	if !strings.Contains(eventText, `"submitted_by":null`) || !strings.Contains(eventText, `"principal_type":"channel"`) {
		t.Fatal("lost source identity", eventText)
	}
	// Replays after processing remain durable duplicates, without a second Input.
	receipt, e := s.ReceiveChannel(ctx, v)
	if e != nil || receipt.Status != "duplicate" {
		t.Fatal(receipt, e)
	}
	if e = s.ProcessChannelInbox(ctx, m); e != nil {
		t.Fatal(e)
	}
	if e = db.QueryRow(`SELECT count(*) FROM manager_inputs`).Scan(&count); e != nil || count != 1 {
		t.Fatal(count, e)
	}
	if _, e = db.Exec(`UPDATE platform_channel_bindings SET access='{"mode":"allowlist","allow_ids":[],"deny_ids":[]}' WHERE id=$1`, binding); e != nil {
		t.Fatal(e)
	}
	if e = s.Dispatch(ctx); e != nil {
		t.Fatal(e)
	}
	var status string
	if e = db.QueryRow(`SELECT status FROM manager_inputs WHERE id=$1`, input).Scan(&status); e != nil || status != "rejected" {
		t.Fatal(status, e)
	}
}
func TestManagerChannelFixedReplyAndDeliveryLease(t *testing.T) {
	db, m, s, _, _, agent, binding := channelManagerFixture(t)
	ctx := context.Background()
	if _, e := db.Exec(`UPDATE platform_agents SET enabled=false WHERE id=$1`, agent); e != nil {
		t.Fatal(e)
	}
	r, e := s.ReceiveChannel(ctx, inboundFixture(binding, "disabled", "ou_a"))
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ProcessChannelInbox(ctx, m); e != nil {
		t.Fatal(e)
	}
	var count int
	if e = db.QueryRow(`SELECT count(*) FROM manager_inputs`).Scan(&count); e != nil || count != 0 {
		t.Fatal(count, e)
	}
	items, e := s.ClaimChannelDeliveries(ctx, 10, true)
	if e != nil || len(items) != 1 {
		t.Fatal(items, e)
	}
	v := items[0]
	if !strings.Contains(v.Text, "已停用") {
		t.Fatal(v.Text)
	}
	allowed, e := s.AuthorizeChannelDelivery(ctx, v.ID, v.LeaseToken)
	if e != nil || !allowed.Allowed || allowed.ChatID != "chat-ou_a" || allowed.ReplyToMessageID != "disabled" {
		t.Fatal(allowed, e)
	}
	if _, e = db.Exec(`UPDATE manager_channel_deliveries SET lease_until=now()-interval '1 second' WHERE id=$1`, v.ID); e != nil {
		t.Fatal(e)
	}
	items, e = s.ClaimChannelDeliveries(ctx, 10, true)
	if e != nil || len(items) != 0 {
		t.Fatal("unknown send blindly retried", items, e)
	}
	var status string
	if e = db.QueryRow(`SELECT status FROM manager_channel_deliveries WHERE inbox_id=$1`, r.ID).Scan(&status); e != nil || status != "unknown" {
		t.Fatal(status, e)
	}
}
func TestManagerChannelGroupFilterAndRevokedDelivery(t *testing.T) {
	db, m, s, _, _, _, binding := channelManagerFixture(t)
	ctx := context.Background()
	v := inboundFixture(binding, "group-1", "ou_a")
	v.ChatType = "group"
	v.ExternalChatID = "oc_group"
	if _, e := s.ReceiveChannel(ctx, v); e != nil {
		t.Fatal(e)
	}
	if e := s.ProcessChannelInbox(ctx, m); e != nil {
		t.Fatal(e)
	}
	var count int
	if e := db.QueryRow(`SELECT count(*) FROM manager_inputs`).Scan(&count); e != nil || count != 0 {
		t.Fatal(count, e)
	}
	v.ProviderEventID = "group-2"
	v.ProviderMessageID = "group-2"
	v.BotMentioned = true
	if _, e := s.ReceiveChannel(ctx, v); e != nil {
		t.Fatal(e)
	}
	if e := s.ProcessChannelInbox(ctx, m); e != nil {
		t.Fatal(e)
	}
	if e := db.QueryRow(`SELECT count(*) FROM manager_inputs`).Scan(&count); e != nil || count != 1 {
		t.Fatal(count, e)
	}
	// A canceled input generates a fixed safe final response, not task completion.
	if _, e := db.Exec(`UPDATE manager_inputs SET status='canceled',cancel_reason='session_stopped',finished_at=now()`); e != nil {
		t.Fatal(e)
	}
	if e := s.MaterializeChannelReplies(ctx); e != nil {
		t.Fatal(e)
	}
	items, e := s.ClaimChannelDeliveries(ctx, 5, true)
	if e != nil || len(items) != 1 {
		t.Fatal(items, e)
	}
	if items[0].Text != "本次处理已取消，未完成。" {
		t.Fatalf("cancellation must be explicit: %q", items[0].Text)
	}
	if _, e = db.Exec(`UPDATE platform_channel_bindings SET enabled=false WHERE id=$1`, binding); e != nil {
		t.Fatal(e)
	}
	_, e = s.AuthorizeChannelDelivery(ctx, items[0].ID, items[0].LeaseToken)
	assertChannelError(t, e, "access_revoked")
}

func TestManagerChannelHistoryFrozenAndDegrades(t *testing.T) {
	db, m, s, _, _, _, binding := channelManagerFixture(t)
	ctx := context.Background()
	if _, e := db.Exec(`UPDATE platform_channel_bindings SET history='{"enabled":true,"limit":2}' WHERE id=$1`, binding); e != nil {
		t.Fatal(e)
	}
	v := inboundFixture(binding, "history-1", "ou_a")
	v.ChatType = "group"
	v.ExternalChatID = "group-history"
	v.BotMentioned = true
	v.CreatedAtMS = 1000
	if _, e := s.ReceiveChannel(ctx, v); e != nil {
		t.Fatal(e)
	}
	if e := s.ProcessChannelInbox(ctx, m); e != nil {
		t.Fatal(e)
	}
	var count int
	if e := db.QueryRow(`SELECT count(*) FROM manager_inputs`).Scan(&count); e != nil || count != 0 {
		t.Fatal("input before history freeze", count, e)
	}
	jobs, e := s.ClaimChannelHistory(ctx, 1)
	if e != nil || len(jobs) != 1 {
		t.Fatal(jobs, e)
	}
	job := jobs[0]
	authorized, e := s.AuthorizeChannelHistory(ctx, job.ID, job.LeaseToken)
	if e != nil || !authorized.Allowed || authorized.ChatID != v.ExternalChatID || authorized.ExternalTenantID != "tenant" {
		t.Fatal(authorized, e)
	}
	completion := manager.ChannelHistoryCompletion{LeaseToken: job.LeaseToken, Snapshot: manager.ChannelHistorySnapshot{Status: "complete", Items: []manager.ChannelHistoryItem{{MessageID: "past", SenderID: "ou_other", SenderType: "user", MessageType: "text", Text: "quoted history", CreatedAtMS: 900}}}}
	bad := completion
	bad.Snapshot.Items = []manager.ChannelHistoryItem{{MessageID: "future", CreatedAtMS: 1001}}
	e = s.CompleteChannelHistory(ctx, job.ID, bad)
	assertChannelError(t, e, "invalid_argument")
	if e = s.CompleteChannelHistory(ctx, job.ID, completion); e != nil {
		t.Fatal(e)
	}
	if e = s.ProcessChannelInbox(ctx, m); e != nil {
		t.Fatal(e)
	}
	var text, original string
	if e = db.QueryRow(`SELECT i.text,n.text FROM manager_inputs i JOIN manager_channel_inbox n ON n.input_id=i.id WHERE n.provider_event_id='history-1'`).Scan(&text, &original); e != nil {
		t.Fatal(e)
	}
	if original != "hello" || text != "[说话人=成员1]hello" {
		t.Fatal(text, original)
	}
	var historyText string
	var triggers bool
	if e = db.QueryRow(`SELECT text,trigger_agent_loop FROM manager_inputs WHERE seq=1`).Scan(&historyText, &triggers); e != nil || historyText != "[说话人=成员2]quoted history" || triggers {
		t.Fatal(historyText, triggers, e)
	}
	if e = db.QueryRow(`SELECT count(*) FROM manager_inputs`).Scan(&count); e != nil || count != 2 {
		t.Fatal(count, e)
	}
	// Complete is immutable and cannot overwrite already used context.
	e = s.CompleteChannelHistory(ctx, job.ID, completion)
	assertChannelError(t, e, "lease_lost")
	var boundaries int
	if e = db.QueryRow(`SELECT count(*) FROM manager_channel_history_boundaries`).Scan(&boundaries); e != nil || boundaries != 1 {
		t.Fatal("queued input did not advance Input boundary", boundaries, e)
	}
	if _, e = db.Exec(`UPDATE manager_inputs SET context_state='committed',context_committed_at=now()`); e != nil {
		t.Fatal(e)
	}
	v.CreatedAtMS = 2000
	v.ProviderEventID = "history-timeout"
	v.ProviderMessageID = "history-timeout"
	if _, e = s.ReceiveChannel(ctx, v); e != nil {
		t.Fatal(e)
	}
	if e = s.ProcessChannelInbox(ctx, m); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE manager_channel_history_jobs SET deadline=now()-interval '1 second' WHERE status<>'completed'`); e != nil {
		t.Fatal(e)
	}
	if e = s.ProcessChannelInbox(ctx, m); e != nil {
		t.Fatal(e)
	}
	if e = db.QueryRow(`SELECT i.text FROM manager_inputs i JOIN manager_channel_inbox n ON n.input_id=i.id WHERE n.provider_event_id='history-timeout'`).Scan(&text); e != nil || text != "[说话人=成员1]hello" {
		t.Fatal(text, e)
	}
}

func TestManagerChannelHistoryInputBoundaryIndependentOfNativeReceipt(t *testing.T) {
	db, m, s, _, _, _, binding := channelManagerFixture(t)
	ctx := context.Background()
	if _, e := db.Exec(`UPDATE platform_channel_bindings SET history='{"enabled":true,"limit":100}' WHERE id=$1`, binding); e != nil {
		t.Fatal(e)
	}
	v := inboundFixture(binding, "first-trigger", "ou_a")
	v.ChatType = "group"
	v.ExternalChatID = "serial-group"
	v.BotMentioned = true
	v.CreatedAtMS = 1000
	if _, e := s.ReceiveChannel(ctx, v); e != nil {
		t.Fatal(e)
	}
	if e := s.ProcessChannelInbox(ctx, m); e != nil {
		t.Fatal(e)
	}
	jobs, e := s.ClaimChannelHistory(ctx, 1)
	if e != nil || len(jobs) != 1 {
		t.Fatal(jobs, e)
	}
	if jobs[0].PreviousTriggerMessageID != "" || jobs[0].Strategy != "since_last_trigger" {
		t.Fatal(jobs)
	}
	if e = s.CompleteChannelHistory(ctx, jobs[0].ID, manager.ChannelHistoryCompletion{LeaseToken: jobs[0].LeaseToken, Snapshot: manager.ChannelHistorySnapshot{Status: "complete", Items: []manager.ChannelHistoryItem{}}}); e != nil {
		t.Fatal(e)
	}
	if e = s.ProcessChannelInbox(ctx, m); e != nil {
		t.Fatal(e)
	}
	v.ProviderEventID = "second-trigger"
	v.ProviderMessageID = "second-trigger"
	v.CreatedAtMS = 2000
	v.Text = "second"
	r, e := s.ReceiveChannel(ctx, v)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ProcessChannelInbox(ctx, m); e != nil {
		t.Fatal(e)
	}
	// The second history job is available before the first Input reaches Pi.
	jobs, e = s.ClaimChannelHistory(ctx, 1)
	if e != nil || len(jobs) != 1 || jobs[0].PreviousTriggerMessageID != "first-trigger" {
		t.Fatal(jobs, e)
	}
	if _, e = db.Exec(`UPDATE manager_inputs SET context_state='unknown'`); e != nil {
		t.Fatal(e)
	}
	if e = s.CompleteChannelHistory(ctx, jobs[0].ID, manager.ChannelHistoryCompletion{LeaseToken: jobs[0].LeaseToken, Snapshot: manager.ChannelHistorySnapshot{Status: "complete", Items: []manager.ChannelHistoryItem{}}}); e != nil {
		t.Fatal(e)
	}
	if e = s.ProcessChannelInbox(ctx, m); e != nil {
		t.Fatal(e)
	}
	var text string
	if e = db.QueryRow(`SELECT i.text FROM manager_inputs i JOIN manager_channel_inbox n ON n.input_id=i.id WHERE n.id=$1`, r.ID).Scan(&text); e != nil || text != "[说话人=成员1]second" {
		t.Fatal(text, e)
	}
	var boundary string
	if e = db.QueryRow(`SELECT trigger_message_id FROM manager_channel_history_boundaries`).Scan(&boundary); e != nil || boundary != "second-trigger" {
		t.Fatal(boundary, e)
	}

	v.ProviderEventID = "third-trigger"
	v.ProviderMessageID = "third-trigger"
	v.CreatedAtMS = 3000
	if _, e = s.ReceiveChannel(ctx, v); e != nil {
		t.Fatal(e)
	}
	if e = s.ProcessChannelInbox(ctx, m); e != nil {
		t.Fatal(e)
	}
	jobs, e = s.ClaimChannelHistory(ctx, 1)
	if e != nil || len(jobs) != 1 || jobs[0].PreviousTriggerMessageID != "second-trigger" || jobs[0].PreviousTriggerCreatedAtMS != 2000 {
		t.Fatal("wrong admitted Input lower bound", jobs, e)
	}
}

func TestManagerChannelGroupMembershipSeparateFromDirectAllowlist(t *testing.T) {
	db, m, s, _, _, _, binding := channelManagerFixture(t)
	ctx := context.Background()
	if _, e := db.Exec(`UPDATE platform_channel_bindings SET access='{"mode":"allowlist","allow_ids":[],"deny_ids":[],"group_members_enabled":true}' WHERE id=$1`, binding); e != nil {
		t.Fatal(e)
	}
	direct := inboundFixture(binding, "private-denied", "ou_a")
	if _, e := s.ReceiveChannel(ctx, direct); e != nil {
		t.Fatal(e)
	}
	if e := s.ProcessChannelInbox(ctx, m); e != nil {
		t.Fatal(e)
	}
	group := inboundFixture(binding, "group-allowed", "ou_a")
	group.ChatType = "group"
	group.BotMentioned = true
	group.ExternalChatID = "members-group"
	if _, e := s.ReceiveChannel(ctx, group); e != nil {
		t.Fatal(e)
	}
	if e := s.ProcessChannelInbox(ctx, m); e != nil {
		t.Fatal(e)
	}
	var count int
	if e := db.QueryRow(`SELECT count(*) FROM manager_inputs`).Scan(&count); e != nil || count != 1 {
		t.Fatal(count, e)
	}
	if _, e := db.Exec(`UPDATE platform_channel_bindings SET access=access||'{"deny_ids":["ou_a"]}' WHERE id=$1`, binding); e != nil {
		t.Fatal(e)
	}
	var allowed bool
	if e := db.QueryRow(`SELECT manager_input_actor_allowed(id) FROM manager_inputs`).Scan(&allowed); e != nil || allowed {
		t.Fatal("blacklist did not override group", allowed, e)
	}
}

func TestManagerChannelIdentityEpochSeparatesReusedIDs(t *testing.T) {
	db, m, s, _, _, _, binding := channelManagerFixture(t)
	ctx := context.Background()
	v := inboundFixture(binding, "same-event", "same-user")
	v.ChatType = "group"
	v.BotMentioned = true
	v.ExternalChatID = "same-chat"
	if _, e := s.ReceiveChannel(ctx, v); e != nil {
		t.Fatal(e)
	}
	if e := s.ProcessChannelInbox(ctx, m); e != nil {
		t.Fatal(e)
	}
	var first string
	if e := db.QueryRow(`SELECT conversation_id FROM manager_channel_inbox LIMIT 1`).Scan(&first); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Exec(`UPDATE platform_channel_bindings SET app_id='another-app',identity_epoch=identity_epoch+1,connection_generation=connection_generation+1,external_tenant_id='tenant-2' WHERE id=$1`, binding); e != nil {
		t.Fatal(e)
	}
	v.ConnectionGeneration = 2
	v.ExternalTenantID = "tenant-2"
	r, e := s.ReceiveChannel(ctx, v)
	if e != nil || r.Status != "accepted" {
		t.Fatal("new identity dedup collided", r, e)
	}
	if e = s.ProcessChannelInbox(ctx, m); e != nil {
		t.Fatal(e)
	}
	var second string
	if e = db.QueryRow(`SELECT conversation_id FROM manager_channel_inbox WHERE id=$1`, r.ID).Scan(&second); e != nil || first == second {
		t.Fatal("cross-identity history reused", first, second, e)
	}
	var oldAllowed bool
	if e = db.QueryRow(`SELECT manager_input_actor_allowed(i.id) FROM manager_inputs i JOIN manager_sessions s ON s.id=i.session_id WHERE s.conversation_id=$1`, first).Scan(&oldAllowed); e != nil || oldAllowed {
		t.Fatal("old identity authorized", oldAllowed, e)
	}
}

func TestManagerChannelHistoryRevokedAfterFetchAndBeforeInput(t *testing.T) {
	for _, stage := range []string{"complete", "input", "queued", "limit"} {
		t.Run(stage, func(t *testing.T) {
			db, m, s, _, _, _, binding := channelManagerFixture(t)
			ctx := context.Background()
			if _, e := db.Exec(`UPDATE platform_channel_bindings SET history='{"enabled":true,"limit":2}' WHERE id=$1`, binding); e != nil {
				t.Fatal(e)
			}
			v := inboundFixture(binding, "history-race", "ou_a")
			v.ChatType = "group"
			v.BotMentioned = true
			v.CreatedAtMS = 1000
			if _, e := s.ReceiveChannel(ctx, v); e != nil {
				t.Fatal(e)
			}
			if e := s.ProcessChannelInbox(ctx, m); e != nil {
				t.Fatal(e)
			}
			jobs, e := s.ClaimChannelHistory(ctx, 1)
			if e != nil || len(jobs) != 1 {
				t.Fatal(jobs, e)
			}
			if _, e = s.AuthorizeChannelHistory(ctx, jobs[0].ID, jobs[0].LeaseToken); e != nil {
				t.Fatal(e)
			}
			revoke := func() {
				if _, e = db.Exec(`UPDATE platform_channel_bindings SET history='{"enabled":false,"limit":1}',config_revision=config_revision+1 WHERE id=$1`, binding); e != nil {
					t.Fatal(e)
				}
			}
			if stage == "complete" {
				revoke()
			}
			completion := manager.ChannelHistoryCompletion{LeaseToken: jobs[0].LeaseToken, Snapshot: manager.ChannelHistorySnapshot{Status: "complete", Items: []manager.ChannelHistoryItem{{MessageID: "past", SenderID: "other", Text: "must not enter", CreatedAtMS: 900}, {MessageID: "past2", SenderID: "other", Text: "second quote", CreatedAtMS: 901}}}}
			if e = s.CompleteChannelHistory(ctx, jobs[0].ID, completion); e != nil {
				t.Fatal(e)
			}
			if stage == "input" {
				revoke()
			}
			if e = s.ProcessChannelInbox(ctx, m); e != nil {
				t.Fatal(e)
			}
			if stage == "queued" || stage == "limit" {
				if stage == "queued" {
					revoke()
				} else {
					if _, e = db.Exec(`UPDATE platform_channel_bindings SET history='{"enabled":true,"limit":1}' WHERE id=$1`, binding); e != nil {
						t.Fatal(e)
					}
				}
				if e = s.Dispatch(ctx); e != nil {
					t.Fatal(e)
				}
				var status string
				if e = db.QueryRow(`SELECT status FROM manager_inputs WHERE trigger_agent_loop`).Scan(&status); e != nil || status != "rejected" {
					t.Fatal("revoked history reached execution", status, e)
				}
				var remaining int
				if e = db.QueryRow(`SELECT count(*) FROM manager_inputs WHERE NOT trigger_agent_loop AND status='queued'`).Scan(&remaining); e != nil || remaining != 0 {
					t.Fatal("history setting revoked but silent context retained", remaining, e)
				}
				return
			}
			var text string
			if e = db.QueryRow(`SELECT text FROM manager_inputs`).Scan(&text); e != nil || text != "[说话人=成员1]hello" {
				t.Fatal("revoked historical context consumed", text, e)
			}
		})
	}
}

func TestManagerChannelDispatchDoesNotDependOnWebOwner(t *testing.T) {
	db, m, s, w, owner, _, binding := channelManagerFixture(t)
	ctx := context.Background()
	if _, e := s.ReceiveChannel(ctx, inboundFixture(binding, "no-owner", "ou_independent")); e != nil {
		t.Fatal(e)
	}
	if e := s.ProcessChannelInbox(ctx, m); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Exec(`UPDATE platform_members SET status='disabled' WHERE workspace_id=$1 AND user_id=$2`, w, owner); e != nil {
		t.Fatal(e)
	}
	engine := manager.Engine{ID: manager.NewID(), HostID: "channel-independent-host", Endpoint: "http://127.0.0.1:19000", Capacity: 10}
	if e := s.RegisterEngine(ctx, engine); e != nil {
		t.Fatal(e)
	}
	if e := s.Dispatch(ctx); e != nil {
		t.Fatal(e)
	}
	var assigned string
	if e := db.QueryRow(`SELECT engine_id FROM manager_sessions LIMIT 1`).Scan(&assigned); e != nil || assigned != engine.ID {
		t.Fatal("channel scheduling impersonates or requires Web owner", assigned, e)
	}
}
