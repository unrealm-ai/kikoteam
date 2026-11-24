package channel

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// Delivery targets are returned by Manager, never inferred from message text.
type Delivery struct {
	taskDetailDispatch       bool
	taskDetailNotice         bool
	taskDetailNoticeMarkdown bool
	TriggerResultCard        bool            `json:"trigger_result_card,omitempty"`
	TaskDetailCard           *taskDetailCard `json:"task_detail_card,omitempty"`
	TaskDetailURL            string          `json:"task_detail_url,omitempty"`
	Presentation             string          `json:"presentation,omitempty"`
	SessionWebhookEligible   bool            `json:"session_webhook_eligible,omitempty"`
	ArtifactID               string          `json:"artifact_id,omitempty"`
	Media                    *deliveryMedia  `json:"-"`
	BeforeDispatch           func() error    `json:"-"`
	Provider                 string          `json:"provider"`
	TenantID                 string          `json:"external_tenant_id"`
	ChatType                 string          `json:"chat_type"`
	RecipientID              string          `json:"recipient_id"`
	ProviderRequestID        string          `json:"provider_request_id,omitempty"`
	TargetDigest             string          `json:"target_digest"`
	QueryToken               string          `json:"query_token,omitempty"`
	ID                       string          `json:"id"`
	LeaseToken               string          `json:"lease_token"`
	BindingID                string          `json:"binding_id"`
	Generation               int64           `json:"connection_generation"`
	ConfigRevision           int64           `json:"config_revision,omitempty"`
	ChatID                   string          `json:"chat_id"`
	ReplyToMessageID         string          `json:"reply_to_message_id"`
	ThreadID                 string          `json:"thread_id"`
	Text                     string          `json:"text"`
	Format                   string          `json:"format,omitempty"`
	IdempotencyKey           string          `json:"idempotency_key"`
}

func (d Delivery) contentFormat() string {
	if d.Format == "" {
		return "text"
	}
	return d.Format
}

type deliveryReceipt struct {
	Presentation        string `json:"presentation,omitempty"`
	ProviderCreatedAtMS int64  `json:"provider_created_at_ms,string,omitempty"`
	ProviderChatID      string `json:"provider_chat_id,omitempty"`
	ProviderThreadID    string `json:"provider_thread_id,omitempty"`
	ProviderRequestID   string `json:"provider_request_id,omitempty"`
	TargetDigest        string `json:"target_digest"`
	QueryToken          string `json:"query_token,omitempty"`
	EvidenceType        string `json:"evidence_type,omitempty"`
	EvidenceID          string `json:"evidence_id,omitempty"`
	ProviderSessionID   string `json:"provider_session_id,omitempty"`
	LeaseToken          string `json:"lease_token"`
	Status              string `json:"status"`
	MessageID           string `json:"provider_message_id,omitempty"`
	ErrorCode           string `json:"error_code,omitempty"`
}
type deliverySender func(context.Context, Binding, Delivery) deliveryReceipt

// RunDeliveries never retries an external send after ambiguity. Only receipt
// persistence may use a bounded cleanup context after cancellation.
func RunDeliveries(ctx context.Context, b *Backend, lookup func(string) (Binding, bool)) error {
	if b.humanNoticeDelivery || b.notificationDelivery || b.dingReplies == nil {
		return runDeliveries(ctx, b, lookup, sendProviderDelivery)
	}
	client := newDingSessionHTTP()
	return runDeliveries(ctx, b, lookup, func(ctx context.Context, binding Binding, d Delivery) deliveryReceipt {
		if binding.Provider == "dingtalk" {
			return sendDingSessionReply(ctx, binding, d, b.dingReplies, client, sendDingTalkDelivery)
		}
		return sendProviderDelivery(ctx, binding, d)
	})
}
func runDeliveries(ctx context.Context, b *Backend, lookup func(string) (Binding, bool), send deliverySender) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if b.dingReplies != nil && !b.humanNoticeDelivery && !b.notificationDelivery {
			b.dingReplies.prune(time.Now())
		}
		var batch struct {
			Items []Delivery `json:"items"`
		}
		if err := b.request(ctx, "POST", b.deliveryPath()+"/claim", map[string]int{"limit": 1}, &batch); err != nil {
			return err
		}
		for _, d := range batch.Items {
			if err := deliver(ctx, b, lookup, send, d); err != nil {
				return err
			}
		}
		if err := reconcileDeliveryQueries(ctx, b, lookup, queryDingTalkDelivery); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
func deliveryBinding(ctx context.Context, lookup func(string) (Binding, bool), d Delivery) (Binding, bool) {
	if ctx.Err() != nil {
		return Binding{}, false
	}
	b, ok := lookup(d.BindingID)
	return b, ok && b.Enabled && supportedProvider(b.Provider) && (d.TenantID == "" || d.TenantID == b.TenantID) && (d.Provider == "" || b.Provider == d.Provider) && b.Generation == d.Generation && (d.ConfigRevision == 0 || b.Revision == d.ConfigRevision) && (b.Provider != "wecom" || wecomReady(b)) && b.Credentials.AppID != "" && b.Credentials.AppSecret != ""
}
func deliver(ctx context.Context, b *Backend, lookup func(string) (Binding, bool), send deliverySender, d Delivery) error {
	b.traceDelivery(ctx, "channel.delivery.claimed", d, "claimed")
	if _, ok := deliveryBinding(ctx, lookup, d); !ok {
		b.traceDelivery(ctx, "channel.delivery.authorize", d, "binding_unavailable")
		return ctx.Err()
	} // Unsent leases expire safely.
	path := b.deliveryPath() + "/" + url.PathEscape(d.ID)
	var authorized struct {
		Delivery
		Allowed bool `json:"allowed"`
	}
	b.traceDelivery(ctx, "channel.delivery.authorize", d, "started")
	if err := b.request(ctx, "POST", path+"/authorize", map[string]string{"lease_token": d.LeaseToken}, &authorized); err != nil {
		b.traceDelivery(ctx, "channel.delivery.authorize_result", d, "failed")
		var rejected *BackendError
		if errors.As(err, &rejected) && rejected.Status == 409 {
			return nil
		}
		return err
	}
	current := authorized.Delivery
	// Authorization must not silently replace the claimed operation or target.
	if !authorized.Allowed || !sameDeliveryTarget(d, current) {
		b.traceDelivery(ctx, "channel.delivery.authorize_result", d, "mismatch")
		return errors.New("channel delivery authorization mismatch")
	}
	b.traceDelivery(ctx, "channel.delivery.authorize_result", d, "allowed")
	binding, ok := deliveryBinding(ctx, lookup, current)
	if !ok {
		return submitDeliveryReceipt(ctx, b, path, deliveryReceipt{LeaseToken: d.LeaseToken, TargetDigest: current.TargetDigest, Status: "failed", EvidenceType: "not_dispatched", ErrorCode: "binding_unavailable_before_send"})
	}
	if err := ctx.Err(); err != nil {
		return submitDeliveryReceipt(ctx, b, path, deliveryReceipt{LeaseToken: d.LeaseToken, TargetDigest: current.TargetDigest, Status: "failed", EvidenceType: "not_dispatched", ErrorCode: "cancelled_before_send"})
	}
	current.BeforeDispatch = func() error {
		if b.humanNoticeDelivery || current.ArtifactID != "" || current.TaskDetailCard != nil {
			if err := b.request(ctx, "POST", path+"/dispatch-check", map[string]string{"lease_token": d.LeaseToken}, nil); err != nil {
				return err
			}
		}
		if latest, valid := deliveryBinding(ctx, lookup, current); !valid || latest.Revision != binding.Revision || latest.RulesVersion != binding.RulesVersion || latest.AppID != binding.AppID || latest.BotOpenID != binding.BotOpenID || latest.Credentials != binding.Credentials {
			return errors.New("channel binding changed before send")
		}
		return ctx.Err()
	}
	b.traceDelivery(ctx, "channel.delivery.send", current, "started")
	if current.ArtifactID != "" {
		media, e := b.deliveryMedia(ctx, path, current)
		if e != nil {
			return submitDeliveryReceipt(ctx, b, path, deliveryReceipt{LeaseToken: d.LeaseToken, TargetDigest: current.TargetDigest, Status: "failed", EvidenceType: "not_dispatched", ErrorCode: "artifact_unavailable"})
		}
		current.Media = media
	}

	receipt := sendWithTaskDetail(ctx, binding, current, b.notificationDelivery, send)
	b.traceDelivery(ctx, "channel.delivery.send_result", current, receipt.Status)
	b.trace(ctx, "channel.delivery.provider_receipt", "delivery_id", traceID(d.ID), "provider", binding.Provider, "provider_message_id", traceID(receipt.MessageID), "provider_request_id", traceID(receipt.ProviderRequestID), "status", traceID(receipt.Status), "error_code", traceID(receipt.ErrorCode))
	receipt.LeaseToken = d.LeaseToken
	receipt.TargetDigest = current.TargetDigest
	return submitDeliveryReceipt(ctx, b, path, receipt)
}

func sameDeliveryTarget(claimed, authorized Delivery) bool {
	return authorized.ID == claimed.ID && authorized.LeaseToken == claimed.LeaseToken &&
		authorized.BindingID == claimed.BindingID && authorized.Generation == claimed.Generation && authorized.ConfigRevision == claimed.ConfigRevision &&
		authorized.IdempotencyKey != "" && authorized.IdempotencyKey == claimed.IdempotencyKey &&
		authorized.ChatID == claimed.ChatID && authorized.ReplyToMessageID == claimed.ReplyToMessageID &&
		authorized.ArtifactID == claimed.ArtifactID && authorized.ThreadID == claimed.ThreadID && authorized.Text == claimed.Text &&
		authorized.contentFormat() == claimed.contentFormat() && authorized.Provider == claimed.Provider &&
		authorized.TenantID == claimed.TenantID && authorized.ChatType == claimed.ChatType &&
		authorized.RecipientID == claimed.RecipientID && authorized.TargetDigest != "" &&
		claimed.TaskDetailURL == "" && !claimed.TriggerResultCard && sameTaskDetailTarget(claimed, authorized)
}

func sendFeishuDelivery(ctx context.Context, b Binding, d Delivery) deliveryReceipt {
	options := []lark.ClientOptionFunc{lark.WithLogger(quietLogger{}), lark.WithReqTimeout(10 * time.Second)}
	if d.taskDetailDispatch {
		// Never replay a bearer-containing message POST following an HTTP
		// 307/308 (even to the same host) or send it to another destination.
		options = append(options, lark.WithHttpClient(noRedirectTaskDetailHTTP()))
	}
	api := lark.NewClient(b.Credentials.AppID, b.Credentials.AppSecret, options...)
	return sendFeishuWithClient(ctx, api, d)
}
func noRedirectTaskDetailHTTP() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func sendFeishuWithClient(ctx context.Context, api *lark.Client, d Delivery) deliveryReceipt {
	var kind string
	var bodyContent any
	switch d.contentFormat() {
	case taskDetailCardFormat:
		if d.TaskDetailCard == nil {
			return deliveryReceipt{Status: "failed", EvidenceType: "not_dispatched", ErrorCode: "invalid_task_detail_card"}
		}
		kind = "interactive"
		bodyContent = feishuTaskDetailContent(d)
	case "text":
		kind = "text"
		bodyContent = map[string]string{"text": d.Text}
	case "image", "file":
		fields, e := feishuUpload(ctx, api, d)
		if e != nil {
			return mediaUploadFailure()
		}
		kind = d.Format
		bodyContent = fields
	case "markdown":
		kind = "post"
		bodyContent = map[string]any{"zh_cn": map[string]any{
			"content": [][]map[string]string{{{"tag": "md", "text": d.Text}}},
		}}
	default:
		return deliveryReceipt{Status: "failed", EvidenceType: "not_dispatched", ErrorCode: "unsupported_delivery_format"}
	}
	content, err := json.Marshal(bodyContent)
	if err != nil {
		return deliveryReceipt{Status: "failed", EvidenceType: "not_dispatched", ErrorCode: "invalid_text"}
	}
	if d.ThreadID != "" {
		if code := checkFeishuTopicSource(ctx, api, d); code != "" {
			return deliveryReceipt{Status: "failed", EvidenceType: "not_dispatched", ErrorCode: code}
		}
	}
	// Recheck the binding after the source lookup and immediately before the write.
	if d.BeforeDispatch != nil {
		if err := d.BeforeDispatch(); err != nil {
			return deliveryReceipt{Status: "failed", EvidenceType: "not_dispatched", ErrorCode: "binding_changed_before_send"}
		}
	}
	if ctx.Err() != nil {
		return deliveryReceipt{Status: "failed", EvidenceType: "not_dispatched", ErrorCode: "send_cancelled"}
	}
	if d.ReplyToMessageID != "" {
		body := larkim.NewReplyMessageReqBodyBuilder().MsgType(kind).Content(string(content)).Uuid(d.IdempotencyKey)
		if d.ThreadID != "" {
			body.ReplyInThread(true)
		}
		resp, err := api.Im.Message.Reply(ctx, larkim.NewReplyMessageReqBuilder().MessageId(d.ReplyToMessageID).Body(body.Build()).Build())
		if err != nil || resp == nil {
			return deliveryReceipt{Status: "unknown", ErrorCode: "provider_result_unknown"}
		}
		if !resp.Success() {
			return feishuMessageRejected(d.taskDetailDispatch, kind, resp.Code, resp.StatusCode, resp.Data != nil && value(resp.Data.MessageId) != "")
		}
		if resp.Data != nil && value(resp.Data.MessageId) != "" {
			return feishuSentReceipt(d, value(resp.Data.MessageId), value(resp.Data.CreateTime), value(resp.Data.ChatId), value(resp.Data.ThreadId))
		}
	} else {
		receiveType, receiveID := "chat_id", d.ChatID
		if d.ChatType == "direct" && d.RecipientID != "" && d.ChatID == "" {
			receiveType, receiveID = "open_id", d.RecipientID
		}
		resp, err := api.Im.Message.Create(ctx, larkim.NewCreateMessageReqBuilder().ReceiveIdType(receiveType).Body(larkim.NewCreateMessageReqBodyBuilder().ReceiveId(receiveID).MsgType(kind).Content(string(content)).Uuid(d.IdempotencyKey).Build()).Build())
		if err != nil || resp == nil {
			return deliveryReceipt{Status: "unknown", ErrorCode: "provider_result_unknown"}
		}
		if !resp.Success() {
			return feishuMessageRejected(d.taskDetailDispatch, kind, resp.Code, resp.StatusCode, resp.Data != nil && value(resp.Data.MessageId) != "")
		}
		if resp.Data != nil && value(resp.Data.MessageId) != "" {
			return feishuSentReceipt(d, value(resp.Data.MessageId), value(resp.Data.CreateTime), value(resp.Data.ChatId), value(resp.Data.ThreadId))
		}
	}
	return deliveryReceipt{Status: "unknown", ErrorCode: "provider_receipt_missing"}
}

func feishuMessageRejected(taskDetailDispatch bool, kind string, code, status int, hasMessageID bool) deliveryReceipt {
	// Documented card validation failure; do not fall back on generic errors,
	// contrary message evidence, or server/proxy failures.
	if taskDetailDispatch && (hasMessageID || status >= http.StatusMultipleChoices && status < http.StatusBadRequest || status >= http.StatusInternalServerError) {
		return deliveryReceipt{Status: "unknown", ErrorCode: "provider_result_unknown"}
	}
	if kind == "interactive" {
		if code == 230099 && (status == http.StatusOK || status == http.StatusBadRequest) {
			return deliveryReceipt{Status: "failed", EvidenceType: "send_response", ErrorCode: taskDetailRejected}
		}
	}
	return deliveryReceipt{Status: "failed", EvidenceType: "send_response", ErrorCode: "provider_rejected"}
}

func checkFeishuTopicSource(ctx context.Context, api *lark.Client, d Delivery) string {
	if d.ReplyToMessageID == "" || d.ChatID == "" {
		return "thread_reply_target_missing"
	}
	resp, err := api.Im.Message.Get(ctx, larkim.NewGetMessageReqBuilder().MessageId(d.ReplyToMessageID).Build())
	if err != nil || resp == nil || !resp.Success() || resp.Data == nil || len(resp.Data.Items) != 1 || resp.Data.Items[0] == nil {
		return "thread_reply_source_unavailable"
	}
	source := resp.Data.Items[0]
	if value(source.MessageId) != d.ReplyToMessageID || value(source.ChatId) != d.ChatID || value(source.ThreadId) != d.ThreadID {
		return "thread_reply_source_mismatch"
	}
	if source.Deleted != nil && *source.Deleted {
		return "thread_reply_source_unavailable"
	}
	return ""
}

func feishuSentReceipt(d Delivery, id, created, chat, thread string) deliveryReceipt {
	r := deliveryReceipt{Status: "sent", MessageID: id, EvidenceType: "send_response", EvidenceID: id}
	if d.ThreadID != "" {
		r.ProviderChatID, r.ProviderThreadID = chat, thread
		if chat == "" || chat != d.ChatID || thread != d.ThreadID {
			r.Status, r.ErrorCode = "unknown", "thread_reply_receipt_unconfirmed"
			return r
		}
	}
	at, err := strconv.ParseInt(created, 10, 64)
	if d.ChatType == "direct" && d.RecipientID != "" && d.ChatID == "" {
		if chat == "" || thread != "" {
			return deliveryReceipt{Status: "unknown", ErrorCode: "provider_receipt_missing"}
		}
		r.ProviderChatID = chat
		if err == nil && at > 0 {
			r.ProviderCreatedAtMS = at
		}
	}
	if err == nil && at > 0 && chat == d.ChatID && thread == d.ThreadID {
		r.ProviderCreatedAtMS = at
		r.ProviderChatID = chat
		r.ProviderThreadID = thread
	}
	return r
}
