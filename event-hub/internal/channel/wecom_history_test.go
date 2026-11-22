package channel

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/unrealm-ai/kikoteam/event-hub/internal/channelhistory"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func writeWecomCLI(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := json.Marshal(map[string]string{"result": string(raw)})
	if err != nil {
		t.Fatal(err)
	}
	if err = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "results_json": string(wrapped)}); err != nil {
		t.Error(err)
	}
}
func TestWecomRecentHistoryUniqueAnchorAndNoInventedIDs(t *testing.T) {
	for _, mode := range []string{"valid", "ambiguous", "missing", "foreign_discovery", "revoked", "same_text_records", "nontext"} {
		t.Run(mode, func(t *testing.T) {
			now := time.UnixMilli(1700000000000)
			j := channelhistory.Job{ID: "job", BindingID: "binding", Generation: 1, Revision: 1, TenantID: "bot", ChatID: "group", Mode: "wecom_recent", TriggerText: "@助手 总结一下", TriggerTimeMS: now.UnixMilli(), Limit: 100}
			b := Binding{ID: "binding", Provider: "wecom", Enabled: true, TenantID: "bot", Credentials: Credentials{AppID: "bot", AppSecret: "secret"}}
			requests := 0
			pages := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				switch r.URL.Path {
				case "/cgi-bin/aibot/cli/get_cli_config":
					if err := json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "token": "synthetic"}); err != nil {
						t.Error(err)
					}
				case "/cli/service/discovery":
					base := wecomAPIOrigin + "/cli"
					if mode == "foreign_discovery" {
						base = "https://attacker.invalid/cli"
					}
					writeWecomCLI(t, w, map[string]any{"base_url": base, "resources": map[string]any{"messages": map[string]any{"methods": map[string]any{"list": map[string]string{"http_method": "POST", "path": "/chat/messages/list"}}}}})
				case "/cli/chat/messages/list":
					pages++
					var envelope struct {
						Payload string `json:"payload"`
					}
					if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
						t.Error(err)
					}
					var request map[string]any
					if err := json.Unmarshal([]byte(envelope.Payload), &request); err != nil {
						t.Error(err)
					}
					if request["chat_id"] != "group" {
						t.Error("wrong group")
					}
					record := func(text string, offset int64) map[string]any {
						return map[string]any{"userid": "history-only-id", "user_name": "历史成员", "send_time": wecomHistoryTime(now.UnixMilli() + offset), "msg_type": "text", "text": map[string]string{"content": text}}
					}
					rows := []map[string]any{record("@助手 之前的问题", -30000), record("第一句", -20000), record("第二句", -10000), record(j.TriggerText, -2000)}
					if mode == "ambiguous" {
						rows = append(rows, record(j.TriggerText, -1000))
					}
					if mode == "missing" {
						rows = rows[:3]
					}
					if mode == "same_text_records" {
						rows[2] = record("第一句", -10000)
					}
					if mode == "nontext" {
						rows[2]["msg_type"] = "image"
					}
					writeWecomCLI(t, w, map[string]any{"messages": rows, "has_more": false})
				default:
					t.Error("unexpected request", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			gate := func(context.Context, channelhistory.Job) error {
				if mode == "revoked" && pages > 0 {
					return errors.New("revoked")
				}
				return nil
			}
			out, err := fetchWecomHistoryWithHTTP(context.Background(), b, j, gate, server.Client(), server.URL, now)
			fail := mode == "ambiguous" || mode == "missing" || mode == "foreign_discovery" || mode == "revoked"
			if (err != nil) != fail {
				t.Fatal(out, err)
			}
			if fail {
				return
			}
			if len(out.Messages) != 2 || out.Digest == "" || requests != 3 {
				t.Fatal(out, requests)
			}
			for _, m := range out.Messages {
				if m.ID != "" || m.RecordKey == "" || m.SenderType != "unknown" || m.SenderIDType != "" {
					t.Fatal("historical identity was fabricated", m)
				}
			}
			if mode == "same_text_records" && out.Messages[0].Text != out.Messages[1].Text {
				t.Fatal("duplicate text lost")
			}
		})
	}
}
func TestWecomHistoryAnchorAndBounds(t *testing.T) {
	j := channelhistory.Job{TriggerText: "@助手 处理", TriggerTimeMS: 100000, Limit: 2}
	rows := []channelhistory.Message{{Text: j.TriggerText, TimeMS: 100000}, {Text: "新", TimeMS: 99000}, {Text: "旧", TimeMS: 98000}, {Text: "@助手 上次", TimeMS: 90000}}
	out, done, err := selectWecomRecent(rows, j, "助手", true)
	if err != nil || !done || len(out) != 2 || out[0].Text != "旧" {
		t.Fatal(out, done, err)
	}
	rows[0].TimeMS = 94000
	if _, done, err = selectWecomRecent(rows, j, "助手", true); done || err != nil {
		t.Fatal("outside five second anchor accepted")
	}
	for _, text := range []string{"普通文本", "@助手 @他人 处理", " @助手 处理"} {
		if wecomMentionName(text) != "" {
			t.Fatal(text)
		}
	}
	raw := "2026-01-02 03:04:05"
	ms, err := parseWecomHistoryTime(raw)
	if err != nil || wecomHistoryTime(ms) != raw || time.UnixMilli(ms).UTC().Hour() != 19 {
		t.Fatal(ms, err)
	}
}
func TestWecomRecentGroupsProviderCapability(t *testing.T) {
	now := time.UnixMilli(1700000000000)
	b := Binding{Provider: "wecom", Enabled: true, TenantID: "bot", Credentials: Credentials{AppID: "bot", AppSecret: "secret"}}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/aibot/cli/get_cli_config":
			if err := json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "token": "synthetic"}); err != nil {
				t.Error(err)
			}
		case "/cli/service/discovery":
			writeWecomCLI(t, w, map[string]any{"resources": map[string]any{"groups": map[string]any{"methods": map[string]any{"list": map[string]string{"http_method": "POST", "path": "/chat/groups/list"}}}}})
		case "/cli/chat/groups/list":
			writeWecomCLI(t, w, map[string]any{"chats": []map[string]string{{"chat_id": "group", "chat_name": "最近群"}}, "has_more": true, "next_cursor": "next"})
		default:
			t.Error(r.URL.Path)
		}
	}))
	defer server.Close()
	out, err := listWecomRecentGroups(context.Background(), b, nil, func(context.Context) error { calls++; return nil }, server.Client(), server.URL, now)
	if err != nil || len(out.Items) != 1 || out.Items[0].ID != "group" || out.Next == nil || out.Next.Cursor != "next" || calls != 4 {
		t.Fatal(out, err, calls)
	}
	if _, err = listWecomRecentGroups(context.Background(), b, nil, nil, server.Client(), server.URL, now); err == nil {
		t.Fatal("missing authorization accepted")
	}
}
