package repository

import (
	"context"
	"testing"
)

func TestManagerUnauthorizedChannelEventBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, chat, thread, text, code string
		mention                        bool
		replies                        int
	}{
		{"direct", "direct", "", "hello", "access_denied", false, 1},
		{"direct topic", "direct", "topic", "hello", "access_denied", false, 1},
		{"group mention", "group", "", "hello", "access_denied", true, 1},
		{"group topic mention", "group", "topic", "hello", "access_denied", true, 1},
		{"group keyword silent", "group", "", "help", "access_denied", false, 0},
		{"group topic keyword silent", "group", "topic", "help", "access_denied", false, 0},
		{"stale rules no reply", "group", "", "unmatched", "trigger_not_matched", false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, m, s, _, _, _, binding := channelManagerFixture(t)
			if _, err := db.Exec(`UPDATE platform_channel_bindings SET access='{"mode":"allowlist","allow_ids":[],"deny_ids":[]}', history='{"enabled":true,"limit":20}' WHERE id=$1`, binding); err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			v := inboundFixture(binding, "denied-event", "sender")
			v.ChatType, v.ThreadID, v.Text, v.BotMentioned = tc.chat, tc.thread, tc.text, tc.mention
			receipt, err := s.ReceiveChannel(ctx, v)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.ProcessChannelInbox(ctx, m); err != nil {
				t.Fatal(err)
			}
			var status, code string
			if err = db.QueryRow(`SELECT status,error_code FROM manager_channel_inbox WHERE id=$1`, receipt.ID).Scan(&status, &code); err != nil || status != "rejected" || code != tc.code {
				t.Fatalf("status=%s code=%s err=%v", status, code, err)
			}
			for _, table := range []string{"manager_inputs", "manager_channel_history_jobs", "manager_channel_history_boundaries"} {
				var n int
				if err = db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil || n != 0 {
					t.Fatalf("%s count=%d err=%v", table, n, err)
				}
			}
			var replies int
			if err = db.QueryRow(`SELECT count(*) FROM manager_channel_deliveries WHERE inbox_id=$1`, receipt.ID).Scan(&replies); err != nil || replies != tc.replies {
				t.Fatalf("replies=%d want=%d err=%v", replies, tc.replies, err)
			}
			replay, err := s.ReceiveChannel(ctx, v)
			if err != nil || replay.Status != "duplicate" {
				t.Fatalf("replay=%+v err=%v", replay, err)
			}
			if err = s.ProcessChannelInbox(ctx, m); err != nil {
				t.Fatal(err)
			}
			if err = db.QueryRow(`SELECT count(*) FROM manager_channel_deliveries WHERE inbox_id=$1`, receipt.ID).Scan(&replies); err != nil || replies != tc.replies {
				t.Fatalf("duplicate reply: %d %v", replies, err)
			}
			deliveries, err := s.ClaimChannelDeliveries(ctx, 10, true)
			if err != nil || len(deliveries) != tc.replies {
				t.Fatalf("claim=%+v err=%v", deliveries, err)
			}
			for _, delivery := range deliveries {
				if delivery.ThreadID != tc.thread || delivery.ReplyToMessageID != v.ProviderMessageID {
					t.Fatalf("lost topic delivery target: %+v", delivery)
				}
				authorized, err := s.AuthorizeChannelDelivery(ctx, delivery.ID, delivery.LeaseToken)
				if err != nil || !authorized.Allowed || authorized.ThreadID != tc.thread || authorized.ReplyToMessageID != v.ProviderMessageID {
					t.Fatalf("lost authorized topic target: %+v %v", authorized, err)
				}
			}
		})
	}
}
