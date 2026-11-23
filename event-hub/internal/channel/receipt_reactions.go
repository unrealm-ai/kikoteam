package channel

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

const reactionEndpoint = "/api/internal/v1/channel-reactions"

type reactionClaim struct {
	InboxID string `json:"inbox_id"`
	Token   string `json:"claim_token"`
}

type reactionTarget struct {
	BindingID  string `json:"binding_id"`
	Provider   string `json:"provider"`
	Generation int64  `json:"connection_generation"`
	TenantID   string `json:"tenant_id"`
	BotID      string `json:"bot_id"`
	ChatID     string `json:"chat_id"`
	MessageID  string `json:"message_id"`
}

func reactionMatches(b Binding, t reactionTarget) bool {
	return b.Enabled && b.ID == t.BindingID && b.Generation == t.Generation && b.Provider == t.Provider &&
		b.TenantID != "" && b.TenantID == t.TenantID && b.BotOpenID != "" && b.BotOpenID == t.BotID
}

func (b *Backend) authorizeReaction(ctx context.Context, c reactionClaim) (*reactionTarget, error) {
	var out struct {
		Target *reactionTarget `json:"target"`
	}
	err := b.request(ctx, http.MethodPost, reactionEndpoint+"/"+url.PathEscape(c.InboxID)+"/authorize", map[string]string{"claim_token": c.Token}, &out)
	return out.Target, err
}

func (b *Backend) processReaction(ctx context.Context, current func(string) (Binding, bool), send func(context.Context, Binding, reactionTarget, func() bool) error) (bool, error) {
	var out struct {
		Claim *reactionClaim `json:"claim"`
	}
	if err := b.request(ctx, http.MethodPost, reactionEndpoint+"/claim", nil, &out); err != nil || out.Claim == nil {
		var status *BackendError
		if errors.As(err, &status) && status.Status == http.StatusNotFound {
			return false, nil // Older Manager during a rolling upgrade.
		}
		return false, err
	}
	t, err := b.authorizeReaction(ctx, *out.Claim)
	if err != nil || t == nil {
		return true, err
	}
	binding, ok := current(t.BindingID)
	if !ok || !reactionMatches(binding, *t) {
		return true, nil
	}
	check := func() bool {
		// Refresh the same Manager proof immediately before a provider write,
		// rather than relying on the earlier queue claim or token acquisition.
		v, e := b.authorizeReaction(ctx, *out.Claim)
		if e != nil || v == nil || *v != *t || ctx.Err() != nil {
			return false
		}
		c, ok := current(t.BindingID)
		return ok && reactionMatches(c, *t) && c.Credentials == binding.Credentials
	}
	return true, send(ctx, binding, *t, check)
}

func (s *Supervisor) runReceiptReactions(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		for i := 0; i < 8 && ctx.Err() == nil; i++ {
			work, cancel := context.WithTimeout(ctx, 12*time.Second)
			more, err := s.Backend.processReaction(work, s.Current, sendReceiptReaction)
			cancel()
			if err != nil && ctx.Err() == nil {
				// No raw provider errors or request content here.
				s.warn("channel.receipt_reaction_unavailable")
			}
			if !more {
				break
			}
		}
	}
}

func sendReceiptReaction(ctx context.Context, b Binding, t reactionTarget, allowed func() bool) error {
	switch t.Provider {
	case "feishu":
		api := lark.NewClient(b.Credentials.AppID, b.Credentials.AppSecret, lark.WithLogger(quietLogger{}), lark.WithReqTimeout(8*time.Second))
		return sendFeishuReceipt(ctx, api, t, allowed)
	case "dingtalk":
		return sendDingReceipt(ctx, newDingHTTP(), b, t, allowed)
	default:
		return nil
	}
}

func sendFeishuReceipt(ctx context.Context, api *lark.Client, t reactionTarget, allowed func() bool) error {
	if !dingID(t.MessageID) || !strings.HasPrefix(t.MessageID, "om_") || strings.ContainsAny(t.MessageID, "%/\\?#") || !allowed() {
		return nil
	}
	body := larkim.NewCreateMessageReactionReqBodyBuilder().ReactionType(larkim.NewEmojiBuilder().EmojiType("OK").Build()).Build()
	req := larkim.NewCreateMessageReactionReqBuilder().MessageId(t.MessageID).Body(body).Build()
	resp, err := api.Im.MessageReaction.Create(ctx, req)
	if err != nil || resp == nil || !resp.Success() {
		return errors.New("feishu reaction unavailable")
	}
	return nil
}

func sendDingReceipt(ctx context.Context, h dingHTTP, b Binding, t reactionTarget, allowed func() bool) error {
	if !dingID(t.ChatID) || !dingID(t.MessageID) || !dingRobotID(t.BotID) {
		return nil
	}
	token, err := h.token(ctx, b)
	if err != nil {
		return err
	}
	if !allowed() {
		return nil
	}
	const received = "已收到"
	var out struct {
		Success *bool           `json:"success"`
		Code    json.RawMessage `json:"code"`
	}
	err = h.request(ctx, http.MethodPost, "/v1.0/robot/emotion/reply", token, map[string]any{
		"robotCode": t.BotID, "openConversationId": t.ChatID, "openMsgId": t.MessageID,
		"emotionType": 2, "emotionName": received,
		"textEmotion": map[string]string{"emotionId": "2659900", "emotionName": received, "text": received, "backgroundId": "im_bg_1"},
	}, &out)
	if err == nil && (out.Success == nil || !*out.Success || len(out.Code) != 0) {
		err = errors.New("dingtalk reaction unavailable")
	}
	return err
}
