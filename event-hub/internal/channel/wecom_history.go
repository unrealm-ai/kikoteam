package channel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/unrealm-ai/kikoteam/event-hub/internal/channelhistory"
)

const wecomAPIOrigin = "https://qyapi.weixin.qq.com"

type wecomHistoryRecord struct {
	UserID string `json:"userid"`
	Name   string `json:"user_name"`
	Time   string `json:"send_time"`
	Type   string `json:"msg_type"`
	Text   struct {
		Content string `json:"content"`
	} `json:"text"`
}
type wecomHistoryPage struct {
	Messages []wecomHistoryRecord `json:"messages"`
	HasMore  *bool                `json:"has_more"`
	Cursor   string               `json:"next_cursor"`
}
type wecomHistoryHTTP struct {
	client *http.Client
	origin string
	gate   channelhistory.Gate
	job    channelhistory.Job
}

func (h wecomHistoryHTTP) post(ctx context.Context, path string, payload any, token string) ([]byte, error) {
	if h.gate == nil {
		return nil, errors.New("wecom_history_authorization_missing")
	}
	if err := h.gate(ctx, h.job); err != nil {
		return nil, err
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", h.origin+path, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, errors.New("wecom_history_unavailable")
	}
	defer func() { _ = resp.Body.Close() }() //nolint:errcheck // Read errors are handled below.
	if resp.StatusCode != 200 {
		return nil, errors.New("wecom_history_unavailable")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil || len(raw) > 4<<20 {
		return nil, errors.New("wecom_history_invalid_response")
	}
	return raw, nil
}
func (h wecomHistoryHTTP) call(ctx context.Context, path string, payload any, token string, out any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	raw, err := h.post(ctx, path, map[string]string{"payload": string(data)}, token)
	if err != nil {
		return err
	}
	var envelope struct {
		Code    *int   `json:"errcode"`
		Results string `json:"results_json"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Code == nil || *envelope.Code != 0 {
		return errors.New("wecom_history_provider_error")
	}
	var result struct {
		Result string `json:"result"`
		Error  *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(envelope.Results), &result) != nil || (result.Error != nil && result.Error.Code != 0) || json.Unmarshal([]byte(result.Result), out) != nil {
		return errors.New("wecom_history_invalid_result")
	}
	return nil
}
func (h wecomHistoryHTTP) connect(ctx context.Context, b Binding, resource string) (string, error) {
	now := time.Now().Unix()
	nonce := wireID("cli_")
	hash := sha256.Sum256([]byte(fmt.Sprintf("%s%s%d%s", b.Credentials.AppSecret, b.Credentials.AppID, now, nonce)))
	raw, err := h.post(ctx, "/cgi-bin/aibot/cli/get_cli_config", map[string]any{"bot_id": b.Credentials.AppID, "time": now, "nonce": nonce, "signature": hex.EncodeToString(hash[:]), "bind_source": 1}, "")
	if err != nil {
		return "", err
	}
	var auth struct {
		Code  *int   `json:"errcode"`
		Token string `json:"token"`
	}
	if json.Unmarshal(raw, &auth) != nil || auth.Code == nil || *auth.Code != 0 || auth.Token == "" || len(auth.Token) > 8192 || strings.ContainsAny(auth.Token, "\x00\r\n\t ") {
		return "", errors.New("wecom_history_authentication_failed")
	}
	var discovery struct {
		Base      string `json:"base_url"`
		Resources map[string]struct {
			Methods map[string]struct {
				Method string `json:"http_method"`
				Path   string `json:"path"`
			} `json:"methods"`
		} `json:"resources"`
	}
	if err = h.call(ctx, "/cli/service/discovery", map[string]string{"service": "chat"}, auth.Token, &discovery); err != nil {
		return "", err
	}
	method := discovery.Resources[resource].Methods["list"]
	if (discovery.Base != "" && discovery.Base != wecomAPIOrigin+"/cli") || method.Method != "POST" || method.Path != "/chat/"+resource+"/list" {
		return "", errors.New("wecom_history_discovery_mismatch")
	}
	return auth.Token, nil
}

var wecomBeijing = time.FixedZone("UTC+08:00", 8*3600)

func wecomHistoryTime(at int64) string {
	return time.UnixMilli(at).In(wecomBeijing).Format("2006-01-02 15:04:05")
}
func parseWecomHistoryTime(raw string) (int64, error) {
	t, err := time.ParseInLocation("2006-01-02 15:04:05", raw, wecomBeijing)
	if err != nil || t.Format("2006-01-02 15:04:05") != raw {
		return 0, errors.New("wecom_history_invalid_time")
	}
	return t.UnixMilli(), nil
}

func wecomMentionName(text string) string {
	if !strings.HasPrefix(text, "@") || strings.Count(text, "@") != 1 {
		return ""
	}
	name := text[1:]
	for i, r := range name {
		if unicode.IsSpace(r) {
			name = name[:i]
			break
		}
		if r < 32 {
			return ""
		}
	}
	if name == "" || utf8.RuneCountInString(name) > 100 {
		return ""
	}
	return name
}
func wecomMentions(text, name string) bool {
	for _, token := range strings.Fields(text) {
		if token == "@"+name {
			return true
		}
	}
	return false
}
func fetchWecomHistory(ctx context.Context, b Binding, j channelhistory.Job, gate channelhistory.Gate) (channelhistory.Snapshot, error) {
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return fetchWecomHistoryWithHTTP(ctx, b, j, gate, client, wecomAPIOrigin, time.Now())
}
func fetchWecomHistoryWithHTTP(ctx context.Context, b Binding, j channelhistory.Job, gate channelhistory.Gate, client *http.Client, origin string, now time.Time) (channelhistory.Snapshot, error) {
	empty := channelhistory.Snapshot{JobID: j.ID, TriggerID: j.TriggerID, Messages: []channelhistory.Message{}}
	name := wecomMentionName(j.TriggerText)
	if !validWecomHistoryJob(b, j, name) {
		return empty, errors.New("wecom_recent_context_unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	h := wecomHistoryHTTP{client: client, origin: origin, gate: gate, job: j}
	token, err := h.connect(ctx, b, "messages")
	if err != nil {
		return empty, err
	}
	end := (j.TriggerTimeMS + 999) / 1000 * 1000
	start := end - 7*86400000 + 60000
	minimum := (now.UnixMilli()+999)/1000*1000 - 7*86400000 + 60000
	if minimum > start {
		start = minimum
	}
	if start >= end {
		return empty, errors.New("wecom_recent_context_out_of_range")
	}
	var cursor string
	seen := map[string]bool{}
	descending := []channelhistory.Message{}
	for pageNumber := 0; pageNumber < 8; pageNumber++ {
		var page wecomHistoryPage
		payload := map[string]any{"chat_id": j.ChatID, "begin_time": wecomHistoryTime(start), "end_time": wecomHistoryTime(end)}
		if cursor != "" {
			payload["cursor"] = cursor
		}
		if err = h.call(ctx, "/cli/chat/messages/list", payload, token, &page); err != nil {
			return empty, err
		}
		if page.HasMore == nil || page.Messages == nil || len(descending)+len(page.Messages) > 10000 || (*page.HasMore && (page.Cursor == "" || seen[page.Cursor])) || len(page.Cursor) > 8192 {
			return empty, errors.New("wecom_history_invalid_page")
		}
		// Provider pages are ascending; reverse each page before appending older pages.
		for index := len(page.Messages) - 1; index >= 0; index-- {
			record := page.Messages[index]
			at, e := parseWecomHistoryTime(record.Time)
			if e != nil || at < start || at > end || !validWecomID(record.UserID) || record.Type == "" || len(record.Type) > 64 || len(record.Name) > 256 {
				return empty, errors.New("wecom_history_invalid_record")
			}
			text := record.Text.Content
			if record.Type != "text" {
				text = "[非文本消息，未读取附件内容]"
			}
			descending = append(descending, channelhistory.Message{RecordKey: fmt.Sprintf("page:%d:record:%d", pageNumber, index), SenderID: record.UserID, SenderName: record.Name, SenderType: "unknown", TimeMS: at, Type: record.Type, Text: text})
		}
		selected, done, e := selectWecomRecent(descending, j, name, !*page.HasMore)
		if e != nil {
			return empty, e
		}
		if done {
			empty.Messages = selected
			raw, e := json.Marshal(selected)
			if e != nil {
				return empty, e
			}
			hash := sha256.Sum256(raw)
			empty.Digest = hex.EncodeToString(hash[:])
			if err = gate(ctx, j); err != nil {
				return empty, err
			}
			return empty, nil
		}
		if !*page.HasMore {
			break
		}
		seen[page.Cursor] = true
		cursor = page.Cursor
	}
	return empty, errors.New("wecom_recent_anchor_unavailable")
}
func selectWecomRecent(rows []channelhistory.Message, j channelhistory.Job, name string, exhausted bool) ([]channelhistory.Message, bool, error) {
	anchor := -1
	covered := exhausted
	for i, row := range rows {
		delta := row.TimeMS - j.TriggerTimeMS
		if row.Text == j.TriggerText && wecomMentionName(row.Text) == name && delta >= -5000 && delta <= 5000 {
			if anchor >= 0 {
				return nil, false, errors.New("wecom_recent_anchor_ambiguous")
			}
			anchor = i
		}
		if row.TimeMS < j.TriggerTimeMS-5000 {
			covered = true
		}
	}
	if !covered || anchor < 0 {
		return nil, false, nil
	}
	before := rows[anchor+1:]
	boundary := -1
	for i, row := range before {
		if wecomMentions(row.Text, name) {
			boundary = i
			break
		}
	}
	if boundary < 0 && len(before) < j.Limit && !exhausted {
		return nil, false, nil
	}
	count := len(before)
	if boundary >= 0 && boundary < count {
		count = boundary
	}
	if count > j.Limit {
		count = j.Limit
	}
	out := make([]channelhistory.Message, 0, count)
	size, runes := 0, 0
	for i := count - 1; i >= 0; i-- {
		size += len(before[i].Text)
		runes += utf8.RuneCountInString(before[i].Text)
		if size > 128*1024 || runes > 32768 {
			return nil, false, errors.New("wecom_recent_context_too_large")
		}
		out = append(out, before[i])
	}
	return out, true, nil
}

// WecomGroupRange is retained across pages. This is a provider capability, not
// a public endpoint or authorization to discover arbitrary installations.
type WecomGroupRange struct {
	BeginMS, EndMS int64
	Cursor         string
}
type WecomRecentGroup struct {
	ID              string `json:"chat_id"`
	Name            string `json:"chat_name"`
	LastMessageTime string `json:"last_msg_time"`
}
type WecomRecentGroups struct {
	Items []WecomRecentGroup
	Next  *WecomGroupRange
}

func ListWecomRecentGroups(ctx context.Context, b Binding, range_ *WecomGroupRange, authorize func(context.Context) error) (WecomRecentGroups, error) {
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return listWecomRecentGroups(ctx, b, range_, authorize, client, wecomAPIOrigin, time.Now())
}
func listWecomRecentGroups(ctx context.Context, b Binding, range_ *WecomGroupRange, authorize func(context.Context) error, client *http.Client, origin string, now time.Time) (WecomRecentGroups, error) {
	out := WecomRecentGroups{Items: []WecomRecentGroup{}}
	if authorize == nil || !b.Enabled || b.Provider != "wecom" || b.TenantID != b.Credentials.AppID {
		return out, errors.New("wecom_group_scope_invalid")
	}
	r := WecomGroupRange{EndMS: now.UnixMilli() / 1000 * 1000}
	r.BeginMS = r.EndMS - 7*86400000 + 60000
	if range_ != nil {
		r = *range_
	}
	if r.BeginMS < now.UnixMilli()-7*86400000 || r.EndMS > now.UnixMilli()+1000 || r.EndMS <= r.BeginMS || r.BeginMS%1000 != 0 || r.EndMS%1000 != 0 || len(r.Cursor) > 8192 || strings.ContainsAny(r.Cursor, "\x00\r\n") {
		return out, errors.New("wecom_group_range_invalid")
	}
	h := wecomHistoryHTTP{client: client, origin: origin, gate: func(call context.Context, _ channelhistory.Job) error { return authorize(call) }}
	token, err := h.connect(ctx, b, "groups")
	if err != nil {
		return out, err
	}
	payload := map[string]any{"begin_time": wecomHistoryTime(r.BeginMS), "end_time": wecomHistoryTime(r.EndMS)}
	if r.Cursor != "" {
		payload["cursor"] = r.Cursor
	}
	var page struct {
		Groups  []WecomRecentGroup `json:"chats"`
		HasMore *bool              `json:"has_more"`
		Cursor  string             `json:"next_cursor"`
	}
	if err = h.call(ctx, "/cli/chat/groups/list", payload, token, &page); err != nil {
		return out, err
	}
	if page.Groups == nil || page.HasMore == nil || len(page.Groups) > 10000 || len(page.Cursor) > 8192 || (*page.HasMore && (page.Cursor == "" || page.Cursor == r.Cursor)) {
		return out, errors.New("wecom_group_page_invalid")
	}
	for _, g := range page.Groups {
		if !validWecomID(g.ID) || len(g.Name) > 1024 {
			return out, errors.New("wecom_group_invalid")
		}
	}
	out.Items = page.Groups
	if *page.HasMore {
		r.Cursor = page.Cursor
		out.Next = &r
	}
	if err = authorize(ctx); err != nil {
		return WecomRecentGroups{}, err
	}
	return out, nil
}

func validWecomHistoryJob(b Binding, j channelhistory.Job, name string) bool {
	return b.Provider == "wecom" && j.Mode == "wecom_recent" && j.ThreadID == "" && j.ChatID != "" && j.TenantID == b.Credentials.AppID && name != "" && j.TriggerTimeMS > 0 && j.Limit >= 1 && j.Limit <= 100
}
