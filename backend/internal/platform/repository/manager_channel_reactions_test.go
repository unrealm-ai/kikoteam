package repository

import (
	"strings"
	"sync"
	"testing"

	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func TestManagerChannelReceiptReactionAfterDurableInput(t *testing.T) {
	for _, tc := range []struct {
		name, provider, scope string
		allow, media          bool
		wantClaim, wantTarget bool
	}{
		{"FeishuGranted", "feishu", "im:message.reactions:write_only", true, false, true, true},
		{"FeishuBroaderGrant", "feishu", "im:message", true, false, true, true},
		{"FeishuMissingScope", "feishu", "", true, false, true, false},
		{"FeishuStaleGrant", "feishu", "im:message.reactions:write_only", true, false, true, false},
		{"FeishuNonTrigger", "feishu", "im:message.reactions:write_only", true, false, false, false},
		{"FeishuDeniedSender", "feishu", "im:message.reactions:write_only", false, false, false, false},
		{"FeishuWaitsForMedia", "feishu", "im:message.reactions:write_only", true, true, true, true},
		{"DingTalk", "dingtalk", "", true, false, true, true},
		{"WeComUnchanged", "wecom", "", true, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, m, store, _, _, _, binding := channelManagerFixture(t)
			ctx := t.Context()
			must := func(e error) {
				t.Helper()
				if e != nil {
					t.Fatal(e)
				}
			}
			v := inboundFixture(binding, "transport-event", "staff")
			v.ProviderMessageID = "om_original"
			if tc.provider == "feishu" {
				reviewExec(t, db, `UPDATE platform_channel_bindings SET bot_open_id='ou_app' WHERE id=$1`, binding)
			} else {
				v.BotID, v.SenderIDType, v.SenderTenant = "robot_app", "user_id", "tenant"
				if tc.provider == "wecom" {
					v.BotID = "tenant"
				}
				reviewExec(t, db, `UPDATE platform_channel_bindings SET provider=$2, app_id='tenant', bot_open_id='' WHERE id=$1`, binding, tc.provider)
			}
			if tc.scope != "" {
				reviewExec(t, db, `INSERT INTO platform_feishu_permission_checks(binding_id,connection_generation,diagnostics)
 VALUES($1,1,jsonb_build_object('checked_at',now(),'scope_status','pass','permissions',jsonb_build_array(jsonb_build_object('scope',$2::text,'status','granted'))))`, binding, tc.scope)
			}
			if tc.name == "FeishuStaleGrant" {
				reviewExec(t, db, `UPDATE platform_feishu_permission_checks SET diagnostics=jsonb_set(diagnostics,'{checked_at}',to_jsonb(now()-interval '11 minutes')) WHERE binding_id=$1`, binding)
			}
			if tc.name == "FeishuNonTrigger" {
				v.ChatType = "group"
			}
			if !tc.allow {
				reviewExec(t, db, `UPDATE platform_channel_bindings SET access='{"mode":"allowlist","allow_ids":[],"deny_ids":[]}' WHERE id=$1`, binding)
			}
			if tc.media {
				m.ProjectRoot = t.TempDir()
				store.RuntimeRoot = m.ProjectRoot
				v.MessageType, v.Text = "file", ""
				v.Media = []manager.MediaReference{{Kind: "file", Name: "attachment.txt", Locator: "synthetic"}}
			}
			receipt, e := store.ReceiveChannel(ctx, v)
			must(e)
			noClaim := func() {
				t.Helper()
				j, e := store.ClaimChannelReaction(ctx)
				if e != nil || j != nil {
					t.Fatalf("unexpected claim: %+v %v", j, e)
				}
			}
			noClaim() // Callback ACK only persists Inbox.
			must(store.ProcessChannelInbox(ctx, m))
			if tc.media {
				noClaim() // Media waiting must not count as durable admission.
				j, e := store.ClaimMedia(ctx, m)
				must(e)
				if j == nil {
					t.Fatal("missing media job")
				}
				must(store.FinishMedia(ctx, m, j.InboxID, j.Ordinal, j.Lease, []byte("synthetic content"), false))
				reviewExec(t, db, `UPDATE manager_channel_inbox SET lease_until=NULL WHERE id=$1`, receipt.ID)
				must(store.ProcessChannelInbox(ctx, m))
			}
			if !tc.wantClaim {
				noClaim()
				return
			}
			claims := make(chan *manager.ChannelReactionClaim, 8)
			errs := make(chan error, 8)
			var wg sync.WaitGroup
			for range 8 {
				wg.Go(func() {
					j, e := store.ClaimChannelReaction(ctx)
					errs <- e
					claims <- j
				})
			}
			wg.Wait()
			close(claims)
			close(errs)
			for err := range errs {
				must(err)
			}
			var got *manager.ChannelReactionClaim
			for j := range claims {
				if j != nil {
					if got != nil {
						t.Fatal("duplicate claim")
					}
					got = j
				}
			}
			if got == nil || got.InboxID != receipt.ID {
				t.Fatal("wrong claim", got)
			}
			target, e := store.AuthorizeChannelReaction(ctx, got.InboxID, got.ClaimToken)
			must(e)
			if (target != nil) != tc.wantTarget {
				t.Fatal("misclassified authorization", target)
			}
			if target != nil && (target.MessageID != v.ProviderMessageID || target.ChatID != v.ExternalChatID || target.Provider != tc.provider || target.BindingID != binding || target.Generation != 1) {
				t.Fatal("incorrect original provider target", target)
			}
			v.ProviderEventID = "changed-transport-event"
			replay, e := store.ReceiveChannel(ctx, v)
			must(e)
			if replay.Status != "duplicate" || replay.ID != got.InboxID {
				t.Fatal("callback did not dedup by original message ID", replay)
			}
			must(store.ProcessChannelInbox(ctx, m))
			noClaim()
			if tc.wantTarget {
				reviewExec(t, db, `UPDATE platform_channel_bindings SET access='{"mode":"allowlist","allow_ids":[],"deny_ids":[]}' WHERE id=$1`, binding)
				target, e = store.AuthorizeChannelReaction(ctx, got.InboxID, got.ClaimToken)
				if e != nil || target != nil {
					t.Fatal("reaction remained authorized after revocation", target, e)
				}
			}
			if tc.provider == "dingtalk" {
				var body string
				must(db.QueryRowContext(ctx, `SELECT i.text FROM manager_channel_inbox n JOIN manager_inputs i ON i.id=n.input_id WHERE n.id=$1`, receipt.ID).Scan(&body))
				if !strings.Contains(body, v.Text) {
					t.Fatal("reaction did not preserve normal input")
				}
			}
		})
	}
}
