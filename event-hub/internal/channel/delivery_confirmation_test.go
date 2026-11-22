package channel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDeliveryRetriesReceiptNotSend(t *testing.T) {
	d := Delivery{ID: "delivery", LeaseToken: "original-attempt", BindingID: "binding", Generation: 1, ChatID: "chat", Text: "text", IdempotencyKey: "operation"}
	sends, receipts := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "authorize") {
			v := d
			v.TargetDigest = "immutable-target"
			if e := json.NewEncoder(w).Encode(struct {
				Delivery
				Allowed bool `json:"allowed"`
			}{v, true}); e != nil {
				t.Error(e)
			}
			return
		}
		receipts++
		var got deliveryReceipt
		if e := json.NewDecoder(r.Body).Decode(&got); e != nil {
			t.Error(e)
		}
		if got.LeaseToken != d.LeaseToken || got.TargetDigest != "immutable-target" || got.MessageID != "actual-message" {
			t.Error(got)
		}
		if receipts < 3 {
			w.WriteHeader(503)
		} else {
			w.WriteHeader(204)
		}
	}))
	defer server.Close()
	backend, e := NewBackend(server.URL, "internal")
	if e != nil {
		t.Fatal(e)
	}
	lookup := func(string) (Binding, bool) {
		return Binding{Provider: "feishu", Enabled: true, Generation: 1, Credentials: Credentials{AppID: "app", AppSecret: "secret"}}, true
	}
	send := func(context.Context, Binding, Delivery) deliveryReceipt {
		sends++
		return deliveryReceipt{Status: "sent", MessageID: "actual-message", EvidenceType: "send_response", EvidenceID: "actual-message"}
	}
	if e = deliver(context.Background(), backend, lookup, send, d); e != nil || sends != 1 || receipts != 3 {
		t.Fatal(e, sends, receipts)
	}
}
func TestDeliveryQueryNeverAuthorizesOrSends(t *testing.T) {
	d := Delivery{ID: "delivery", LeaseToken: "original-attempt", TargetDigest: "immutable-target", BindingID: "binding", Generation: 1, Provider: "dingtalk", TenantID: "tenant", ProviderRequestID: "query-key", QueryToken: "query-attempt"}
	queried, completed := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/internal/v1/channel-delivery-queries/claim" {
			if e := json.NewEncoder(w).Encode(map[string]any{"items": []Delivery{d}}); e != nil {
				t.Error(e)
			}
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/complete") {
			t.Error("query attempted send authorization", r.URL)
			w.WriteHeader(404)
			return
		}
		completed++
		var got deliveryReceipt
		if e := json.NewDecoder(r.Body).Decode(&got); e != nil {
			t.Error(e)
		}
		if got.QueryToken != d.QueryToken || got.LeaseToken != d.LeaseToken || got.Status != "sent" {
			t.Error(got)
		}
		w.WriteHeader(204)
	}))
	defer server.Close()
	backend, e := NewBackend(server.URL, "internal")
	if e != nil {
		t.Fatal(e)
	}
	// Current send authorization is off; a known status fact can still be read.
	lookup := func(string) (Binding, bool) {
		return Binding{Provider: "dingtalk", Enabled: false, Generation: 1, TenantID: "tenant", Credentials: Credentials{AppID: "app", AppSecret: "secret"}}, true
	}
	query := func(context.Context, Binding, Delivery) deliveryReceipt {
		queried++
		return deliveryReceipt{Status: "sent", ProviderRequestID: "query-key", EvidenceType: "status_query", EvidenceID: "query-key"}
	}
	if e = reconcileDeliveryQueries(context.Background(), backend, lookup, query); e != nil || queried != 1 || completed != 1 {
		t.Fatal(e, queried, completed)
	}
}
