package repository

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	managercontroller "github.com/unrealm-ai/kikoteam/backend/internal/manager/controller"
	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func humanRejectionFixture(t *testing.T, db *sql.DB, store *managerrepo.Store, w, owner, a, task, input string) (string, string) {
	t.Helper()
	ctx := context.Background()
	approver := service.NewID()
	insertWorkspaceAccount(t, db, approver, "approver-rejection@example.test")
	reviewExec(t, db, `INSERT INTO platform_members(workspace_id,user_id,role) VALUES($1,$2,'member')`, w, approver)
	rules, err := store.HumanRecipients(ctx, w, owner, a, &manager.HumanRecipients{Version: 1, Recipients: []manager.HumanRecipient{{
		Ref: service.SelectionRef{Kind: "user", Provider: "web", WorkspaceID: w, AgentID: a, ExternalID: approver}, IsDefault: true,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	identity := manager.DelegationIdentity{SessionID: task, EngineID: service.NewID(), AssignmentID: service.NewID(), Generation: 1}
	if err = store.RegisterEngine(ctx, manager.Engine{ID: identity.EngineID, HostID: "human-rejection", Endpoint: "http://127.0.0.1:1", Capacity: 10}); err != nil {
		t.Fatal(err)
	}
	turn := service.NewID()
	reviewExec(t, db, `INSERT INTO manager_turns(id,workspace_id,session_id,input_id,turn_no,assignment_id,generation,status) VALUES($1,$2,$3,$4,1,$5,1,'running')`, turn, w, task, input, identity.AssignmentID)
	reviewExec(t, db, `UPDATE manager_inputs SET status='started',turn_id=$2 WHERE id=$1`, input, turn)
	reviewExec(t, db, `UPDATE manager_sessions SET active_turn_id=$2,engine_id=$3,assignment_id=$4,generation=1 WHERE id=$1`, task, turn, identity.EngineID, identity.AssignmentID)
	res, err := store.HumanTaskTool(ctx, manager.HumanRuntimeScope{Identity: identity, InputID: input}, "request", "", &manager.HumanQuestion{
		RecipientID: rules.Recipients[0].ID, RulesVersion: rules.Version, Question: "是否同意演练发布？", AnswerType: "decision",
	})
	if err != nil {
		t.Fatal(err)
	}
	r := res.(manager.HumanRequest)
	reviewExec(t, db, `UPDATE manager_turns SET status='completed',finished_at=now() WHERE id=$1`, turn)
	reviewExec(t, db, `UPDATE manager_inputs SET status='processed',finished_at=now() WHERE id=$1`, input)
	reviewExec(t, db, `UPDATE manager_sessions SET active_turn_id=NULL WHERE id=$1`, task)
	reviewExec(t, db, `UPDATE manager_human_requests SET state='pending',fenced_at=now() WHERE id=$1`, r.ID)
	return approver, r.ID
}

func humanRejectionHTTP(t *testing.T, sessions *manager.Service, store *managerrepo.Store, actor, w, request, body string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	(&managercontroller.Server{Service: sessions, Engines: store, Origin: "http://localhost",
		Authenticate: func(context.Context, string) (string, error) { return actor, nil },
	}).Routes(mux)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/"+w+"/human-requests/"+request+"/answer", strings.NewReader(body))
	r.Header.Set("Cookie", "kiko_session=fixture")
	r.Header.Set("X-CSRF-Protection", "1")
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, r)
	return res
}

func TestHumanRejectionNotifiesOnlyWebTaskCreatorOnce(t *testing.T) {
	ctx := context.Background()
	db, sessions, _, w, owner, a, _ := managerFixture(t)
	store := sessions.Store.(*managerrepo.Store)
	conversation, err := sessions.ResolveWebConversation(ctx, w, owner, a)
	if err != nil {
		t.Fatal(err)
	}
	chat, _, err := sessions.CreateSession(ctx, w, owner, "", manager.CreateSession{ConversationID: conversation.ID, Mode: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	create := manager.CreateSession{ConversationID: conversation.ID, Mode: "task", Input: &struct {
		Text string `json:"text"`
	}{Text: "运维演练"}}
	task, _, err := sessions.CreateSession(ctx, w, owner, "human-rejection-test", create)
	if err != nil {
		t.Fatal(err)
	}
	var input string
	if err = db.QueryRow(`SELECT initial_input_id FROM manager_sessions WHERE id=$1`, task.ID).Scan(&input); err != nil {
		t.Fatal(err)
	}
	approver, request := humanRejectionFixture(t, db, store, w, owner, a, task.ID, input)
	const answer = `{"version":"1","decision":"reject","text":"测试尚未完成。 password=internal-secret"}`
	for range 2 {
		out := humanRejectionHTTP(t, sessions, store, approver, w, request, answer)
		if out.Code != 200 || !strings.Contains(out.Body.String(), "rejected") {
			t.Fatalf("reject/replay: %d %s", out.Code, out.Body)
		}
	}
	if out := humanRejectionHTTP(t, sessions, store, approver, w, request, `{"version":"1","decision":"reject","text":"不能覆盖"}`); out.Code != 409 {
		t.Fatalf("conflicting rejection = %d %s", out.Code, out.Body)
	}
	var wg sync.WaitGroup
	reports := make(chan error, 4)
	for range 4 {
		wg.Go(func() { reports <- store.MaterializeHumanRejectionNotices(ctx) })
	}
	wg.Wait()
	close(reports)
	for report := range reports {
		if report != nil {
			t.Fatal(report)
		}
	}
	var outcome, text, dest string
	var messageCount, turns, resumed, inputs int
	var saved string
	err = db.QueryRow(`SELECT n.outcome,r.answer->>'text',
 (SELECT count(*) FROM manager_session_events WHERE event->>'source_kind'='human_rejection_notice'),
 (SELECT count(*) FROM manager_turns WHERE session_id=$2),
 (SELECT count(*) FROM manager_inputs WHERE session_id=$3 AND human_request_id=$1),
 (SELECT count(*) FROM manager_inputs WHERE session_id=$2)
 FROM manager_human_rejection_notices n JOIN manager_human_requests r ON r.id=n.request_id WHERE n.request_id=$1`, request, chat.ID, task.ID).
		Scan(&outcome, &saved, &messageCount, &turns, &resumed, &inputs)
	if err != nil || outcome != "web_published" || messageCount != 1 || turns != 0 || inputs != 0 || resumed != 0 || !strings.Contains(saved, "internal-secret") {
		t.Fatalf("unexpected rejection delivery outcome=%s count=%d turns=%d resume=%d inputs=%d saved=%q err=%v", outcome, messageCount, turns, resumed, inputs, saved, err)
	}
	err = db.QueryRow(`SELECT session_id,event->'content'->0->>'text' FROM manager_session_events WHERE event->>'source_kind'='human_rejection_notice'`).Scan(&dest, &text)
	if err != nil || dest != chat.ID || !strings.Contains(text, "拒绝原因：测试尚未完成。 [已隐藏]") || strings.Contains(text, "internal-secret") || strings.Contains(text, "不能覆盖") {
		t.Fatalf("recipient/body mismatch: %s %s %v", dest, text, err)
	}
	events, err := sessions.ListEvents(ctx, w, owner, chat.ID, "", "agent.message", "", 10, "backward")
	if err != nil || len(events.Items) != 1 {
		t.Fatalf("creator cannot read notice in main chat: %v %v", events, err)
	}
	if events, err = sessions.ListEvents(ctx, w, approver, chat.ID, "", "agent.message", "", 10, "backward"); err == nil || len(events.Items) != 0 {
		t.Fatalf("approver could read creator's private notice: %v %v", events, err)
	}
}

func TestHumanRejectionPrivateChannelOutboxRevalidatesInitiator(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		revoke, group, corrupt bool
	}{
		{name: "authorized"},
		{name: "revoked after queue", revoke: true},
		{name: "mismatched sender after queue", corrupt: true},
		{name: "group reason is never posted", group: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			db, sessions, store, w, owner, a, binding := channelManagerFixture(t)
			reviewExec(t, db, `UPDATE platform_channel_bindings SET bot_open_id='bound-bot' WHERE id=$1`, binding)
			src := inboundFixture(binding, "source-dispatch", "initiator")
			src.SenderIDType = "open_id"
			if tc.group {
				src.ChatType, src.ExternalChatID, src.BotMentioned = "group", "test-group", true
			}
			inbox, err := store.ReceiveChannel(ctx, src)
			if err != nil {
				t.Fatal(err)
			}
			if err = store.ProcessChannelInbox(ctx, sessions); err != nil {
				t.Fatal(err)
			}
			var conversation, chat, principal, origin string
			err = db.QueryRow(`SELECT n.conversation_id,i.session_id,n.principal_id,i.id FROM manager_channel_inbox n JOIN manager_inputs i ON i.id=n.input_id WHERE n.id=$1`, inbox.ID).Scan(&conversation, &chat, &principal, &origin)
			if err != nil {
				t.Fatal(err)
			}
			channelCtx := manager.WithChannelActor(ctx, manager.ChannelActor{ID: principal, Generation: 1})
			task, _, err := sessions.CreateSession(channelCtx, w, principal, "channel-rejection", manager.CreateSession{ConversationID: conversation, Mode: "task", Input: &struct {
				Text string `json:"text"`
			}{Text: "运维演练"}})
			if err != nil {
				t.Fatal(err)
			}
			var input string
			reviewExec(t, db, `UPDATE manager_tasks SET source_chat_session_id=$2,source_chat_input_id=$3 WHERE id=$1`, task.TaskID, chat, origin)
			if err = db.QueryRow(`SELECT initial_input_id FROM manager_sessions WHERE id=$1`, task.ID).Scan(&input); err != nil {
				t.Fatal(err)
			}
			approver, request := humanRejectionFixture(t, db, store, w, owner, a, task.ID, input)
			out := humanRejectionHTTP(t, sessions, store, approver, w, request, `{"version":"1","decision":"reject","text":"等待安全复核"}`)
			if out.Code != 200 {
				t.Fatalf("reject = %d %s", out.Code, out.Body)
			}
			for range 2 {
				if err = store.MaterializeHumanRejectionNotices(ctx); err != nil {
					t.Fatal(err)
				}
			}
			items, err := store.ClaimChannelDeliveries(ctx, 10, true)
			if tc.group {
				var outcome string
				if e := db.QueryRow(`SELECT outcome FROM manager_human_rejection_notices WHERE request_id=$1`, request).Scan(&outcome); err != nil || e != nil || len(items) != 0 || outcome != "suppressed" {
					t.Fatalf("group reason was not suppressed: deliveries=%d outcome=%q claim=%v query=%v", len(items), outcome, err, e)
				}
				return
			}
			if err != nil || len(items) != 1 {
				t.Fatalf("proactive denial notice count=%d error=%v", len(items), err)
			}
			d := items[0]
			if d.RecipientID != src.SenderID || d.ChatID != src.ExternalChatID || d.ReplyToMessageID != "" || d.ChatType != "direct" || !strings.Contains(d.Text, "等待安全复核") {
				t.Fatalf("outbox did not preserve intended private route: %+v", d)
			}
			if tc.revoke {
				reviewExec(t, db, `UPDATE platform_channel_bindings SET access=jsonb_set(access,'{deny_ids}', '["initiator"]') WHERE id=$1`, binding)
			}
			if tc.corrupt {
				reviewExec(t, db, `UPDATE manager_channel_inbox SET sender_id='other' WHERE id=$1`, inbox.ID)
			}
			allowed, err := store.AuthorizeChannelDelivery(ctx, d.ID, d.LeaseToken)
			if tc.revoke || tc.corrupt {
				assertChannelError(t, err, "access_revoked")
				return
			}
			if err != nil || !allowed.Allowed || allowed.TargetDigest == "" || allowed.ReplyToMessageID != "" {
				t.Fatalf("outbox not authorized: %+v %v", allowed, err)
			}
		})
	}
}
