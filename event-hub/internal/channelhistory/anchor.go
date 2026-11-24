package channelhistory

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"

	"github.com/unrealm-ai/kikoteam/event-hub/internal/feishucontent"
	"github.com/unrealm-ai/kikoteam/event-hub/internal/taskdetailurl"
)

// ResolveNotificationAnchor validates a provider-owned sent message. It never
// substitutes local time or a delivery request key for a message identity.
func (r Reader) ResolveNotificationAnchor(ctx context.Context, j Job, appID string) (int64, error) {
	if r.Authorize == nil || r.Get == nil || appID == "" || j.TriggerID == "" || j.ChatID == "" {
		return 0, errors.New("notification_anchor_invalid")
	}
	if err := r.Authorize(ctx, j); err != nil {
		return 0, err
	}
	raw, err := r.Get(ctx, "/open-apis/im/v1/messages/"+url.PathEscape(j.TriggerID))
	if err != nil {
		return 0, err
	}
	if len(raw) > 1<<20 {
		return 0, errors.New("notification_anchor_too_large")
	}
	var page wirePage
	if err = json.Unmarshal(raw, &page); err != nil {
		return 0, err
	}
	if page.Code != 0 || len(page.Data.Items) != 1 {
		return 0, errors.New("notification_anchor_unavailable")
	}
	m := page.Data.Items[0]
	at, err := strconv.ParseInt(m.Created, 10, 64)
	if err != nil || at <= 0 || m.ID != j.TriggerID || m.ChatID != j.ChatID || m.ThreadID != j.ThreadID || m.Deleted || m.Sender.Type != "app" || m.Sender.ID != appID || (m.Sender.TenantKey != "" && m.Sender.TenantKey != j.TenantID) {
		return 0, errors.New("notification_anchor_scope_mismatch")
	}
	if j.TriggerTimeMS != 0 && j.TriggerTimeMS != at {
		return 0, errors.New("notification_anchor_time_mismatch")
	}
	if !matchesNotificationBody(m, j) {
		return 0, errors.New("notification_anchor_body_mismatch")
	}
	return at, nil
}

func matchesNotificationBody(m wireMessage, j Job) bool {
	switch j.TriggerFormat {
	case "", "text": // Stored jobs created before notification formats were introduced.
		text, err := feishucontent.Render("text", m.Body.Content)
		return m.Type == "text" && err == nil && text == j.TriggerText
	case "task_detail_card":
		// Message ID, sender app and tenant, conversation, thread and provider
		// time have already been verified. Don't deserialize card or bearer
		// content; only the provider's authoritative message type matters.
		return m.Type == "interactive"
	case "markdown", "task_detail_link":
		if m.Type != "post" {
			return false
		}
		// Match the single md node sent by the adapter, not a lossy rendering
		// of other nodes. PostBlocks prefers the provider's original content_v2.
		blocks, title, err := feishucontent.PostBlocks(m.Body.Content)
		if err != nil || title != "" || len(blocks) != 1 || len(blocks[0]) != 1 {
			return false
		}
		node := blocks[0][0]
		var tag, text string
		if json.Unmarshal(node["tag"], &tag) != nil || tag != "md" || json.Unmarshal(node["text"], &text) != nil {
			return false
		}
		if j.TriggerFormat == "task_detail_link" {
			return taskdetailurl.MatchesAppended(j.TriggerText, text)
		}
		return text == j.TriggerText
	default:
		return false
	}
}
