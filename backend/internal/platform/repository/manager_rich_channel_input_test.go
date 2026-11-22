package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	managercontroller "github.com/unrealm-ai/kikoteam/backend/internal/manager/controller"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func TestManagerRichChannelInputUsesNormalPermissionPath(t *testing.T) {
	for _, test := range []struct {
		provider, kind, wrongKind string
		quoted                    string
	}{
		{"dingtalk", "richText", "mixed", ""},
		{"wecom", "mixed", "richText", "被引用的正文"},
	} {
		t.Run(test.provider, func(t *testing.T) {
			db, m, s, _, _, _, binding := channelManagerFixture(t)
			ctx := context.Background()
			if _, err := db.Exec(`UPDATE platform_channel_bindings SET provider=$2,app_id='bot',bot_open_id='bot',external_tenant_id='bot',history=jsonb_set(history,'{enabled}',to_jsonb($3::boolean)) WHERE id=$1`, binding, test.provider, test.provider == "wecom"); err != nil {
				t.Fatal(err)
			}
			token := strings.Repeat("h", 40)
			mux := http.NewServeMux()
			(&managercontroller.ChannelServer{Store: s, Manager: m, Token: token}).Routes(mux)
			post := func(v manager.ChannelInbound, code int) {
				t.Helper()
				raw, err := json.Marshal(v)
				if err != nil {
					t.Fatal(err)
				}
				r := httptest.NewRequest(http.MethodPost, "/api/internal/v1/channel-inbox", bytes.NewReader(raw))
				r.Header.Set("Authorization", "Bearer "+token)
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, r)
				if w.Code != code {
					t.Fatalf("got %d, want %d: %s", w.Code, code, w.Body.String())
				}
			}
			v := inboundFixture(binding, "rich-accepted", "callback-user")
			v.BotID = "bot"
			v.SenderIDType = "user_id"
			v.ExternalTenantID = "bot"
			v.SenderTenant = "bot"
			v.ChatType = "group"
			v.Text = "第一段\n第二段"
			v.QuotedText = test.quoted
			v.CreatedAtMS = 100000
			v.MessageType = test.wrongKind
			v.BotMentioned = true
			post(v, http.StatusConflict)
			v.MessageType = test.kind
			v.BotMentioned = false
			post(v, http.StatusConflict)
			v.BotMentioned = test.provider == "dingtalk"
			v.ProviderDirected = test.provider == "wecom"
			post(v, http.StatusOK)
			post(v, http.StatusOK)
			if err := s.ProcessChannelInbox(ctx, m); err != nil {
				t.Fatal(err)
			}
			var text, raw, kind, reason string
			if err := db.QueryRow(`SELECT i.text,n.text,n.message_type,j.snapshot->>'error_code' FROM manager_channel_inbox n JOIN manager_inputs i ON n.input_id=i.id JOIN manager_channel_history_jobs j ON j.inbox_id=n.id WHERE n.provider_event_id=$1`, v.ProviderEventID).Scan(&text, &raw, &kind, &reason); err != nil {
				t.Fatal(err)
			}
			want := "[说话人=成员1]" + v.Text
			wantReason := "history_disabled"
			if test.provider == "wecom" {
				want += "\n\n[用户引用的内容，原始身份未核验]\n" + test.quoted
				wantReason = "wecom_mixed_history_anchor_unavailable"
			}
			if text != want || raw != v.Text || kind != test.kind || reason != wantReason {
				t.Fatal(text, raw, kind, reason)
			}
			if _, err := db.Exec(`UPDATE platform_channel_bindings SET access='{"mode":"allowlist","allow_ids":[],"deny_ids":[]}' WHERE id=$1`, binding); err != nil {
				t.Fatal(err)
			}
			v.ProviderEventID, v.ProviderMessageID = "rich-denied", "rich-denied"
			post(v, http.StatusOK)
			if err := s.ProcessChannelInbox(ctx, m); err != nil {
				t.Fatal(err)
			}
			var inputs int
			if err := db.QueryRow(`SELECT count(*) FROM manager_inputs`).Scan(&inputs); err != nil || inputs != 1 {
				t.Fatal(inputs, err)
			}
		})
	}
}
