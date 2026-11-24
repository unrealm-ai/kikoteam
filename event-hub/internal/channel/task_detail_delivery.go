package channel

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/unrealm-ai/kikoteam/event-hub/internal/taskdetailurl"
)

const (
	taskDetailCardFormat = "task_detail_card"
	taskDetailLinkFormat = "task_detail_link"
	taskDetailRejected   = "task_detail_card_unsupported"
	taskDetailLabel      = taskdetailurl.Label
)

type taskDetailCard struct {
	Description string `json:"description,omitempty"`
	Title       string `json:"title"`
	State       string `json:"state"`
	URL         string `json:"url,omitempty"`
}

func sameTaskDetailTarget(claimed, authorized Delivery) bool {
	a, b := claimed.TaskDetailCard, authorized.TaskDetailCard
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	// The URL isn't present in the claim; Manager authenticates and binds it to
	// this exact operation when authorizing the original lease/target digest.
	return a.Title == b.Title && a.State == b.State && a.Description == b.Description && a.URL == ""
}

func taskDetailTitle(title string) string {
	title = strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, title)), " ")
	if title == "" {
		return "任务详情"
	}
	runes := []rune(title)
	if len(runes) > 32 {
		return string(runes[:31]) + "…"
	}
	return title
}

func taskDetailState(state string) string {
	switch state {
	case "已排队", "执行中", "等待处理", "正在停止", "本轮已结束", "待核对":
		return state
	default:
		return "查看实时进度"
	}
}

func sendTaskDetail(ctx context.Context, b Binding, d Delivery, send deliverySender) deliveryReceipt {
	card := d.TaskDetailCard
	if card == nil || !taskdetailurl.Valid(card.URL) || d.ArtifactID != "" || d.Media != nil {
		return deliveryReceipt{Status: "failed", EvidenceType: "not_dispatched", ErrorCode: "invalid_task_detail_card"}
	}
	// On WeCom use a fresh correlated request instead of an inbound message's
	// reusable reply req_id (whose stale business ACK could confirm this card).
	if b.Provider == "wecom" {
		d.ReplyToMessageID = ""
	}
	d.SessionWebhookEligible = false
	d.taskDetailDispatch = true
	link := d
	link.TaskDetailCard, link.Format, link.Text = nil, "markdown", taskdetailurl.Link(card.URL)
	native := d
	native.Format = taskDetailCardFormat
	native.Text = taskDetailState(card.State)
	if body := card.Description; body != "" {
		link.Text = taskDetailPlainMarkdown(body) + "\n\n" + link.Text
		if b.Provider == "wecom" && utf8.RuneCountInString(body) > 112 {
			return sendDetailLink(ctx, b, link, send)
		}
		native.taskDetailNotice = true
		native.Text = body
		if b.Provider == "dingtalk" {
			native.Text = taskDetailPlainMarkdown(body)
		}
	}
	return sendDetailPresentation(ctx, b, native, link, send)
}

// Escape plain model-authored copy for Markdown-only transports; the only
// actionable link is the exact authorized URL appended by the sender.
func taskDetailPlainMarkdown(text string) string {
	return strings.NewReplacer(`\`, `\\`, "&", "&amp;", "<", "&lt;", ">", "&gt;",
		"[", `\[`, "]", `\]`, "(", `\(`, ")", `\)`, "!", `\!`, "*", `\*`,
		"_", `\_`, "`", "\\`", "#", `\#`, "-", `\-`, "+", `\+`, "|", `\|`, "~", `\~`).Replace(text)
}

func sendDetailPresentation(ctx context.Context, b Binding, native, link Delivery, send deliverySender) deliveryReceipt {
	r := send(ctx, b, native)
	if r.Status == "failed" && r.EvidenceType == "send_response" && r.ErrorCode == taskDetailRejected {
		return sendDetailLink(ctx, b, link, send)
	}
	r.Presentation = taskDetailCardFormat
	return r
}

func sendDetailLink(ctx context.Context, b Binding, d Delivery, send deliverySender) deliveryReceipt {
	r := send(ctx, b, d)
	r.Presentation = taskDetailLinkFormat
	return r
}

func sendWithTaskDetail(ctx context.Context, b Binding, d Delivery, isNotification bool, send deliverySender) deliveryReceipt {
	if d.TaskDetailCard != nil {
		return sendTaskDetail(ctx, b, d, send)
	}
	if isNotification && d.ArtifactID == "" && taskdetailurl.Valid(d.TaskDetailURL) {
		return sendNotificationTaskDetail(ctx, b, d, send)
	}
	return send(ctx, b, d)
}

func sendNotificationTaskDetail(ctx context.Context, b Binding, d Delivery, send deliverySender) deliveryReceipt {
	link := d
	link.Format = "markdown"
	link.taskDetailDispatch = true
	link.Text += "\n\n" + taskdetailurl.Link(d.TaskDetailURL)
	// Don't discard the original result if too long for the existing channel
	// body or if the link extension was not negotiated with Manager.
	switch {
	case b.Provider == "wecom" && len(link.Text) > 16384,
		b.Provider == "dingtalk" && len(link.Text) > 65536,
		b.Provider == "feishu" && len(link.Text) > 30000:
		return send(ctx, b, d)
	}
	// WeCom's text_notice body is a plain-text field with a vendor-recommended
	// maximum of 112 characters. Preserve longer results in one ordinary
	// linked notice rather than hiding/reformatting/truncating their body.
	if !d.TriggerResultCard || (b.Provider == "wecom" && utf8.RuneCountInString(d.Text) > 112) {
		return sendDetailLink(ctx, b, link, send)
	}
	native := d
	native.Format = taskDetailCardFormat
	native.TaskDetailCard = &taskDetailCard{Title: "任务结果", State: "本轮已结束", URL: d.TaskDetailURL}
	native.taskDetailDispatch = true
	native.taskDetailNotice = true
	native.taskDetailNoticeMarkdown = d.contentFormat() == "markdown"
	return sendDetailPresentation(ctx, b, native, link, send)
}

func feishuTaskDetailContent(d Delivery) map[string]any {
	subtitle := d.Text
	var elements []any
	if d.taskDetailNotice {
		subtitle = d.TaskDetailCard.State
		if d.taskDetailNoticeMarkdown {
			elements = append(elements, map[string]string{"tag": "markdown", "content": d.Text})
		} else {
			elements = append(elements, map[string]any{"tag": "div", "text": map[string]string{"tag": "plain_text", "content": d.Text}})
		}
	}
	elements = append(elements, map[string]any{
		"tag": "button", "type": "primary",
		"text":      map[string]string{"tag": "plain_text", "content": taskDetailLabel},
		"behaviors": []map[string]string{{"type": "open_url", "default_url": d.TaskDetailCard.URL}},
	})
	return map[string]any{
		"schema": "2.0",
		"header": map[string]any{
			"title":    map[string]string{"tag": "plain_text", "content": taskDetailTitle(d.TaskDetailCard.Title)},
			"subtitle": map[string]string{"tag": "plain_text", "content": subtitle},
		},
		"body": map[string]any{"elements": elements},
	}
}

func wecomTaskDetailContent(d Delivery) map[string]any {
	desc := d.Text
	if d.taskDetailNotice {
		desc = d.TaskDetailCard.State
	}
	content := map[string]any{
		"card_type":   "text_notice",
		"main_title":  map[string]string{"title": taskDetailTitle(d.TaskDetailCard.Title), "desc": desc},
		"card_action": map[string]any{"type": 1, "url": d.TaskDetailCard.URL},
		"jump_list":   []map[string]any{{"type": 1, "title": taskDetailLabel, "url": d.TaskDetailCard.URL}},
	}
	if d.taskDetailNotice {
		content["sub_title_text"] = d.Text
	}
	return content
}
