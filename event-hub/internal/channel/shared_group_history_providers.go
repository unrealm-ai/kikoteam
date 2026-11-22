package channel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/unrealm-ai/kikoteam/event-hub/internal/channelhistory"
	"github.com/unrealm-ai/kikoteam/event-hub/internal/feishucontent"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

func fetchSharedFeishuHistory(ctx context.Context, b Binding, s sharedHistorySource, j sharedHistoryJob, gate func(context.Context) error) (sharedHistoryPage, error) {
	api := lark.NewClient(b.Credentials.AppID, b.Credentials.AppSecret, lark.WithLogger(quietLogger{}), lark.WithReqTimeout(10*time.Second))
	return readSharedFeishuPage(ctx, s, j, gate, func(call context.Context, path string) ([]byte, error) {
		r, e := api.Get(call, path, nil, larkcore.AccessTokenTypeTenant)
		if e != nil {
			return nil, errors.New("group_history_provider_unavailable")
		}
		return r.RawBody, nil
	})
}
func readSharedFeishuPage(ctx context.Context, s sharedHistorySource, j sharedHistoryJob, gate func(context.Context) error, get func(context.Context, string) ([]byte, error)) (sharedHistoryPage, error) {
	out := sharedHistoryPage{Items: []sharedHistoryItem{}, Coverage: "authorized_query"}
	if e := gate(ctx); e != nil {
		return out, e
	}
	q := url.Values{"container_id_type": {"chat"}, "container_id": {s.ExternalID}, "start_time": {strconv.FormatInt(j.StartMS/1000, 10)}, "end_time": {strconv.FormatInt((j.EndMS+999)/1000, 10)}, "sort_type": {"ByCreateTimeDesc"}, "page_size": {strconv.Itoa(j.Limit)}}
	if j.Cursor != "" {
		q.Set("page_token", j.Cursor)
	}
	raw, e := get(ctx, "/open-apis/im/v1/messages?"+q.Encode())
	if e != nil {
		return out, e
	}
	if len(raw) > 1<<20 {
		return out, errors.New("group_history_page_too_large")
	}
	var page struct {
		Code int `json:"code"`
		Data struct {
			Items []struct {
				Mentions []Mention `json:"mentions"`
				ID       string    `json:"message_id"`
				Chat     string    `json:"chat_id"`
				Thread   string    `json:"thread_id"`
				Created  string    `json:"create_time"`
				Type     string    `json:"msg_type"`
				Deleted  bool      `json:"deleted"`
				Sender   struct {
					ID   string `json:"id"`
					Type string `json:"sender_type"`
					Name string `json:"sender_name"`
				} `json:"sender"`
				Body struct {
					Content string `json:"content"`
				} `json:"body"`
			} `json:"items"`
			More  *bool  `json:"has_more"`
			Token string `json:"page_token"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &page) != nil || page.Code != 0 || page.Data.More == nil || len(page.Data.Items) > j.Limit {
		return out, errors.New("group_history_page_invalid")
	}
	for _, m := range page.Data.Items {
		if m.Chat != s.ExternalID || m.ID == "" {
			return out, errors.New("group_history_scope_mismatch")
		}
		if m.Thread != "" {
			continue
		}
		at, e := strconv.ParseInt(m.Created, 10, 64)
		if e != nil {
			return out, e
		}
		if at < j.StartMS || at >= j.EndMS {
			continue
		}
		text := "[不支持的消息类型]"
		if m.Deleted {
			text = "[消息已撤回]"
		} else if m.Type == "text" || m.Type == "post" {
			var err error
			text, err = feishucontent.RenderWithMentions(m.Type, m.Body.Content, feishuMentionTokens(m.Mentions, false))
			if err != nil {
				return out, err
			}
		}
		out.Items = append(out.Items, sharedHistoryItem{ID: m.ID, MessageID: m.ID, SenderID: m.Sender.ID, SenderName: m.Sender.Name, SenderType: m.Sender.Type, CreatedAtMS: at, Text: text, MessageType: m.Type})
	}
	if *page.Data.More {
		if page.Data.Token == "" || page.Data.Token == j.Cursor || len(page.Data.Token) > 8192 {
			return out, errors.New("group_history_cursor_invalid")
		}
		out.Next = page.Data.Token
	}
	return out, gate(ctx)
}
func fetchSharedWecomHistory(ctx context.Context, b Binding, s sharedHistorySource, j sharedHistoryJob, gate func(context.Context) error) (sharedHistoryPage, error) {
	h := wecomHistoryHTTP{client: &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, origin: wecomAPIOrigin, gate: func(c context.Context, _ channelhistory.Job) error { return gate(c) }}
	out := sharedHistoryPage{Items: []sharedHistoryItem{}, Coverage: "recent_query_local_ids"}
	if j.StartMS < time.Now().UnixMilli()-604800000 {
		return out, errors.New("wecom_history_range_expired")
	}
	token, e := h.connect(ctx, b, "messages")
	if e != nil {
		return out, e
	}
	payload := map[string]any{"chat_id": s.ExternalID, "begin_time": wecomHistoryTime(j.StartMS / 1000 * 1000), "end_time": wecomHistoryTime((j.EndMS + 999) / 1000 * 1000)}
	if j.Cursor != "" {
		payload["cursor"] = j.Cursor
	}
	var page wecomHistoryPage
	if e = h.call(ctx, "/cli/chat/messages/list", payload, token, &page); e != nil {
		return out, e
	}
	if page.HasMore == nil || page.Messages == nil || len(page.Messages) > 10000 {
		return out, errors.New("wecom_history_page_invalid")
	}
	for i := len(page.Messages) - 1; i >= 0; i-- {
		m := page.Messages[i]
		if !validWecomID(m.UserID) || len(m.Name) > 256 || m.Type == "" || len(m.Type) > 64 {
			return out, errors.New("wecom_history_record_invalid")
		}
		at, e := parseWecomHistoryTime(m.Time)
		if e != nil {
			return out, e
		}
		if at < j.StartMS || at >= j.EndMS {
			continue
		}
		text := m.Text.Content
		if m.Type != "text" {
			text = "[非文本消息，未读取附件内容]"
		}
		hash := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d", j.ID, j.Cursor, i)))
		out.Items = append(out.Items, sharedHistoryItem{ID: "query-record-" + hex.EncodeToString(hash[:]), SenderID: m.UserID, SenderName: m.Name, SenderType: "unknown", CreatedAtMS: at, Text: text, MessageType: m.Type})
	}
	if len(out.Items) > 500 {
		return out, errors.New("wecom_history_page_budget_exceeded")
	}
	if *page.HasMore {
		if page.Cursor == "" || page.Cursor == j.Cursor || len(page.Cursor) > 8192 {
			return out, errors.New("wecom_history_cursor_invalid")
		}
		out.Next = page.Cursor
	}
	return out, gate(ctx)
}
