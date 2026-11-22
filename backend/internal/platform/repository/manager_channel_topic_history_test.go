package repository

import (
	"context"
	"fmt"
	"testing"

	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func TestChannelTopicHistoryUsesOwnInputBoundary(t *testing.T) {
	db, m, s, _, _, _, binding := channelManagerFixture(t)
	ctx := context.Background()
	if _, err := db.Exec(`UPDATE platform_channel_bindings SET history='{"enabled":true,"limit":20}' WHERE id=$1`, binding); err != nil {
		t.Fatal(err)
	}
	for round := 1; round <= 2; round++ {
		for _, topic := range []string{"", "topic-a", "topic-b"} {
			event := fmt.Sprintf("event-%s-%d", topic, round)
			v := inboundFixture(binding, event, "ou_a")
			v.ChatType, v.ExternalChatID, v.ThreadID, v.BotMentioned, v.CreatedAtMS = "group", "same-parent", topic, true, int64(round*1000)
			r, err := s.ReceiveChannel(ctx, v)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.ProcessChannelInbox(ctx, m); err != nil {
				t.Fatal(err)
			}
			var n int
			if err = db.QueryRow(`SELECT count(*) FROM manager_inputs i JOIN manager_channel_inbox n ON n.input_id=i.id WHERE n.id=$1`, r.ID).Scan(&n); err != nil || n != 0 {
				t.Fatal("input before snapshot", n, err)
			}
			jobs, err := s.ClaimChannelHistory(ctx, 1)
			if err != nil || len(jobs) != 1 {
				t.Fatal(jobs, err)
			}
			job := jobs[0]
			wantPrevious := ""
			if round > 1 {
				wantPrevious = fmt.Sprintf("event-%s-%d", topic, round-1)
			}
			if job.ThreadID != topic || job.ChatID != "same-parent" || job.PreviousTriggerMessageID != wantPrevious {
				t.Fatal("cross-topic job", job)
			}
			authorized, err := s.AuthorizeChannelHistory(ctx, job.ID, job.LeaseToken)
			if err != nil || !authorized.Allowed || authorized.ThreadID != topic {
				t.Fatal(authorized, err)
			}
			history := fmt.Sprintf("history-%s-%d", topic, round)
			snapshot := manager.ChannelHistorySnapshot{Status: "complete", Items: []manager.ChannelHistoryItem{{MessageID: history, SenderID: "historical-user", SenderType: "user", Text: history, CreatedAtMS: v.CreatedAtMS - 100}}}
			if err = s.CompleteChannelHistory(ctx, job.ID, manager.ChannelHistoryCompletion{LeaseToken: job.LeaseToken, Snapshot: snapshot}); err != nil {
				t.Fatal(err)
			}
			if err = s.ProcessChannelInbox(ctx, m); err != nil {
				t.Fatal(err)
			}
			var conversation, session, actualTopic, boundary, context string
			var seq int
			if err = db.QueryRow(`SELECT n.conversation_id,i.session_id,c.channel_thread_id,h.trigger_message_id,i.seq FROM manager_channel_inbox n JOIN manager_inputs i ON i.id=n.input_id JOIN manager_conversations c ON c.id=n.conversation_id JOIN manager_channel_history_boundaries h ON h.conversation_id=c.id WHERE n.id=$1`, r.ID).Scan(&conversation, &session, &actualTopic, &boundary, &seq); err != nil {
				t.Fatal(err)
			}
			if actualTopic != topic || boundary != event || seq != round*2 {
				t.Fatal(actualTopic, boundary, seq)
			}
			if err = db.QueryRow(`SELECT text FROM manager_inputs WHERE session_id=$1 AND seq=$2 AND NOT trigger_agent_loop`, session, seq-1).Scan(&context); err != nil || context != "[说话人=成员2]"+history {
				t.Fatal(context, err)
			}
		}
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM manager_channel_history_boundaries`).Scan(&n); err != nil || n != 3 {
		t.Fatal(n, err)
	}
}

func TestChannelTopicHistoryUnavailableDoesNotMixParent(t *testing.T) {
	db, m, s, _, _, _, binding := channelManagerFixture(t)
	ctx := context.Background()
	if _, err := db.Exec(`UPDATE platform_channel_bindings SET history='{"enabled":true,"limit":20}' WHERE id=$1`, binding); err != nil {
		t.Fatal(err)
	}
	v := inboundFixture(binding, "topic-unavailable", "ou_a")
	v.ChatType, v.ThreadID, v.BotMentioned, v.CreatedAtMS = "group", "topic-one", true, 1000
	if _, err := s.ReceiveChannel(ctx, v); err != nil {
		t.Fatal(err)
	}
	if err := s.ProcessChannelInbox(ctx, m); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.ClaimChannelHistory(ctx, 1)
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	if err = s.CompleteChannelHistory(ctx, jobs[0].ID, manager.ChannelHistoryCompletion{LeaseToken: jobs[0].LeaseToken, Snapshot: manager.ChannelHistorySnapshot{Status: "unavailable", ErrorCode: "history_unavailable", Items: []manager.ChannelHistoryItem{}}}); err != nil {
		t.Fatal(err)
	}
	if err = s.ProcessChannelInbox(ctx, m); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = db.QueryRow(`SELECT count(*) FROM manager_inputs`).Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM manager_channel_history_boundaries h JOIN manager_conversations c ON c.id=h.conversation_id WHERE c.channel_thread_id=''`).Scan(&n); err != nil || n != 0 {
		t.Fatal("parent boundary changed", n, err)
	}
}

func TestDirectTopicNeverFetchesGroupHistory(t *testing.T) {
	db, m, s, _, _, _, binding := channelManagerFixture(t)
	ctx := context.Background()
	if _, err := db.Exec(`UPDATE platform_channel_bindings SET history='{"enabled":true,"limit":20}' WHERE id=$1`, binding); err != nil {
		t.Fatal(err)
	}
	v := inboundFixture(binding, "direct-topic", "ou_a")
	v.ThreadID, v.CreatedAtMS = "topic-one", 1000
	if _, err := s.ReceiveChannel(ctx, v); err != nil {
		t.Fatal(err)
	}
	if err := s.ProcessChannelInbox(ctx, m); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"manager_channel_history_jobs", "manager_channel_history_boundaries"} {
		var n int
		if err := db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil || n != 0 {
			t.Fatal(table, n, err)
		}
	}
	var text string
	if err := db.QueryRow(`SELECT text FROM manager_inputs`).Scan(&text); err != nil || text != "hello" {
		t.Fatal(text, err)
	}
}
