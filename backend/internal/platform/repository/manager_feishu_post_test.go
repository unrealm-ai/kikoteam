package repository

import (
	"context"
	"testing"
)

func TestManagerFeishuPostUsesNormalPermissionPath(t *testing.T) {
	db, m, s, _, _, _, binding := channelManagerFixture(t)
	ctx := context.Background()
	v := inboundFixture(binding, "post-allowed", "ou_a")
	v.MessageType = "post"
	v.Text = "# 标题\n**正文**"
	if _, err := s.ReceiveChannel(ctx, v); err != nil {
		t.Fatal(err)
	}
	if err := s.ProcessChannelInbox(ctx, m); err != nil {
		t.Fatal(err)
	}
	var text string
	if err := db.QueryRow(`SELECT i.text FROM manager_inputs i JOIN manager_channel_inbox n ON n.input_id=i.id WHERE n.provider_event_id='post-allowed'`).Scan(&text); err != nil || text != v.Text {
		t.Fatal(text, err)
	}
	if _, err := db.Exec(`UPDATE platform_channel_bindings SET access='{"mode":"allowlist","allow_ids":[],"deny_ids":[]}' WHERE id=$1`, binding); err != nil {
		t.Fatal(err)
	}
	v.ProviderEventID = "post-denied"
	v.ProviderMessageID = "post-denied"
	if _, err := s.ReceiveChannel(ctx, v); err != nil {
		t.Fatal(err)
	}
	if err := s.ProcessChannelInbox(ctx, m); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM manager_inputs i JOIN manager_channel_inbox n ON n.input_id=i.id WHERE n.provider_event_id='post-denied'`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}
