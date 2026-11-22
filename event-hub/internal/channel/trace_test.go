package channel

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPipelineLogsDurableAdmissionWithoutPayloads(t *testing.T) {
	var logs bytes.Buffer
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"id":"11111111-1111-1111-1111-111111111111","status":"accepted"}`)); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	b, err := NewBackend(server.URL, "SECRET_TOKEN")
	if err != nil {
		t.Fatal(err)
	}
	b.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
	v := Inbound{BindingID: "binding", EventID: "event", MessageID: "message", SenderID: "PRIVATE_PERSON", Text: "SECRET_CONTENT"}
	if err = b.Inbound(context.Background(), v); err != nil {
		t.Fatal(err)
	}
	out := logs.String()
	for _, want := range []string{"channel.inbound.forward", "channel.inbound.durable_receipt", "11111111-1111-1111-1111-111111111111", "accepted"} {
		if !strings.Contains(out, want) {
			t.Fatal(out)
		}
	}
	for _, secret := range []string{"SECRET_TOKEN", "SECRET_CONTENT", "PRIVATE_PERSON"} {
		if strings.Contains(out, secret) {
			t.Fatal("sensitive field logged")
		}
	}
	logs.Reset()
	b.traceDelivery(context.Background(), "channel.delivery.send", Delivery{ID: "notice", LeaseToken: "LEASE_SECRET", Text: "BODY_SECRET", TargetDigest: "DIGEST_SECRET"}, "started")
	for _, secret := range []string{"LEASE_SECRET", "BODY_SECRET", "DIGEST_SECRET"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatal("secret logged")
		}
	}
	b.notificationDelivery = true
	b.traceDelivery(context.Background(), "channel.delivery.query", Delivery{ID: "notice"}, "started")
	if !strings.Contains(logs.String(), `"delivery_kind":"notification"`) {
		t.Fatal(logs.String())
	}
}
func TestTraceIdentifierBounds(t *testing.T) {
	for _, v := range []string{"injected\nline", strings.Repeat("a", 257)} {
		if traceID(v) != "invalid_identifier" {
			t.Fatal("unsafe wire id logged")
		}
	}
}

func TestEmptyDeliveryQueryPollDoesNotLog(t *testing.T) {
	var logs bytes.Buffer
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write([]byte(`{"items":[]}`)); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	b, err := NewBackend(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	b.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
	err = reconcileDeliveryQueries(context.Background(), b, func(string) (Binding, bool) { t.Fatal("empty poll lookup"); return Binding{}, false }, nil)
	if err != nil || logs.Len() != 0 {
		t.Fatal(err, logs.String())
	}
}

func TestInboundTraceDistinguishesProviderTimeFromWeComCallback(t *testing.T) {
	var logs bytes.Buffer
	b := &Backend{Logger: slog.New(slog.NewJSONHandler(&logs, nil))}
	b.traceInbound(context.Background(), "channel.callback.received", "feishu", Inbound{CreatedAtMS: 123}, "received")
	if !strings.Contains(logs.String(), `"provider_created_at_ms":123`) {
		t.Fatal(logs.String())
	}
	logs.Reset()
	b.traceInbound(context.Background(), "channel.inbound.forward", "wecom", Inbound{CreatedAtMS: 123, LogCallbackID: "wire-request", MessageID: "provider-message"}, "started")
	line := logs.String()
	if strings.Contains(line, "provider_created_at_ms") || !strings.Contains(line, `"callback_id":"wire-request"`) || !strings.Contains(line, `"provider_message_id":"provider-message"`) {
		t.Fatal(line)
	}
}
