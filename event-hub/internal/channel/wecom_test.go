package channel

import (
	"context"
	"encoding/json"
	"github.com/gorilla/websocket"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type wecomFixture struct {
	b       Binding
	ctx     context.Context
	cancel  context.CancelFunc
	frames  chan wecomFrame
	inbound chan Inbound
	socket  *websocket.Conn
	write   sync.Mutex
	done    chan error
	server  *httptest.Server
	backend *httptest.Server
}

func (f *wecomFixture) push(t *testing.T, v any) {
	t.Helper()
	f.write.Lock()
	defer f.write.Unlock()
	if err := f.socket.WriteJSON(v); err != nil {
		t.Fatal(err)
	}
}
func awaitWecom(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("wecom condition timed out")
		}
		time.Sleep(time.Millisecond)
	}
}
func setupWecom(t *testing.T, authenticate bool, rejectInbox bool) *wecomFixture {
	t.Helper()
	return setupWecomWithReceipt(t, authenticate, rejectInbox, nil)
}

func setupWecomWithReceipt(t *testing.T, authenticate bool, rejectInbox bool, receipt func(Inbound) inboundReceipt) *wecomFixture {
	t.Helper()
	f := &wecomFixture{frames: make(chan wecomFrame, 32), inbound: make(chan Inbound, 32), done: make(chan error, 1)}
	f.ctx, f.cancel = context.WithCancel(context.Background())
	f.b = Binding{ID: wireID("test_"), Provider: "wecom", Enabled: true, Generation: 1, Revision: 1, RulesVersion: 1, TenantID: "bot", Credentials: Credentials{AppID: "bot", AppSecret: "private-secret"}}
	connected := make(chan *websocket.Conn, 1)
	f.backend = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/applied") {
			var applied Applied
			if err := json.NewDecoder(r.Body).Decode(&applied); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			if applied.Status != "connected" || applied.Generation != f.b.Generation || applied.TenantID != "" || applied.BotOpenID != "" {
				t.Error("expected a connected transport report without inbound identity")
				w.WriteHeader(409)
				return
			}
			w.WriteHeader(200)
			return
		}
		if r.URL.Path != "/api/internal/v1/channel-inbox" {
			t.Errorf("unexpected backend path %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		var v Inbound
		if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
			t.Error(err)
		}
		f.inbound <- v
		if rejectInbox {
			w.WriteHeader(503)
			return
		}
		response := inboundReceipt{ID: "11111111-1111-4111-8111-111111111111", Status: "accepted"}
		if receipt != nil {
			response = receipt(v)
			response.ID = "11111111-1111-4111-8111-111111111111"
		}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Error(err)
		}
	}))
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = c.Close() }()
		connected <- c
		for {
			var frame wecomFrame
			if c.ReadJSON(&frame) != nil {
				return
			}
			f.frames <- frame
			if authenticate && frame.Cmd == "aibot_subscribe" {
				f.write.Lock()
				err = c.WriteJSON(map[string]any{"headers": frame.Headers, "errcode": 0})
				f.write.Unlock()
				if err != nil {
					return
				}
			}
		}
	}))
	b, err := NewBackend(f.backend.URL, "internal")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		f.done <- runWecom(f.ctx, f.b, b, "ws"+strings.TrimPrefix(f.server.URL, "http"), websocket.DefaultDialer)
	}()
	f.socket = <-connected
	t.Cleanup(func() {
		f.cancel()
		select {
		case err := <-f.done:
			_ = err // Expected cancellation/transport close is the runner outcome.
		case <-time.After(3 * time.Second):
			t.Error("session did not stop")
		}
		f.server.Close()
		f.backend.Close()
	})
	if authenticate {
		awaitWecom(t, func() bool { return wecomReady(f.b) })
	}
	return f
}
func wecomIncoming(id, req, text string) map[string]any {
	return map[string]any{"cmd": "aibot_msg_callback", "headers": map[string]string{"req_id": req}, "body": map[string]any{"msgid": id, "aibotid": "bot", "from": map[string]string{"userid": "user"}, "chattype": "single", "msgtype": "text", "text": map[string]string{"content": text}}}
}
func nextWecomFrame(t *testing.T, f *wecomFixture, cmd string) wecomFrame {
	t.Helper()
	for {
		select {
		case frame := <-f.frames:
			if frame.Cmd == cmd {
				return frame
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("missing %s", cmd)
		}
	}
}
func TestWecomAuthenticatedSameSocketAndBusinessACK(t *testing.T) {
	f := setupWecom(t, true, false)
	f.push(t, wecomIncoming("message", "callback", "hello"))
	v := <-f.inbound
	if v.TenantID != "bot" || v.SenderIDType != "user_id" || !v.ProviderDirected || v.BotMentioned || v.ChatID != "user" || !v.ControlEligible {
		t.Fatal(v)
	}
	d := Delivery{BindingID: f.b.ID, Generation: 1, ChatID: "user", ReplyToMessageID: "message", Text: "reply"}
	result := make(chan deliveryReceipt, 1)
	go func() { result <- sendWeComDelivery(f.ctx, f.b, d) }()
	frame := nextWecomFrame(t, f, "aibot_respond_msg")
	if frame.Headers.ReqID != "callback" {
		t.Fatal(frame)
	}
	var body struct {
		Type   string `json:"msgtype"`
		Stream struct {
			ID      string `json:"id"`
			Content string `json:"content"`
			Finish  bool   `json:"finish"`
		} `json:"stream"`
	}
	if json.Unmarshal(frame.Body, &body) != nil || body.Type != "stream" || !body.Stream.Finish || body.Stream.Content != "reply" {
		t.Fatal(string(frame.Body))
	}
	f.push(t, map[string]any{"headers": frame.Headers, "errcode": 0})
	got := <-result
	if got.Status != "sent" || got.MessageID != "" || got.ProviderRequestID != "callback" || got.EvidenceType != "business_ack" || got.ProviderSessionID == "" {
		t.Fatal(got)
	}
	firstStream := body.Stream.ID
	d.Text = "task completed"
	go func() { result <- sendWeComDelivery(f.ctx, f.b, d) }()
	frame = nextWecomFrame(t, f, "aibot_respond_msg")
	if frame.Headers.ReqID != "callback" || json.Unmarshal(frame.Body, &body) != nil || body.Stream.ID == firstStream || body.Stream.Content != d.Text || !body.Stream.Finish {
		t.Fatal("second reply did not open its own stream", frame)
	}
	f.push(t, map[string]any{"headers": frame.Headers, "errcode": 0})
	if got = <-result; got.Status != "sent" {
		t.Fatal(got)
	}
	d.ReplyToMessageID = ""
	go func() { result <- sendWeComDelivery(f.ctx, f.b, d) }()
	frame = nextWecomFrame(t, f, "aibot_send_msg")
	if frame.Headers.ReqID == "callback" {
		t.Fatal("proactive reused callback")
	}
	f.push(t, map[string]any{"headers": frame.Headers, "errcode": 0})
	if got = <-result; got.Status != "sent" {
		t.Fatal(got)
	}
}
func TestWecomUnknownPoisonsCallbackAndLateACK(t *testing.T) {
	f := setupWecom(t, true, false)
	f.push(t, wecomIncoming("message", "callback", "hello"))
	<-f.inbound
	d := Delivery{BindingID: f.b.ID, Generation: 1, ChatID: "user", ReplyToMessageID: "message", Text: "reply"}
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	result := make(chan deliveryReceipt, 1)
	go func() { result <- sendWeComDelivery(ctx, f.b, d) }()
	frame := nextWecomFrame(t, f, "aibot_respond_msg")
	if got := sendWeComDelivery(f.ctx, f.b, d); got.Status != "failed" || got.ErrorCode != "wecom_send_busy" {
		t.Fatal(got)
	}
	cancel()
	if got := <-result; got.Status != "unknown" {
		t.Fatal(got)
	}
	f.push(t, map[string]any{"headers": frame.Headers, "errcode": 0})
	f.push(t, wecomIncoming("new-message", "callback", "hello again"))
	<-f.inbound
	d.ReplyToMessageID = "new-message"
	if got := sendWeComDelivery(f.ctx, f.b, d); got.Status != "failed" || got.ErrorCode != "wecom_reply_context_expired" {
		t.Fatal(got)
	}
	f.push(t, wecomIncoming("message", "new-callback", "hello again"))
	<-f.inbound
	d.ReplyToMessageID = "message"
	if got := sendWeComDelivery(f.ctx, f.b, d); got.ErrorCode != "wecom_reply_context_expired" {
		t.Fatal("redelivery revived an uncertain reply", got)
	}
}

func TestWecomFirstCallbackSurvivesIdentityRefresh(t *testing.T) {
	b := Binding{ID: wireID("binding_"), Provider: "wecom", Enabled: true, AppID: "bot", Generation: 1, Revision: 1, RulesVersion: 1, Credentials: Credentials{AppID: "bot", AppSecret: "fixture"}}
	var confirmed atomic.Bool
	manager := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet:
			snapshot := b
			if confirmed.Load() {
				snapshot.TenantID, snapshot.BotOpenID = "bot", "bot"
			}
			if err := json.NewEncoder(w).Encode(Snapshot{Revision: 1, Items: []Binding{snapshot}}); err != nil {
				t.Error(err)
			}
		case strings.HasSuffix(r.URL.Path, "/channel-inbox"):
			confirmed.Store(true)
			if err := json.NewEncoder(w).Encode(map[string]string{"id": "11111111-1111-4111-8111-111111111111", "status": "accepted"}); err != nil {
				t.Error(err)
			}
		}
	}))
	defer manager.Close()
	var subscriptions atomic.Int32
	incoming := make(chan struct{})
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = c.Close() }()
		for {
			var frame wecomFrame
			if c.ReadJSON(&frame) != nil {
				return
			}
			if frame.Cmd == "aibot_subscribe" {
				subscriptions.Add(1)
			} else if frame.Cmd != "aibot_respond_msg" || frame.Headers.ReqID != "first-req" {
				t.Errorf("unexpected frame %s/%s", frame.Cmd, frame.Headers.ReqID)
				return
			}
			if c.WriteJSON(map[string]any{"headers": frame.Headers, "errcode": 0}) != nil {
				return
			}
			if frame.Cmd == "aibot_subscribe" && subscriptions.Load() == 1 {
				<-incoming
				if c.WriteJSON(wecomIncoming("first", "first-req", "hello")) != nil {
					return
				}
			}
		}
	}))
	defer provider.Close()
	client, err := NewBackend(manager.URL, "internal")
	if err != nil {
		t.Fatal(err)
	}
	sup := NewSupervisor(client, "", time.Second, nil)
	sup.Connect = func(ctx context.Context, b Binding, client *Backend) error {
		return runWecom(ctx, b, client, "ws"+strings.TrimPrefix(provider.URL, "http"), websocket.DefaultDialer)
	}
	ctx := t.Context()
	defer sup.stop()
	var release sync.Once
	defer release.Do(func() { close(incoming) })
	if err = sup.fetch(ctx); err != nil {
		t.Fatal(err)
	}
	sup.reconcile(ctx)
	awaitWecom(t, func() bool { return wecomReady(b) })
	d := Delivery{BindingID: b.ID, Generation: 1, ChatID: "user", TenantID: "bot", Provider: "wecom", ReplyToMessageID: "first", Text: "reply"}
	if _, ok := deliveryBinding(ctx, sup.Current, d); ok {
		t.Fatal("subscribe ACK confirmed identity")
	}
	release.Do(func() { close(incoming) })
	select {
	case <-sup.refresh:
	case <-time.After(3 * time.Second):
		t.Fatal("no durable first callback")
	}
	for _, refresh := range []bool{false, true} {
		if refresh {
			if err = sup.fetch(ctx); err != nil {
				t.Fatal(err)
			}
			sup.reconcile(ctx)
		}
		current, ok := deliveryBinding(ctx, sup.Current, d)
		if !ok {
			t.Fatal("first reply unavailable; refreshed =", refresh)
		}
		if got := sendWeComDelivery(ctx, current, d); got.Status != "sent" {
			t.Fatal(got)
		}
	}
	if subscriptions.Load() != 1 {
		t.Fatal("identity observation restarted the socket")
	}
	sup.mu.Lock()
	sup.snapshot.Items[0].Generation++
	sup.snapshot.Items[0].Revision++
	sup.mu.Unlock()
	sup.reconcile(ctx)
	if _, ok := deliveryBinding(ctx, sup.Current, d); ok {
		t.Fatal("superseded credential generation remained usable")
	}
	awaitWecom(t, func() bool { return subscriptions.Load() == 2 })
}
func TestWecomAuthTakeoverAndDurableAdmissionFailure(t *testing.T) {
	t.Run("auth", func(t *testing.T) {
		f := setupWecom(t, false, false)
		frame := nextWecomFrame(t, f, "aibot_subscribe")
		if wecomReady(f.b) {
			t.Fatal("unauthenticated ready")
		}
		f.push(t, map[string]any{"headers": frame.Headers, "errcode": 40001})
		awaitWecom(t, func() bool { wecomSessions.Lock(); defer wecomSessions.Unlock(); return len(f.done) > 0 })
	})
	t.Run("takeover", func(t *testing.T) {
		f := setupWecom(t, true, false)
		f.push(t, map[string]any{"cmd": "aibot_event_callback", "body": map[string]any{"event": map[string]string{"eventtype": "disconnected_event"}}})
		awaitWecom(t, func() bool { return !wecomReady(f.b) })
	})
	t.Run("persistence", func(t *testing.T) {
		f := setupWecom(t, true, true)
		f.push(t, wecomIncoming("message", "callback", "hello"))
		<-f.inbound
		awaitWecom(t, func() bool { return !wecomReady(f.b) })
		for len(f.frames) > 0 {
			frame := <-f.frames
			if frame.Cmd != "aibot_subscribe" && frame.Cmd != "ping" {
				t.Fatal("fabricated inbound ACK", frame)
			}
		}
	})
}
func TestNormalizeWecomControlAndScope(t *testing.T) {
	b := Binding{Credentials: Credentials{AppID: "bot"}}
	for _, quoted := range []bool{false, true} {
		raw := wecomIncoming("message", "req", "123456")
		if quoted {
			raw["body"].(map[string]any)["quote"] = map[string]string{"content": "untrusted"}
		}
		data, err := json.Marshal(raw)
		if err != nil {
			t.Fatal(err)
		}
		var f wecomFrame
		if err = json.Unmarshal(data, &f); err != nil {
			t.Fatal(err)
		}
		v, _, err := normalizeWecom(b, f, time.Now())
		if err != nil || v.ControlEligible == quoted {
			t.Fatal(v, err)
		}
	}
	raw := wecomIncoming("message", "req", "text")
	raw["body"].(map[string]any)["aibotid"] = "foreign"
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	var frame wecomFrame
	if err = json.Unmarshal(data, &frame); err != nil {
		t.Fatal(err)
	}
	if _, _, err = normalizeWecom(b, frame, time.Now()); err == nil {
		t.Fatal("foreign bot accepted")
	}
}

func TestWecomMixedAndQuoteOverSocket(t *testing.T) {
	f := setupWecom(t, true, false)
	for _, test := range []struct {
		id, kind, text, quote string
		items                 string
	}{
		{"image-only", "mixed", "", "原文", `[{"msgtype":"image","image":{"url":"media-url","aeskey":"media-key"}}]`},
		{"mixed", "mixed", "第一行\n第二行", "原文", `[{"msgtype":"text","text":{"content":"第一行"}},{"msgtype":"image","image":{"url":"media-url","aeskey":"media-key"}},{"msgtype":"text","text":{"content":"第二行"}}]`},
		{"quoted", "text", "123456", "原文", ""},
	} {
		incoming := wecomIncoming(test.id, "req-"+test.id, test.text)
		body := incoming["body"].(map[string]any)
		body["quote"] = json.RawMessage(`{"msgtype":"text","text":{"content":"原文"},"from":{"userid":"forged-author"}}`)
		if test.kind == "mixed" {
			body["msgtype"] = "mixed"
			delete(body, "text")
			body["mixed"] = map[string]json.RawMessage{"msg_item": json.RawMessage(test.items)}
		}
		f.push(t, incoming)
		select {
		case got := <-f.inbound:
			if (test.kind == "mixed" && (len(got.Media) != 1 || strings.Contains(got.Media[0].Locator, "media-key"))) || got.MessageID != test.id || got.MessageType != test.kind || got.Text != test.text || got.QuotedText != test.quote || got.ControlEligible || got.SenderID != "user" || got.ChatType != "direct" || got.ThreadID != "" {
				t.Fatal(got)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("text-bearing callback not forwarded", test.id)
		}
	}
}

func TestWecomBeforeDispatchRevocation(t *testing.T) {
	f := setupWecom(t, true, false)
	d := Delivery{BindingID: f.b.ID, Generation: 1, ChatID: "user", Text: "must not send", BeforeDispatch: func() error { return context.Canceled }}
	got := sendWeComDelivery(f.ctx, f.b, d)
	if got.Status != "failed" || got.EvidenceType != "not_dispatched" {
		t.Fatal(got)
	}
	for len(f.frames) > 0 {
		frame := <-f.frames
		if frame.Cmd != "aibot_subscribe" {
			t.Fatal("revoked send reached transport", frame)
		}
	}
}
