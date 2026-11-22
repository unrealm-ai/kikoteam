package repository

import (
	"context"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"testing"
)

func TestManagerWecomRecentHistorySnapshot(t *testing.T) {
	db, m, s, _, _, _, binding := channelManagerFixture(t)
	ctx := context.Background()
	if _, err := db.Exec(`UPDATE platform_channel_bindings SET provider='wecom',app_id='bot',bot_open_id='bot',external_tenant_id='bot',trigger='{"mention":false,"keywords":[],"regexes":[]}',history='{"enabled":true,"limit":20}' WHERE id=$1`, binding); err != nil {
		t.Fatal(err)
	}
	v := inboundFixture(binding, "wecom-trigger", "callback-user")
	v.ChatType = "group"
	v.ExternalChatID = "group"
	v.ExternalTenantID = "bot"
	v.SenderIDType = "user_id"
	v.BotID = "bot"
	v.ProviderDirected = true
	v.Text = "@助手 总结"
	v.QuotedText = "要总结的原文"
	v.CreatedAtMS = 100000
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
	j := jobs[0]
	if j.Strategy != "wecom_recent" || j.TriggerText != v.Text || j.ThreadID != "" {
		t.Fatal(j)
	}
	if _, err = s.AuthorizeChannelHistory(ctx, j.ID, j.LeaseToken); err != nil {
		t.Fatal(err)
	}
	snapshot := manager.ChannelHistorySnapshot{Status: "complete", Items: []manager.ChannelHistoryItem{{RecordKey: "page:0:record:0", SenderID: "unverified-search-id", SenderName: "历史成员", SenderType: "unknown", MessageType: "text", Text: "背景", CreatedAtMS: 99000}}}
	if err = s.CompleteChannelHistory(ctx, j.ID, manager.ChannelHistoryCompletion{LeaseToken: j.LeaseToken, Snapshot: snapshot}); err != nil {
		t.Fatal(err)
	}
	if err = s.ProcessChannelInbox(ctx, m); err != nil {
		t.Fatal(err)
	}
	var text, source string
	var trigger bool
	if err = db.QueryRow(`SELECT text,source_kind,trigger_agent_loop FROM manager_inputs ORDER BY seq LIMIT 1`).Scan(&text, &source, &trigger); err != nil || text != "[说话人=历史成员]背景" || source != "user" || trigger {
		t.Fatal(text, source, trigger, err)
	}
	if err = db.QueryRow(`SELECT i.text FROM manager_inputs i JOIN manager_channel_inbox n ON n.input_id=i.id WHERE n.provider_event_id=$1`, v.ProviderEventID).Scan(&text); err != nil || text != "[说话人=成员1]@助手 总结\n\n[用户引用的内容，原始身份未核验]\n要总结的原文" {
		t.Fatal(text, err)
	}
	var principals int
	if err = db.QueryRow(`SELECT count(*) FROM manager_channel_principals WHERE external_user_id='unverified-search-id'`).Scan(&principals); err != nil || principals != 0 {
		t.Fatal("history speaker became an actor", principals, err)
	}
}
