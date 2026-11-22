package channel

// Stream protocol follows the locally audited official dingtalk-stream 2.1.5.
// Only robot CALLBACK is subscribed; no EVENT auto-ack or fabricated history.
import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const dingRobotTopic = "/v1.0/im/bot/messages/get"

type dingFrame struct {
	Type    string            `json:"type"`
	Headers map[string]string `json:"headers"`
	Data    string            `json:"data"`
}
type dingMessage struct {
	SenderName  string `json:"senderNick"`
	ChatName    string `json:"conversationTitle"`
	AppID       string `json:"appId"`
	AppKey      string `json:"appKey"`
	Corp        string `json:"chatbotCorpId"`
	BotAlias    string `json:"chatbotUserId"`
	Robot       string `json:"robotCode"`
	SenderCorp  string `json:"senderCorpId"`
	Staff       string `json:"senderStaffId"`
	SenderAlias string `json:"senderId"`
	MessageID   string `json:"msgId"`
	ChatID      string `json:"conversationId"`
	Kind        string `json:"conversationType"`
	Type        string `json:"msgtype"`
	Mentioned   bool   `json:"isInAtList"`
	Created     int64  `json:"createAt"`
	Text        struct {
		Content string `json:"content"`
	} `json:"text"`
	Content json.RawMessage `json:"content"`
}

func dingRichText(raw json.RawMessage) (string, error) {
	var content struct {
		Items []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"richText"`
	}
	if json.Unmarshal(raw, &content) != nil || len(content.Items) > 256 {
		return "", errors.New("invalid dingtalk rich text")
	}
	parts := make([]string, 0, len(content.Items))
	for _, item := range content.Items {
		if (item.Type == "" || item.Type == "text") && item.Text != "" {
			parts = append(parts, item.Text)
		}
	}
	return strings.Join(parts, "\n"), nil
}

func dingID(s string) bool      { return s != "" && len(s) <= 256 && !strings.ContainsAny(s, "\x00\r\n\t ") }
func dingRobotID(s string) bool { return dingID(s) && len(s) <= 200 }

func validDingMessageIdentity(b Binding, m dingMessage, eventID string) bool {
	return dingID(eventID) && dingID(m.Corp) && dingRobotID(m.Robot) && dingID(m.BotAlias) &&
		dingID(m.Staff) && dingID(m.MessageID) && dingID(m.ChatID) && m.Created >= 0 && m.SenderCorp == m.Corp &&
		(m.AppID == "" || m.AppID == b.Credentials.AppID) && (m.AppKey == "" || m.AppKey == b.Credentials.AppID) &&
		(b.TenantID == "" || b.TenantID == m.Corp) && (b.BotOpenID == "" || b.BotOpenID == m.Robot)
}

func normalizeDingTalk(b Binding, f dingFrame) (Inbound, bool, error) {
	var v Inbound
	if f.Type != "CALLBACK" || f.Headers["topic"] != dingRobotTopic {
		return v, false, nil
	}
	var m dingMessage
	if len(f.Data) > 1<<20 || json.Unmarshal([]byte(f.Data), &m) != nil {
		return v, false, errors.New("invalid dingtalk frame")
	}
	if !validDingMessageIdentity(b, m, f.Headers["messageId"]) {
		return v, false, errors.New("dingtalk identity mismatch")
	}
	media := dingMedia(m.Type, m.Content)
	if m.SenderAlias == m.BotAlias || (m.Type != "text" && m.Type != "richText" && len(media) == 0) {
		return v, false, nil
	}
	if m.Kind != "1" && m.Kind != "2" {
		return v, false, errors.New("invalid dingtalk conversation")
	}
	if m.Kind == "2" && !m.Mentioned {
		return v, false, nil
	}
	if len(m.SenderName) > 1024 || strings.ContainsRune(m.SenderName, 0) || len(m.ChatName) > 512 || strings.ContainsRune(m.ChatName, 0) {
		return v, false, errors.New("invalid dingtalk chat name")
	}
	body := m.Text.Content
	if m.Type == "richText" {
		var err error
		body, err = dingRichText(m.Content)
		if err != nil || strings.TrimSpace(body) == "" && len(media) == 0 {
			return v, false, err
		}
	}
	if len(body) > 65536 || strings.ContainsRune(body, 0) {
		return v, false, errors.New("invalid dingtalk text")
	}
	kind := "group"
	if m.Kind == "1" {
		kind = "direct"
	}
	return Inbound{Media: media, LogProvider: "dingtalk", BindingID: b.ID, Generation: b.Generation, Revision: b.Revision, RulesVersion: b.RulesVersion, BotID: m.Robot, BotMentioned: m.Mentioned, EventID: f.Headers["messageId"], MessageID: m.MessageID, TenantID: m.Corp, ChatID: m.ChatID, ChatName: m.ChatName, ChatType: kind, SenderTenant: m.SenderCorp, SenderIDType: "user_id", SenderID: m.Staff, SenderName: m.SenderName, Text: body, MessageType: m.Type, CreatedAtMS: m.Created, Mentions: []Mention{}}, true, nil
}
func dingEndpoint(endpoint, ticket string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "wss" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") || (u.Hostname() != "dingtalk.com" && !strings.HasSuffix(u.Hostname(), ".dingtalk.com")) || ticket == "" || len(ticket) > 8192 {
		return "", errors.New("invalid dingtalk stream endpoint")
	}
	q := url.Values{"ticket": []string{ticket}}
	u.RawQuery = q.Encode()
	return u.String(), nil
}
func DingTalk(ctx context.Context, b Binding, backend *Backend) error {
	api := newDingHTTP()
	var endpoint struct {
		Endpoint string `json:"endpoint"`
		Ticket   string `json:"ticket"`
	}
	if err := api.request(ctx, "POST", "/v1.0/gateway/connections/open", "", map[string]any{"clientId": b.Credentials.AppID, "clientSecret": b.Credentials.AppSecret, "ua": "kiko-event-hub", "subscriptions": []map[string]string{{"type": "CALLBACK", "topic": dingRobotTopic}}}, &endpoint); err != nil {
		return err
	}
	address, err := dingEndpoint(endpoint.Endpoint, endpoint.Ticket)
	if err != nil {
		return err
	}
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second, Proxy: http.ProxyFromEnvironment}
	conn, resp, err := dialer.DialContext(ctx, address, nil)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close() //nolint:errcheck // Closing an already closed transport cannot restore the session.
	}
	if err != nil {
		return errors.New("dingtalk stream unavailable")
	}
	defer func() { _ = conn.Close() }() //nolint:errcheck // Read loop owns the operational error.
	return runDingStream(ctx, b, backend, conn)
}
func runDingStream(ctx context.Context, b Binding, backend *Backend, conn *websocket.Conn) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopped := make(chan struct{})
	defer func() { cancel(); <-stopped }()
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(8 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				_ = conn.Close() //nolint:errcheck // Closing an already closed transport cannot restore the session.
				return
			case <-ticker.C:
				if conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)) != nil {
					_ = conn.Close() //nolint:errcheck // Closing an already closed transport cannot restore the session.
					return
				}
			}
		}
	}()
	conn.SetReadLimit(1 << 20)
	if err := conn.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return err
	}
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(30 * time.Second)) })
	if err := backend.Applied(ctx, b.ID, status(b, "connected", "")); err != nil {
		return err
	}
	for {
		var f dingFrame
		if err := conn.ReadJSON(&f); err != nil {
			return errors.New("dingtalk stream disconnected")
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if f.Type == "SYSTEM" {
			if f.Headers["topic"] == "disconnect" {
				return errors.New("dingtalk server disconnected")
			}
			if f.Headers["topic"] == "ping" {
				if err := dingWrite(conn, map[string]any{"code": 200, "headers": f.Headers, "message": "OK", "data": f.Data}); err != nil {
					return err
				}
			}
			continue
		}

		incoming := Inbound{BindingID: b.ID, Generation: b.Generation, EventID: f.Headers["messageId"]}
		if f.Type == "CALLBACK" && f.Headers["topic"] == dingRobotTopic {
			backend.traceInbound(ctx, "channel.callback.received", "dingtalk", incoming, "received")
		}
		v, matched, err := normalizeDingTalk(b, f)
		if err != nil {
			backend.traceInbound(ctx, "channel.callback.result", "dingtalk", incoming, "invalid_or_ignored")
			continue
		}
		if f.Type != "CALLBACK" || f.Headers["topic"] != dingRobotTopic {
			continue
		}
		identityChanged := matched && (b.BotOpenID != v.BotID || b.TenantID != v.TenantID)
		if matched {
			if err = backend.Inbound(ctx, v); err != nil {
				backend.traceInbound(ctx, "channel.callback.result", "dingtalk", v, "forward_failed")
				return err
			}
			b.BotOpenID = v.BotID
			b.TenantID = v.TenantID
		}
		if matched {
			backend.traceInbound(ctx, "channel.callback.result", "dingtalk", v, "durably_received")
		} else {
			backend.traceInbound(ctx, "channel.callback.result", "dingtalk", incoming, "filtered")
		}
		// SUCCESS only after durable receipt (or intentional filtering). No send reply here.
		if err = dingWrite(conn, map[string]any{"code": 200, "headers": map[string]string{"contentType": "application/json", "messageId": f.Headers["messageId"]}, "message": "OK", "data": `{"response":{"status":"SUCCESS"}}`}); err != nil {
			return err
		}
		if identityChanged && backend.OnObserved != nil {
			backend.OnObserved()
		}
	}
}
func dingWrite(c *websocket.Conn, v any) error {
	if err := c.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return err
	}
	return c.WriteJSON(v)
}
