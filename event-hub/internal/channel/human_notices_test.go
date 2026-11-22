package channel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	lark "github.com/larksuite/oapi-sdk-go/v3"
)

func TestHumanNoticeDispatchRequiresFreshAuthorization(t *testing.T) {
	d := Delivery{ID: "notice", LeaseToken: "lease", BindingID: "binding", Generation: 1, ConfigRevision: 2,
		Provider: "feishu", TenantID: "tenant", ChatType: "direct", RecipientID: "ou_person", Text: "question", IdempotencyKey: "key"}
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/internal/v1/human-notices/notice/authorize":
			claimed := d
			claimed.TargetDigest = "digest"
			if err := json.NewEncoder(w).Encode(struct {
				Delivery
				Allowed bool `json:"allowed"`
			}{claimed, true}); err != nil {
				t.Error(err)
			}
		case "/api/internal/v1/human-notices/notice/dispatch-check":
			w.WriteHeader(http.StatusConflict)
		case "/api/internal/v1/human-notices/notice/complete":
			var v deliveryReceipt
			if err := json.NewDecoder(r.Body).Decode(&v); err != nil || v.Status != "failed" || v.EvidenceType != "not_dispatched" || v.TargetDigest != "digest" {
				t.Error("wrong receipt of revoked send", err)
			}
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	b, err := NewBackend(server.URL, "test")
	if err != nil {
		t.Fatal(err)
	}
	b.humanNoticeDelivery = true
	lookup := func(string) (Binding, bool) {
		return Binding{Enabled: true, Provider: "feishu", TenantID: "tenant", Generation: 1, Revision: 2, Credentials: Credentials{AppID: "app", AppSecret: "secret"}}, true
	}
	send := func(_ context.Context, _ Binding, d Delivery) deliveryReceipt {
		if err := d.BeforeDispatch(); err != nil {
			return deliveryReceipt{Status: "failed", EvidenceType: "not_dispatched"}
		}
		called = true
		return deliveryReceipt{Status: "sent"}
	}
	if err = deliver(context.Background(), b, lookup, send, d); err != nil || called {
		t.Fatal("revoked notice reached provider", err)
	}
}

func TestFeishuHumanNoticeUsesOpenIDAndRequiresPrivateReceipt(t *testing.T) {
	for _, tc := range []struct{ chat, status string }{{"oc_private", "sent"}, {"", "unknown"}} {
		t.Run(tc.status, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				var resp any
				if r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal" {
					resp = map[string]any{"code": 0, "tenant_access_token": "fixture-token", "expire": 7200}
				} else {
					var body map[string]any
					err := json.NewDecoder(r.Body).Decode(&body)
					if r.URL.Path != "/open-apis/im/v1/messages" || r.URL.Query().Get("receive_id_type") != "open_id" || body["receive_id"] != "ou_target" || body["uuid"] != "stable" || err != nil {
						t.Errorf("unexpected proactive send: %s %v", r.URL.Path, err)
					}
					resp = map[string]any{"code": 0, "data": map[string]string{"message_id": "om_sent", "chat_id": tc.chat}}
				}
				if err := json.NewEncoder(w).Encode(resp); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			api := lark.NewClient("app", "secret", lark.WithOpenBaseUrl(server.URL), lark.WithEnableTokenCache(false), lark.WithLogger(quietLogger{}))
			got := sendFeishuWithClient(context.Background(), api, Delivery{ChatType: "direct", RecipientID: "ou_target", Text: "confirmation", IdempotencyKey: "stable", ConfigRevision: 2})
			if got.Status != tc.status || got.ProviderChatID != tc.chat || tc.status == "sent" && got.MessageID != "om_sent" {
				t.Fatal(got)
			}
		})
	}
}
