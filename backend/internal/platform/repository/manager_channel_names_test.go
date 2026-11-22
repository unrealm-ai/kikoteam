package repository

import (
	"context"
	"strings"
	"testing"

	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func TestChannelMessageNamesPreserveAuthorityAndPrivateText(t *testing.T) {
	db, m, s, _, _, _, binding := channelManagerFixture(t)
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO platform_channel_directory_state(binding_id,identity_epoch,connection_generation,external_tenant_id,full_snapshot,expires_at) VALUES($1,1,1,'tenant',true,now()+interval '1 hour')`, binding); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO platform_channel_directory_users(binding_id,user_id,display_name,status,department_ids,memberships_known) VALUES($1,'ou_a','张三','active','[]',true),($1,'ou_b','李四','active','[]',true)`, binding); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"direct", "group"} {
		v := inboundFixture(binding, "named-"+kind, "ou_a")
		v.ChatType = kind
		v.BotMentioned = true
		v.SenderIDType = "open_id"
		v.SenderName = "过时姓名"
		v.Text = "@_user_1 @_user_2 请找@_user_3，@_user_9未知"
		v.Mentions = []manager.ChannelMention{{ID: "bot", Key: "@_user_1", Name: "机器人", IDType: "open_id"}, {ID: "ou_b", Key: "@_user_2", Name: "旧李四", IDType: "open_id"}, {ID: "ou_b", Key: "@_user_3", Name: "旧李四", IDType: "open_id"}}
		r, err := s.ReceiveChannel(ctx, v)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.ProcessChannelInbox(ctx, m); err != nil {
			t.Fatal(err)
		}
		var raw, text, sender, metadata string
		if err = db.QueryRow(`SELECT n.text,i.text,n.sender_id,n.message_metadata::text FROM manager_channel_inbox n JOIN manager_inputs i ON i.id=n.input_id WHERE n.id=$1`, r.ID).Scan(&raw, &text, &sender, &metadata); err != nil {
			t.Fatal(err)
		}
		want := v.Text
		if kind == "group" {
			want = "[说话人=张三][被@人=机器人,李四]请找@李四，@_user_9未知"
		}
		if raw != v.Text || text != want || sender != "ou_a" || !strings.Contains(metadata, "旧李四") || strings.Contains(text, "部门=") {
			t.Fatalf("raw=%q text=%q sender=%s metadata=%s", raw, text, sender, metadata)
		}
	}
}

func TestChannelSpeakerFallbackIsStableAndNamesDoNotCrossNamespace(t *testing.T) {
	db, m, s, _, _, _, binding := channelManagerFixture(t)
	ctx := context.Background()
	for n := 0; n < 3; n++ {
		sender := "unknown-a"
		if n == 1 {
			sender = "unknown-b"
		}
		v := inboundFixture(binding, []string{"first", "second", "third"}[n], sender)
		v.ExternalChatID = "same-group"
		v.ChatType = "group"
		v.BotMentioned = true
		v.SenderIDType = "open_id"
		if _, err := s.ReceiveChannel(ctx, v); err != nil {
			t.Fatal(err)
		}
		if err := s.ProcessChannelInbox(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := db.Query(`SELECT text FROM manager_inputs ORDER BY seq`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Error(err)
		}
	}()
	wants := []string{"[说话人=成员1]hello", "[说话人=成员2]hello", "[说话人=成员1]hello"}
	i := 0
	for rows.Next() {
		var text string
		if err = rows.Scan(&text); err != nil {
			t.Fatal(err)
		}
		if i >= len(wants) || text != wants[i] {
			t.Fatal(text, i)
		}
		i++
	}
	if err = rows.Err(); err != nil || i != 3 {
		t.Fatal(err, i)
	}
}

func TestChannelHistoricalNamesAndMentions(t *testing.T) {
	db, m, s, _, _, _, binding := channelManagerFixture(t)
	ctx := context.Background()
	if _, err := db.Exec(`UPDATE platform_channel_bindings SET history='{"enabled":true,"limit":20}' WHERE id=$1`, binding); err != nil {
		t.Fatal(err)
	}
	v := inboundFixture(binding, "history-names", "ou_a")
	v.ChatType = "group"
	v.BotMentioned = true
	v.CreatedAtMS = 1000
	v.SenderIDType = "open_id"
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
	item := manager.ChannelHistoryItem{MessageID: "prior-message", SenderID: "ou_b", SenderType: "user", SenderName: "历史姓名", SenderIDType: "open_id", Text: "@_user_1 请联系@_user_2", CreatedAtMS: 900, Mentions: []manager.ChannelMention{{ID: "bot", Key: "@_user_1", Name: "机器人", IDType: "open_id"}, {ID: "c", Key: "@_user_2", Name: "同事", IDType: "open_id"}}}
	system := manager.ChannelHistoryItem{MessageID: "system-history", SenderID: "bot-sender", SenderType: "app", SenderName: "Not a user", SenderIDType: "open_id", Text: "[系统消息]@_user_1 请找@_user_2，@_user_9", CreatedAtMS: 950, Mentions: item.Mentions}
	if err = s.CompleteChannelHistory(ctx, jobs[0].ID, manager.ChannelHistoryCompletion{LeaseToken: jobs[0].LeaseToken, Snapshot: manager.ChannelHistorySnapshot{Status: "complete", Items: []manager.ChannelHistoryItem{item, system}}}); err != nil {
		t.Fatal(err)
	}
	if err = s.ProcessChannelInbox(ctx, m); err != nil {
		t.Fatal(err)
	}
	var text string
	if err = db.QueryRow(`SELECT text FROM manager_inputs WHERE NOT trigger_agent_loop AND source_kind='user'`).Scan(&text); err != nil || text != "[说话人=历史姓名][被@人=机器人,同事]请联系@同事" {
		t.Fatal(text, err)
	}
	var triggers bool
	if err = db.QueryRow(`SELECT text,trigger_agent_loop FROM manager_inputs WHERE source_kind='system'`).Scan(&text, &triggers); err != nil || triggers || text != "[系统消息][被@人=机器人,同事]请找@同事，@_user_9" {
		t.Fatal(text, triggers, err)
	}
	var aliases int
	if err = db.QueryRow(`SELECT count(*) FROM manager_channel_speaker_aliases`).Scan(&aliases); err != nil || aliases != 1 {
		t.Fatal("system impersonated human", aliases, err)
	}
}

func TestChannelPresentationDoesNotBorrowNamesFromWrongIdentityScope(t *testing.T) {
	for _, tc := range []struct{ name, idType, tenant, expiry string }{
		{"other namespace", "user_id", "tenant", "1 hour"},
		{"other tenant", "open_id", "other-tenant", "1 hour"},
		{"expired directory", "open_id", "tenant", "-1 second"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, m, s, _, _, _, binding := channelManagerFixture(t)
			if _, err := db.Exec(`INSERT INTO platform_channel_directory_state(binding_id,identity_epoch,connection_generation,external_tenant_id,full_snapshot,expires_at) VALUES($1,1,1,'tenant',true,now()+$2::interval)`, binding, tc.expiry); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO platform_channel_directory_users(binding_id,user_id,display_name,status,department_ids,memberships_known) VALUES($1,'ou_a','不该泄露的缓存名','active','[]',true)`, binding); err != nil {
				t.Fatal(err)
			}
			v := inboundFixture(binding, "scope-test", "ou_a")
			v.ChatType = "group"
			v.BotMentioned = true
			v.SenderIDType = tc.idType
			v.SenderTenant = tc.tenant
			v.SenderName = "提供方姓名"
			v.Text = "@_user_1 hi"
			v.Mentions = []manager.ChannelMention{{ID: "ou_a", IDType: tc.idType, TenantKey: tc.tenant, Key: "@_user_1", Name: "提供方姓名"}}
			ctx := context.Background()
			r, err := s.ReceiveChannel(ctx, v)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.ProcessChannelInbox(ctx, m); err != nil {
				t.Fatal(err)
			}
			var text string
			if err = db.QueryRow(`SELECT i.text FROM manager_inputs i JOIN manager_channel_inbox n ON n.input_id=i.id WHERE n.id=$1`, r.ID).Scan(&text); err != nil || text != "[说话人=提供方姓名][被@人=提供方姓名]hi" {
				t.Fatal(text, err)
			}
		})
	}
}
