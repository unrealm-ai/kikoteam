package channel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// Text-bearing messages use the official aibot SDK wire contract.
// No independent sender connection, fabricated inbound ACK, or automatic write retry.
const wecomWebSocketURL = "wss://openws.work.weixin.qq.com"

type wecomFrame struct {
	ReceivedAt time.Time `json:"-"`
	Cmd        string    `json:"cmd,omitempty"`
	Headers    struct {
		ReqID string `json:"req_id"`
	} `json:"headers"`
	Body      json.RawMessage `json:"body,omitempty"`
	ErrorCode *int            `json:"errcode,omitempty"`
}
type wecomReply struct{ RequestID, ChatID, Kind string }
type wecomSession struct {
	identityConfirmed atomic.Bool
	traceBackend      *Backend
	id                string
	binding           Binding
	conn              *websocket.Conn
	mu                sync.Mutex
	writeMu           sync.Mutex
	closed            bool
	done              chan struct{}
	replies           map[string]wecomReply
	replyOrder        []string
	poisoned          map[string]bool
	poisonedMessages  map[string]bool
	pending           map[string]chan wecomFrame
	ping              string
	missed            int
	timeout           time.Duration
}

var wecomSessions = struct {
	sync.Mutex
	items map[string]*wecomSession
}{items: map[string]*wecomSession{}}

func wireID(prefix string) string {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		panic("random source unavailable")
	}
	return prefix + hex.EncodeToString(data[:])
}
func validWecomID(s string) bool {
	return len(s) > 0 && len(s) <= 256 && !strings.ContainsAny(s, "\x00\r\n\t ")
}

type wecomTextContent struct {
	Content string `json:"content"`
}

func wecomMixedText(raw json.RawMessage) (string, error) {
	var mixed struct {
		Items []struct {
			Type string           `json:"msgtype"`
			Text wecomTextContent `json:"text"`
		} `json:"msg_item"`
	}
	if json.Unmarshal(raw, &mixed) != nil || len(mixed.Items) > 256 {
		return "", errors.New("wecom_invalid_mixed_message")
	}
	parts := make([]string, 0, len(mixed.Items))
	for _, item := range mixed.Items {
		if item.Type == "text" && item.Text.Content != "" {
			parts = append(parts, item.Text.Content)
		}
	}
	return strings.Join(parts, "\n"), nil
}

func wecomQuotedText(raw json.RawMessage, textLen int) string {
	var quote struct {
		Type  string           `json:"msgtype"`
		Text  wecomTextContent `json:"text"`
		Voice wecomTextContent `json:"voice"`
	}
	if json.Unmarshal(raw, &quote) != nil {
		return ""
	}
	var text string
	switch quote.Type {
	case "text":
		text = quote.Text.Content
	case "voice":
		text = quote.Voice.Content
	}
	if strings.ContainsRune(text, 0) || textLen+len(text) > 65536 {
		return ""
	}
	return strings.TrimSpace(text)
}

func normalizeWecom(b Binding, f wecomFrame, now time.Time) (Inbound, wecomReply, error) {
	v := Inbound{LogProvider: "wecom", LogCallbackID: f.Headers.ReqID, BindingID: b.ID, Generation: b.Generation, Revision: b.Revision, RulesVersion: b.RulesVersion, SenderIDType: "user_id", TenantID: b.Credentials.AppID, BotID: b.Credentials.AppID, ProviderDirected: true, CreatedAtMS: now.UnixMilli(), Mentions: []Mention{}}
	var body struct {
		MessageID string `json:"msgid"`
		BotID     string `json:"aibotid"`
		From      struct {
			UserID string `json:"userid"`
			CorpID any    `json:"corpid"`
		} `json:"from"`
		ChatType string           `json:"chattype"`
		ChatID   string           `json:"chatid"`
		Type     string           `json:"msgtype"`
		Text     wecomTextContent `json:"text"`
		Mixed    json.RawMessage  `json:"mixed"`
		Quote    json.RawMessage  `json:"quote"`
	}
	if f.Cmd != "aibot_msg_callback" || !validWecomID(f.Headers.ReqID) || json.Unmarshal(f.Body, &body) != nil || body.BotID != b.Credentials.AppID || !validWecomID(body.MessageID) || !validWecomID(body.From.UserID) || body.From.UserID == body.BotID {
		return v, wecomReply{}, errors.New("wecom_invalid_identity")
	}
	if body.From.CorpID != nil {
		corp, ok := body.From.CorpID.(string)
		if !ok || !validWecomID(corp) {
			return v, wecomReply{}, errors.New("wecom_invalid_corporate_metadata")
		}
	}
	v.EventID = body.MessageID
	v.MessageID = body.MessageID
	v.SenderID = body.From.UserID
	v.MessageType = body.Type
	v.Media = wecomMedia(body.Type, f.Body)
	switch body.Type {
	case "text":
		v.Text = body.Text.Content
	case "mixed":
		var err error
		v.Text, err = wecomMixedText(body.Mixed)
		if err != nil {
			return v, wecomReply{}, err
		}
	case "image", "file":
	default:
		return v, wecomReply{}, errors.New("wecom_unsupported_message")
	}
	switch body.ChatType {
	case "single":
		v.ChatType = "direct"
		v.ChatID = body.From.UserID
	case "group":
		v.ChatType = "group"
		v.ChatID = body.ChatID
	default:
		return v, wecomReply{}, errors.New("wecom_invalid_chat_type")
	}
	if !validWecomID(v.ChatID) || strings.TrimSpace(v.Text) == "" && len(v.Media) == 0 || len(v.Text) > 65536 || strings.ContainsRune(v.Text, 0) {
		return v, wecomReply{}, errors.New("wecom_unsupported_message")
	}
	v.QuotedText = wecomQuotedText(body.Quote, len(v.Text))
	var original map[string]json.RawMessage
	if v.ChatType == "direct" && v.MessageType == "text" && json.Unmarshal(f.Body, &original) == nil {
		v.ControlEligible = true
		for _, field := range []string{"quote", "mixed", "image", "file", "voice", "video"} {
			if _, exists := original[field]; exists {
				v.ControlEligible = false
			}
		}
	}
	return v, wecomReply{RequestID: f.Headers.ReqID, ChatID: v.ChatID, Kind: v.ChatType}, nil
}
func newWecomSession(b Binding, c *websocket.Conn) *wecomSession {
	return &wecomSession{id: wireID("session_"), binding: b, conn: c, done: make(chan struct{}), replies: map[string]wecomReply{}, poisoned: map[string]bool{}, poisonedMessages: map[string]bool{}, pending: map[string]chan wecomFrame{}, timeout: 10 * time.Second}
}
func (s *wecomSession) close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	close(s.done)
	s.replies = map[string]wecomReply{}
	s.mu.Unlock()
	_ = s.conn.Close() //nolint:errcheck // Shutdown invalidates the session even if the peer already closed.
}

var (
	errWecomNotDispatched = errors.New("wecom_not_dispatched")
	errWecomConnect       = errors.New("wecom_connection_failed")
	errWecomAuth          = errors.New("wecom_authentication_failed")
	errWecomStatusUpdate  = errors.New("wecom_status_update_failed")
)

func (s *wecomSession) write(ctx context.Context, cmd, id string, body any) error {
	return s.writeGuard(ctx, cmd, id, body, nil)
}
func (s *wecomSession) writeGuard(ctx context.Context, cmd, id string, body any, guard func() error) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed || ctx.Err() != nil {
		return errWecomNotDispatched
	}
	if guard != nil {
		if err := guard(); err != nil {
			return errWecomNotDispatched
		}
	}
	deadline := time.Now().Add(s.timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := s.conn.SetWriteDeadline(deadline); err != nil {
		return err
	}
	return s.conn.WriteJSON(map[string]any{"cmd": cmd, "headers": map[string]string{"req_id": id}, "body": body})
}
func registerWecom(s *wecomSession) error {
	wecomSessions.Lock()
	defer wecomSessions.Unlock()
	for _, old := range wecomSessions.items {
		if old.binding.Credentials.AppID == s.binding.Credentials.AppID {
			return errors.New("wecom_duplicate_live_bot")
		}
	}
	wecomSessions.items[s.binding.ID] = s
	return nil
}
func unregisterWecom(s *wecomSession) {
	wecomSessions.Lock()
	defer wecomSessions.Unlock()
	if wecomSessions.items[s.binding.ID] == s {
		delete(wecomSessions.items, s.binding.ID)
	}
}
func WeCom(ctx context.Context, b Binding, backend *Backend) error {
	return runWecom(ctx, b, backend, wecomWebSocketURL, websocket.DefaultDialer)
}
func runWecom(ctx context.Context, b Binding, backend *Backend, address string, dialer *websocket.Dialer) error {
	if !validWecomID(b.Credentials.AppID) || b.Credentials.AppSecret == "" || (b.TenantID != "" && b.TenantID != b.Credentials.AppID) || (b.BotOpenID != "" && b.BotOpenID != b.Credentials.AppID) {
		return errors.New("wecom_invalid_binding")
	}
	c, resp, err := dialer.DialContext(ctx, address, http.Header{})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close() //nolint:errcheck // No response payload is consumed.
	}
	if err != nil {
		return errWecomConnect
	}
	s := newWecomSession(b, c)
	s.traceBackend = backend
	defer s.close()
	c.SetReadLimit(1 << 20)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			s.close()
		case <-stop:
		}
	}()
	authID := wireID("auth_")
	if err = s.write(ctx, "aibot_subscribe", authID, map[string]string{"bot_id": b.Credentials.AppID, "secret": b.Credentials.AppSecret}); err != nil {
		return errWecomAuth
	}
	if err = c.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return err
	}
	var ack wecomFrame
	if c.ReadJSON(&ack) != nil || ack.Cmd != "" || ack.Headers.ReqID != authID || ack.ErrorCode == nil || *ack.ErrorCode != 0 {
		return errWecomAuth
	}
	if err = c.SetReadDeadline(time.Time{}); err != nil {
		return err
	}
	if err = registerWecom(s); err != nil {
		return err
	}
	defer unregisterWecom(s)
	// The subscribe ACK authenticates the transport. The first trusted inbound
	// event establishes identity separately, as for DingTalk.
	if err = backend.Applied(ctx, b.ID, status(b, "connected", "")); err != nil {
		return errWecomStatusUpdate
	}
	events := make(chan wecomFrame, 32)
	workers := sync.WaitGroup{}
	workers.Add(2)
	go func() { defer workers.Done(); s.ingest(ctx, backend, events) }()
	go func() { defer workers.Done(); s.heartbeat(ctx) }()
	err = s.read(ctx, events)
	s.close()
	close(events)
	workers.Wait()
	return err
}
func (s *wecomSession) read(ctx context.Context, events chan<- wecomFrame) error {
	for {
		var f wecomFrame
		if s.conn.ReadJSON(&f) != nil {
			return errors.New("wecom_transport_closed")
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if f.Cmd == "aibot_event_callback" {
			var b struct {
				Event struct {
					Type string `json:"eventtype"`
				} `json:"event"`
			}
			if json.Unmarshal(f.Body, &b) == nil && b.Event.Type == "disconnected_event" {
				return errors.New("wecom_connection_taken_over")
			}
			continue
		}
		if f.Cmd == "aibot_msg_callback" {
			s.traceBackend.traceInbound(ctx, "channel.callback.received", "wecom", Inbound{BindingID: s.binding.ID, Generation: s.binding.Generation, LogCallbackID: f.Headers.ReqID}, "received")
			f.ReceivedAt = time.Now()
			select {
			case events <- f:
			default:
				s.traceBackend.traceInbound(ctx, "channel.callback.result", "wecom", Inbound{BindingID: s.binding.ID, Generation: s.binding.Generation, LogCallbackID: f.Headers.ReqID}, "queue_full")
				return errors.New("wecom_ingress_backpressure")
			}
			continue
		}
		if f.Cmd != "" {
			continue
		}
		s.mu.Lock()
		if f.Headers.ReqID == s.ping && f.ErrorCode != nil && *f.ErrorCode == 0 {
			s.missed = 0
		}
		pending := s.pending[f.Headers.ReqID]
		if pending != nil {
			select {
			case pending <- f:
			default:
			}
		}
		s.mu.Unlock()
	}
}
func (s *wecomSession) heartbeat(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.done:
			return
		case <-ticker.C:
		}
		s.mu.Lock()
		if s.missed >= 3 {
			s.mu.Unlock()
			s.close()
			return
		}
		s.missed++
		s.ping = wireID("ping_")
		id := s.ping
		s.mu.Unlock()
		if s.write(ctx, "ping", id, nil) != nil {
			s.close()
			return
		}
	}
}
func (s *wecomSession) ingest(ctx context.Context, backend *Backend, events <-chan wecomFrame) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.done:
			return
		case f, ok := <-events:
			if !ok {
				return
			}
			v, reply, err := normalizeWecom(s.binding, f, f.ReceivedAt)
			if err != nil {
				backend.traceInbound(ctx, "channel.callback.result", "wecom", Inbound{BindingID: s.binding.ID, Generation: s.binding.Generation, LogCallbackID: f.Headers.ReqID}, "invalid_or_ignored")
				continue
			}
			s.mu.Lock()
			prior, exists := s.replies[v.MessageID]
			if exists && (prior.ChatID != reply.ChatID || prior.Kind != reply.Kind) {
				s.mu.Unlock()
				backend.traceInbound(ctx, "channel.callback.result", "wecom", v, "reply_scope_conflict")
				continue
			}
			if !exists && !s.poisoned[reply.RequestID] && !s.poisonedMessages[v.MessageID] {
				if len(s.replyOrder) >= 1024 {
					delete(s.replies, s.replyOrder[0])
					s.replyOrder = s.replyOrder[1:]
				}
				s.replies[v.MessageID] = reply
				s.replyOrder = append(s.replyOrder, v.MessageID)
			}
			s.mu.Unlock()
			receipt, err := backend.receiveInbound(ctx, v)
			if err != nil {
				backend.traceInbound(ctx, "channel.callback.result", "wecom", v, "forward_failed")
				s.close()
				return
			}
			if s.binding.TenantID == "" && !s.identityConfirmed.Swap(true) && backend.OnObserved != nil {
				backend.OnObserved()
			}
			backend.traceInbound(ctx, "channel.callback.result", "wecom", v, "durably_received")
			s.replyToControl(ctx, backend, v, reply, receipt)
		}
	}
}
func currentWecomSession(b Binding) *wecomSession {
	wecomSessions.Lock()
	defer wecomSessions.Unlock()
	s := wecomSessions.items[b.ID]
	if s == nil || s.binding.Generation != b.Generation || s.binding.Revision != b.Revision || s.binding.Credentials != b.Credentials {
		return nil
	}
	return s
}
func sendWeComDelivery(ctx context.Context, b Binding, d Delivery) deliveryReceipt {
	s := currentWecomSession(b)
	if s == nil {
		return deliveryReceipt{Status: "failed", EvidenceType: "not_dispatched", ErrorCode: "wecom_session_unavailable"}
	}
	return s.send(ctx, d)
}
func (s *wecomSession) send(ctx context.Context, d Delivery) deliveryReceipt {
	return s.sendBound(ctx, d, "")
}

func (s *wecomSession) sendBound(ctx context.Context, d Delivery, requiredCallback string) deliveryReceipt {
	if d.BindingID != s.binding.ID || d.Generation != s.binding.Generation || !validWecomID(d.ChatID) || d.ThreadID != "" || d.Text == "" && d.Media == nil || len(d.Text) > 16384 {
		return deliveryReceipt{Status: "failed", EvidenceType: "not_dispatched", ErrorCode: "wecom_invalid_send"}
	}
	var mediaID string
	if d.Media != nil {
		var e error
		mediaID, e = s.uploadMedia(ctx, d)
		if e != nil {
			return mediaUploadFailure()
		}
	}
	id := wireID("send_")
	cmd := "aibot_send_msg"
	body := map[string]any{"chatid": d.ChatID, "msgtype": "markdown", "markdown": map[string]string{"content": d.Text}}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return deliveryReceipt{Status: "failed", EvidenceType: "not_dispatched", ErrorCode: "wecom_session_unavailable"}
	}
	if d.ReplyToMessageID != "" {
		reply, ok := s.replies[d.ReplyToMessageID]
		if !ok || !s.replyMatches(d, reply, requiredCallback) {
			s.mu.Unlock()
			return deliveryReceipt{Status: "failed", EvidenceType: "not_dispatched", ErrorCode: "wecom_reply_context_expired"}
		}
		id = reply.RequestID
		cmd = "aibot_respond_msg"
		body = map[string]any{"msgtype": "stream", "stream": map[string]any{"id": wireID("stream_"), "finish": true, "content": d.Text}}
	}
	if mediaID != "" {
		body = map[string]any{"msgtype": d.Format, d.Format: map[string]string{"media_id": mediaID}}
		if cmd == "aibot_send_msg" {
			body["chatid"] = d.ChatID
		}
	}
	if s.pending[id] != nil || len(s.pending) >= 128 {
		s.mu.Unlock()
		return deliveryReceipt{Status: "failed", EvidenceType: "not_dispatched", ErrorCode: "wecom_send_busy"}
	}
	result := make(chan wecomFrame, 1)
	s.pending[id] = result
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.pending, id); s.mu.Unlock() }()
	if err := s.writeGuard(ctx, cmd, id, body, d.BeforeDispatch); err != nil {
		if errors.Is(err, errWecomNotDispatched) {
			return deliveryReceipt{Status: "failed", EvidenceType: "not_dispatched", ErrorCode: "wecom_dispatch_revoked"}
		}
		s.poisonReply(d.ReplyToMessageID, id)
		return deliveryReceipt{Status: "unknown", ErrorCode: "wecom_send_outcome_unknown"}
	}
	timer := time.NewTimer(s.timeout)
	defer timer.Stop()
	select {
	case ack := <-result:
		s.mu.Lock()
		closed := s.closed
		s.mu.Unlock()
		if !closed && ack.Headers.ReqID == id && ack.ErrorCode != nil && *ack.ErrorCode == 0 {
			return deliveryReceipt{Status: "sent", ProviderRequestID: id, EvidenceType: "business_ack", EvidenceID: id, ProviderSessionID: s.id}
		}
	case <-ctx.Done():
	case <-s.done:
	case <-timer.C:
	}
	s.poisonReply(d.ReplyToMessageID, id)
	return deliveryReceipt{Status: "unknown", ErrorCode: "wecom_send_outcome_unknown"}
}

// Caller holds s.mu. A control response must use this exact callback, not a
// reply context remembered for a previous delivery of the same message.
func (s *wecomSession) replyMatches(d Delivery, reply wecomReply, requiredCallback string) bool {
	return reply.ChatID == d.ChatID && (d.ChatType == "" || reply.Kind == d.ChatType) &&
		(requiredCallback == "" || requiredCallback == reply.RequestID) && !s.poisoned[reply.RequestID]
}

func (s *wecomSession) poisonReply(messageID, id string) {
	if messageID == "" {
		return
	}
	s.mu.Lock()
	s.poisonedMessages[messageID] = true
	s.poisoned[id] = true
	for message, r := range s.replies {
		if r.RequestID == id {
			s.poisonedMessages[message] = true
			delete(s.replies, message)
		}
	}
	full := len(s.poisoned) >= 1024 || len(s.poisonedMessages) >= 1024
	s.mu.Unlock()
	if full {
		s.close()
	}
}

func wecomReady(b Binding) bool {
	s := currentWecomSession(b)
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.closed
}
