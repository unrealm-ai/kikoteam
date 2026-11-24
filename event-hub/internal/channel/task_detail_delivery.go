package channel

import (
	"context"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

const (
	taskDetailCardFormat = "task_detail_card"
	taskDetailLinkFormat = "task_detail_link"
	taskDetailRejected   = "task_detail_card_unsupported"
	taskDetailLabel      = "查看任务详情"
)

type taskDetailCard struct {
	Title string `json:"title"`
	State string `json:"state"`
	URL   string `json:"url,omitempty"`
}

var (
	taskDetailPath = regexp.MustCompile(`^/tasks/[A-Za-z0-9_-]{20,256}$`)
	taskDetailHost = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
)

func validTaskDetailURL(raw string) bool {
	if len(raw) > 1024 || strings.ContainsAny(raw, "#\\\r\n\t ") {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil ||
		strings.ContainsAny(u.Host, "()<>%") || strings.HasSuffix(u.Host, ":") ||
		u.RawQuery != "" || u.ForceQuery || !taskDetailPath.MatchString(u.EscapedPath()) {
		return false
	}
	host := u.Hostname()
	if strings.HasPrefix(u.Host, "[") {
		addr, err := netip.ParseAddr(host)
		if err != nil || !addr.Is6() {
			return false
		}
	} else if !taskDetailHost.MatchString(host) {
		return false
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		return err == nil && n > 0 && n <= 65535
	}
	// Manager authorized this configured origin. It is only rendered as a
	// navigation target; the Hub never resolves or fetches the bearer link.
	return true
}

func sameTaskDetailTarget(claimed, authorized Delivery) bool {
	a, b := claimed.TaskDetailCard, authorized.TaskDetailCard
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	// The URL isn't present in the claim; Manager authenticates and binds it to
	// this exact operation when authorizing the original lease/target digest.
	return a.Title == b.Title && a.State == b.State && a.URL == ""
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

func taskDetailLink(raw string) string { return "[" + taskDetailLabel + "](" + raw + ")" }

func sendTaskDetail(ctx context.Context, b Binding, d Delivery, send deliverySender) deliveryReceipt {
	card := d.TaskDetailCard
	if card == nil || !validTaskDetailURL(card.URL) || d.ArtifactID != "" || d.Media != nil {
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
	link.TaskDetailCard, link.Format, link.Text = nil, "markdown", taskDetailLink(card.URL)
	native := d
	native.Format = taskDetailCardFormat
	native.Text = taskDetailState(card.State) // Plain state, never raw arbitrary Markdown.
	r := send(ctx, b, native)
	if r.Status == "failed" && r.EvidenceType == "send_response" && r.ErrorCode == taskDetailRejected {
		r = send(ctx, b, link)
		r.Presentation = taskDetailLinkFormat
	} else {
		r.Presentation = taskDetailCardFormat
	}
	return r
}

func sendWithTaskDetail(ctx context.Context, b Binding, d Delivery, isNotification bool, send deliverySender) deliveryReceipt {
	if d.TaskDetailCard != nil {
		return sendTaskDetail(ctx, b, d, send)
	}
	// Hook/schedule result notices retain their original body, route and send
	// attempt; a mutually optional invalid addon must never block the notice.
	if isNotification && d.ArtifactID == "" && validTaskDetailURL(d.TaskDetailURL) {
		original := d
		d.Format = "markdown"
		d.taskDetailDispatch = true
		d.Text += "\n\n" + taskDetailLink(d.TaskDetailURL)
		// Keep the existing original notification intact if it cannot carry an
		// appended link within the provider's existing outbound payload bounds.
		switch {
		case b.Provider == "wecom" && len(d.Text) > 16384, b.Provider == "dingtalk" && len(d.Text) > 65536, b.Provider == "feishu" && len(d.Text) > 30000:
			return send(ctx, b, original)
		}
		r := send(ctx, b, d)
		r.Presentation = taskDetailLinkFormat
		return r
	}
	return send(ctx, b, d)
}

func feishuTaskDetailContent(d Delivery) map[string]any {
	return map[string]any{
		"schema": "2.0",
		"header": map[string]any{
			"title":    map[string]string{"tag": "plain_text", "content": taskDetailTitle(d.TaskDetailCard.Title)},
			"subtitle": map[string]string{"tag": "plain_text", "content": d.Text},
		},
		"body": map[string]any{"elements": []any{map[string]any{
			"tag": "button", "type": "primary",
			"text":      map[string]string{"tag": "plain_text", "content": taskDetailLabel},
			"behaviors": []map[string]string{{"type": "open_url", "default_url": d.TaskDetailCard.URL}},
		}}},
	}
}

func wecomTaskDetailContent(d Delivery) map[string]any {
	return map[string]any{
		"card_type":   "text_notice",
		"main_title":  map[string]string{"title": taskDetailTitle(d.TaskDetailCard.Title), "desc": d.Text},
		"card_action": map[string]any{"type": 1, "url": d.TaskDetailCard.URL},
		"jump_list":   []map[string]any{{"type": 1, "title": taskDetailLabel, "url": d.TaskDetailCard.URL}},
	}
}
