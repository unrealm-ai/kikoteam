package channel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNotificationDeliveryUsesIndependentLedgerAndEvidence(t *testing.T) {
	d := Delivery{ID: "notice", LeaseToken: "lease", BindingID: "binding", Generation: 1, Provider: "dingtalk", TenantID: "tenant", ChatID: "group", ChatType: "group", Text: "final", IdempotencyKey: "notice-key"}
	completed := 0
	sent := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test" {
			t.Error("missing auth")
		}
		switch r.URL.Path {
		case "/api/internal/v1/trigger-notifications/notice/authorize":
			v := d
			v.TargetDigest = "digest"
			if err := json.NewEncoder(w).Encode(struct {
				Delivery
				Allowed bool `json:"allowed"`
			}{v, true}); err != nil {
				t.Error(err)
			}
		case "/api/internal/v1/trigger-notifications/notice/complete":
			var receipt deliveryReceipt
			if err := json.NewDecoder(r.Body).Decode(&receipt); err != nil {
				t.Error(err)
			}
			if receipt.Status != "unknown" || receipt.TargetDigest != "digest" || receipt.LeaseToken != "lease" {
				t.Error("receipt altered", receipt)
			}
			completed++
			w.WriteHeader(204)
		default:
			t.Error("used wrong ledger path", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	b, err := NewBackend(server.URL, "test")
	if err != nil {
		t.Fatal(err)
	}
	b.notificationDelivery = true
	lookup := func(string) (Binding, bool) {
		return Binding{Enabled: true, Provider: "dingtalk", Generation: 1, TenantID: "tenant", Credentials: Credentials{AppID: "app", AppSecret: "secret"}}, true
	}
	send := func(context.Context, Binding, Delivery) deliveryReceipt {
		sent++
		return deliveryReceipt{Status: "unknown"}
	}
	if err = deliver(context.Background(), b, lookup, send, d); err != nil || sent != 1 || completed != 1 {
		t.Fatal(sent, completed, err)
	}
	if b.deliveryQueryPath() != "/api/internal/v1/trigger-notification-queries" {
		t.Fatal("query used inbound ledger")
	}
}

func TestFeishuReceiptAnchorOnlyExactTarget(t *testing.T) {
	d := Delivery{ChatID: "oc_group", ThreadID: "omt_topic"}
	for _, c := range []struct {
		chat, thread, at, status string
		wantAnchor               bool
	}{
		{"oc_group", "omt_topic", "123", "sent", true},
		{"other", "omt_topic", "123", "unknown", false},
		{"oc_group", "other", "123", "unknown", false},
		{"oc_group", "omt_topic", "0", "sent", false},
	} {
		r := feishuSentReceipt(d, "om_sent", c.at, c.chat, c.thread)
		if r.Status != c.status || r.MessageID != "om_sent" || r.ProviderChatID != c.chat || r.ProviderThreadID != c.thread {
			t.Fatal(r)
		}
		if (r.ProviderCreatedAtMS > 0) != c.wantAnchor {
			t.Fatal("fabricated anchor", r)
		}
	}
}
