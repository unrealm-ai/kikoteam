package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	dingSessionHost = "oapi.dingtalk.com"
	dingSessionPath = "/robot/sendBySession"
	dingSessionCap  = 2048
	dingSessionTTL  = 2 * time.Hour
)

type dingSessionKey struct {
	binding, tenant, bot, chat, kind, message string
	generation, revision, rules               int64
}

type dingSessionReply struct {
	address, sender string
	expires         time.Time
}

type dingSessionReplies struct {
	mu        sync.Mutex
	items     map[dingSessionKey]dingSessionReply
	nextPrune time.Time
}

func dingSessionURL(raw string) bool {
	if len(raw) > 4096 || strings.Contains(raw, "#") {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != dingSessionHost || u.User != nil || u.Opaque != "" ||
		u.EscapedPath() != dingSessionPath || u.Path != dingSessionPath {
		return false
	}
	q, err := url.ParseQuery(u.RawQuery)
	return err == nil && len(q) == 1 && len(q["session"]) == 1 && q.Get("session") != "" &&
		!strings.ContainsAny(q.Get("session"), "\x00\r\n\t ")
}

func (c *dingSessionReplies) prune(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if now.Before(c.nextPrune) {
		return
	}
	for k, item := range c.items {
		if !now.Before(item.expires) {
			delete(c.items, k)
		}
	}
	c.nextPrune = now.Add(time.Minute)
}

func (c *dingSessionReplies) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = nil
	c.nextPrune = time.Time{}
}

func (c *dingSessionReplies) remember(v Inbound, data string, now time.Time) {
	var fields struct {
		URL       string `json:"sessionWebhook"`
		ExpiresMS int64  `json:"sessionWebhookExpiredTime"`
	}
	if json.Unmarshal([]byte(data), &fields) != nil || !dingSessionURL(fields.URL) || fields.ExpiresMS <= now.UnixMilli() {
		return
	}
	key := dingSessionKey{
		binding: v.BindingID, tenant: v.TenantID, bot: v.BotID, chat: v.ChatID, kind: v.ChatType,
		message: v.MessageID, generation: v.Generation, revision: v.Revision, rules: v.RulesVersion,
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.items == nil {
		c.items = make(map[dingSessionKey]dingSessionReply)
	}

	if _, exists := c.items[key]; exists {
		return // An original accepted message never has its capability replaced.
	}
	if len(c.items) == dingSessionCap {
		// Evict the earliest-expiring entry. A later lookup safely uses the active path.
		var oldest dingSessionKey
		var earliest time.Time
		for k, v := range c.items {
			if earliest.IsZero() || v.expires.Before(earliest) {
				oldest, earliest = k, v.expires
			}
		}
		delete(c.items, oldest)
	}
	expires := time.UnixMilli(fields.ExpiresMS)
	if maxExpiry := now.Add(dingSessionTTL); expires.After(maxExpiry) {
		expires = maxExpiry
	}
	c.items[key] = dingSessionReply{address: fields.URL, sender: v.SenderID, expires: expires}
}

func (c *dingSessionReplies) lookup(b Binding, d Delivery, now time.Time) (dingSessionReply, bool) {
	if !d.SessionWebhookEligible || !b.Enabled || d.BindingID != b.ID || d.Generation != b.Generation || d.TenantID != b.TenantID ||
		d.Provider != b.Provider || d.ConfigRevision != 0 && d.ConfigRevision != b.Revision ||
		d.ArtifactID != "" || d.Media != nil || !dingID(d.ReplyToMessageID) || !dingTarget(b, d) ||
		d.contentFormat() != "text" && d.contentFormat() != "markdown" ||
		!dingID(d.ChatID) || strings.TrimSpace(d.Text) == "" || len(d.Text) > 65536 || strings.ContainsRune(d.Text, 0) {
		return dingSessionReply{}, false
	}
	key := dingSessionKey{
		binding: b.ID, tenant: b.TenantID, bot: b.BotOpenID, chat: d.ChatID, kind: d.ChatType,
		message: d.ReplyToMessageID, generation: b.Generation, revision: b.Revision, rules: b.RulesVersion,
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	item, present := c.items[key]
	if present && !now.Before(item.expires) {
		delete(c.items, key)
		return dingSessionReply{}, false
	}
	if d.ChatType == "direct" && d.RecipientID != item.sender {
		return dingSessionReply{}, false
	}
	return item, present
}

// forwardDingInbound is called only for matching, normalized Stream callbacks.
// The capability is intentionally absent from Inbound and the Backend request.
func (b *Backend) forwardDingInbound(ctx context.Context, v Inbound, data string) error {
	r, err := b.receiveInbound(ctx, v)
	if err == nil && r.Status == "accepted" && r.ControlReply == "" && b.dingReplies != nil {
		b.dingReplies.remember(v, data, time.Now())
	}
	return err
}

func newDingSessionHTTP() *http.Client {
	return &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func sendDingSessionReply(ctx context.Context, b Binding, d Delivery, cache *dingSessionReplies, client *http.Client, active deliverySender) deliveryReceipt {
	item, ok := cache.lookup(b, d, time.Now())
	if !ok {
		return active(ctx, b, d)
	}
	if d.BeforeDispatch != nil && d.BeforeDispatch() != nil || ctx.Err() != nil {
		return deliveryReceipt{Status: "failed", EvidenceType: "not_dispatched", ErrorCode: "binding_changed_before_send"}
	}
	if !time.Now().Before(item.expires) {
		return active(ctx, b, d)
	}
	return postDingSessionReply(ctx, client, d, item.address)
}

func postDingSessionReply(ctx context.Context, client *http.Client, d Delivery, address string) deliveryReceipt {
	if !dingSessionURL(address) {
		return deliveryReceipt{Status: "failed", EvidenceType: "not_dispatched", ErrorCode: "invalid_dingtalk_session_target"}
	}
	kind := d.contentFormat()
	fields := map[string]string{"content": d.Text}
	if kind == "markdown" {
		fields = map[string]string{"title": "AI 员工回复", "text": d.Text}
	}
	body, err := json.Marshal(map[string]any{"msgtype": kind, kind: fields})
	if err != nil {
		return deliveryReceipt{Status: "failed", EvidenceType: "not_dispatched", ErrorCode: "invalid_text"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, address, bytes.NewReader(body))
	if err != nil {
		return deliveryReceipt{Status: "failed", EvidenceType: "not_dispatched", ErrorCode: "invalid_dingtalk_session_target"}
	}
	req.Header.Set("Content-Type", "application/json")
	// Do not transfer an app bearer token, persistent idempotency headers, or follow redirects.
	resp, err := client.Do(req)
	if err != nil {
		return unknownDingSessionReply()
	}
	defer func() { _ = resp.Body.Close() }() //nolint:errcheck // Closing the response cannot undo the provider write.
	return dingSessionReceipt(d, resp)
}

func unknownDingSessionReply() deliveryReceipt {
	return deliveryReceipt{Status: "unknown", ErrorCode: "dingtalk_session_reply_unconfirmed"}
}

func dingSessionReceipt(d Delivery, resp *http.Response) deliveryReceipt {
	if resp.StatusCode < 200 || resp.StatusCode >= 500 || resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return unknownDingSessionReply()
	}
	const limit = 65536
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	var ack struct {
		ErrCode *int64 `json:"errcode"`
	}
	if err != nil || len(data) > limit || json.Unmarshal(data, &ack) != nil || ack.ErrCode == nil {
		return unknownDingSessionReply()
	}
	if *ack.ErrCode != 0 {
		return deliveryReceipt{Status: "failed", EvidenceType: "send_response", ErrorCode: "dingtalk_session_reply_rejected"}
	}
	if resp.StatusCode >= 300 {
		return unknownDingSessionReply()
	}
	return deliveryReceipt{Status: "sent", EvidenceType: "session_webhook", EvidenceID: d.ReplyToMessageID}
}
