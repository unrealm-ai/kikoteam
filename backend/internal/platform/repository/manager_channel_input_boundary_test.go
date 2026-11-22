package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

// Exercise the trusted storage path without pretending a scheduler or a public
// system-message endpoint exists. Event producers must use the same transaction.
func TestChannelSilentInputAndHistoryBoundaryAreAtomic(t *testing.T) {
	db, m, s, w, _, _, binding := channelManagerFixture(t)
	ctx := context.Background()
	if _, err := db.Exec(`UPDATE platform_channel_bindings SET history='{"enabled":true,"limit":20}',access='{"mode":"all","deny_ids":["historical-denied"]}' WHERE id=$1`, binding); err != nil {
		t.Fatal(err)
	}
	v := inboundFixture(binding, "silent-event", "ou_a")
	v.ChatType, v.ExternalChatID, v.BotMentioned, v.CreatedAtMS = "group", "group", true, 1000
	r, err := s.ReceiveChannel(ctx, v)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ProcessChannelInbox(ctx, m); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.ClaimChannelHistory(ctx, 1)
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	snapshot := manager.ChannelHistorySnapshot{Status: "complete", Items: []manager.ChannelHistoryItem{
		{MessageID: "before", SenderID: "historical-denied", SenderType: "user", Text: "earlier request", CreatedAtMS: 800},
		{MessageID: "fixed", SenderID: "another-bot", SenderType: "app", Text: "没有权限", CreatedAtMS: 900},
	}}
	if err = s.CompleteChannelHistory(ctx, jobs[0].ID, manager.ChannelHistoryCompletion{LeaseToken: jobs[0].LeaseToken, Snapshot: snapshot}); err != nil {
		t.Fatal(err)
	}
	var principal, conversation string
	if err = db.QueryRow(`UPDATE manager_channel_inbox SET status='processing' WHERE id=$1 RETURNING principal_id,conversation_id`, r.ID).Scan(&principal, &conversation); err != nil {
		t.Fatal(err)
	}
	actorCtx := manager.WithChannelActor(ctx, manager.ChannelActor{ID: principal, Generation: 1})
	session, _, err := m.CreateSession(actorCtx, w, principal, "", manager.CreateSession{ConversationID: conversation, Mode: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	actorCtx = manager.WithChannelActor(ctx, manager.ChannelActor{ID: principal, Generation: 1, InboxID: r.ID})
	rollback := errors.New("test rollback")
	appendEvent := func(fail bool) error {
		return s.WithSession(actorCtx, manager.Target{Workspace: w, Actor: principal, Session: session.ID}, func(tx manager.SessionTx) error {
			silent := false
			_, e := tx.InsertInput(manager.Input{ID: manager.NewID(), SessionID: session.ID, Text: "会议提醒", SourceKind: "system", TriggerAgentLoop: &silent, Status: "queued", CreatedAt: time.Now().UTC()})
			if e != nil {
				return e
			}
			if fail {
				return rollback
			}
			return nil
		})
	}
	if err = appendEvent(true); !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	for _, table := range []string{"manager_inputs", "manager_channel_history_boundaries"} {
		var n int
		if err = db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil || n != 0 {
			t.Fatal(table, n, err)
		}
	}
	if err = appendEvent(false); err != nil {
		t.Fatal(err)
	}
	var n, active int
	if err = db.QueryRow(`SELECT count(*),count(*) FILTER(WHERE trigger_agent_loop) FROM manager_inputs`).Scan(&n, &active); err != nil || n != 3 || active != 0 {
		t.Fatal(n, active, err)
	}
	var boundary, text string
	if err = db.QueryRow(`SELECT trigger_message_id FROM manager_channel_history_boundaries`).Scan(&boundary); err != nil || boundary != "silent-event" {
		t.Fatal(boundary, err)
	}
	if err = db.QueryRow(`SELECT text FROM manager_inputs ORDER BY seq DESC LIMIT 1`).Scan(&text); err != nil || text != "[系统消息]会议提醒" {
		t.Fatal(text, err)
	}
	if err = db.QueryRow(`SELECT text FROM manager_inputs ORDER BY seq LIMIT 1`).Scan(&text); err != nil || text != "[说话人=成员1]earlier request" {
		t.Fatal(text, err)
	}
	// Revoking the event actor does not discard background context. A different
	// authorized speaker can trigger the pending batch; history is not authority.
	if _, err = db.Exec(`UPDATE platform_channel_bindings SET access='{"mode":"all","deny_ids":["ou_a"]}' WHERE id=$1`, binding); err != nil {
		t.Fatal(err)
	}
	v.SenderID = "ou_b"
	// No native receipt is needed to make this the next history lower bound.
	v.ProviderEventID, v.ProviderMessageID, v.CreatedAtMS = "next", "next", 2000
	if _, err = s.ReceiveChannel(ctx, v); err != nil {
		t.Fatal(err)
	}
	if err = s.ProcessChannelInbox(ctx, m); err != nil {
		t.Fatal(err)
	}
	jobs, err = s.ClaimChannelHistory(ctx, 1)
	if err != nil || len(jobs) != 1 || jobs[0].PreviousTriggerMessageID != "silent-event" {
		t.Fatal(jobs, err)
	}
	if err = s.CompleteChannelHistory(ctx, jobs[0].ID, manager.ChannelHistoryCompletion{LeaseToken: jobs[0].LeaseToken, Snapshot: manager.ChannelHistorySnapshot{Status: "complete", Items: []manager.ChannelHistoryItem{}}}); err != nil {
		t.Fatal(err)
	}
	if err = s.ProcessChannelInbox(ctx, m); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM manager_inputs WHERE NOT trigger_agent_loop AND status='queued'`).Scan(&n); err != nil || n != 3 {
		t.Fatal("revocation removed history", n, err)
	}
}

func TestChannelTopicsHaveIndependentConversationsAndSessions(t *testing.T) {
	for _, kind := range []string{"direct", "group"} {
		t.Run(kind, func(t *testing.T) {
			db, m, s, _, _, _, binding := channelManagerFixture(t)
			ctx := context.Background()
			conversations, sessions := map[string]bool{}, map[string]bool{}
			for _, topic := range []string{"", "topic-a", "topic-b"} {
				for repeat := 0; repeat < 2; repeat++ {
					event := manager.NewID()
					v := inboundFixture(binding, event, "ou_a")
					v.ChatType, v.ExternalChatID, v.ThreadID, v.BotMentioned = kind, "same-parent", topic, true
					r, err := s.ReceiveChannel(ctx, v)
					if err != nil {
						t.Fatal(err)
					}
					if err = s.ProcessChannelInbox(ctx, m); err != nil {
						t.Fatal(err)
					}
					var conversation, session, actualTopic string
					if err = db.QueryRow(`SELECT n.conversation_id,i.session_id,c.channel_thread_id FROM manager_channel_inbox n JOIN manager_inputs i ON i.id=n.input_id JOIN manager_conversations c ON c.id=n.conversation_id WHERE n.id=$1`, r.ID).Scan(&conversation, &session, &actualTopic); err != nil || actualTopic != topic {
						t.Fatal(actualTopic, err)
					}
					if repeat == 0 && (conversations[conversation] || sessions[session]) {
						t.Fatal("topic reused another topic's execution")
					}
					if repeat == 1 && (!conversations[conversation] || !sessions[session]) {
						t.Fatal("same topic did not reuse its own session")
					}
					conversations[conversation], sessions[session] = true, true
				}
			}
			if len(conversations) != 3 || len(sessions) != 3 {
				t.Fatal(conversations, sessions)
			}
		})
	}
}
