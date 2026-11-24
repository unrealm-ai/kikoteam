package channelhistory

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestNotificationAnchorExactProviderEvidence(t *testing.T) {
	j := Job{TriggerID: "om_notice", ChatID: "oc_group", ThreadID: "", TenantID: "tenant", TriggerText: "[系统消息]result"}
	base := wireMessage{ID: j.TriggerID, ChatID: j.ChatID, Created: "1760000000000", Type: "text"}
	base.Sender.Type = "app"
	base.Sender.ID = "cli_app"
	base.Body.Content = `{"text":"[系统消息]result"}`
	for _, mode := range []string{"valid", "chat", "thread", "sender", "time", "body", "deleted", "gate"} {
		t.Run(mode, func(t *testing.T) {
			m := base
			calls := 0
			switch mode {
			case "chat":
				m.ChatID = "other"
			case "thread":
				m.ThreadID = "other"
			case "sender":
				m.Sender.ID = "other"
			case "time":
				m.Created = "0"
			case "body":
				m.Body.Content = `{"text":"other"}`
			case "deleted":
				m.Deleted = true
			}
			r := Reader{Authorize: func(context.Context, Job) error {
				if mode == "gate" {
					return errors.New("revoked")
				}
				return nil
			}, Get: func(_ context.Context, p string) ([]byte, error) {
				calls++
				if !strings.HasSuffix(p, "/om_notice") {
					t.Fatal(p)
				}
				return json.Marshal(map[string]any{"code": 0, "data": map[string]any{"items": []wireMessage{m}}})
			}}
			at, err := r.ResolveNotificationAnchor(context.Background(), j, "cli_app")
			if mode == "valid" {
				if err != nil || at != 1760000000000 {
					t.Fatal(at, err)
				}
			} else if err == nil {
				t.Fatal("accepted wrong provider scope")
			}
			if mode == "gate" && calls != 0 {
				t.Fatal("unauthorized network call")
			}
		})
	}
}

func TestNotificationResolvedAnchorFeedsBoundedHistory(t *testing.T) {
	j := job()
	j.TriggerTimeMS = 0
	j.TriggerFormat = "markdown"
	j.TriggerText = "**result**\n\n- one"
	anchor := message("anchor", "10000")
	anchor.Sender.Type = "app"
	anchor.Sender.ID = "cli_app"
	anchor.Type = "post"
	anchor.Body.Content = `{"content":[[{"tag":"text","text":"result one"}]],"content_v2":[[{"tag":"md","text":"**result**\n\n- one"}]]}`
	calls := 0
	r := Reader{Authorize: allow, Get: func(_ context.Context, path string) ([]byte, error) {
		calls++
		if strings.HasSuffix(path, "/anchor") {
			return page([]wireMessage{anchor}, false, ""), nil
		}
		if strings.Contains(path, "page_token=") {
			return page([]wireMessage{message("b", "9000")}, false, ""), nil
		}
		return page([]wireMessage{message("later", "10000"), anchor, message("a", "9999")}, true, "next"), nil
	}}
	at, err := r.ResolveNotificationAnchor(context.Background(), j, "cli_app")
	if err != nil {
		t.Fatal(err)
	}
	j.TriggerTimeMS = at
	result, err := r.Fetch(context.Background(), j)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 || len(result.Messages) != 2 || result.Messages[0].ID != "b" || result.Messages[1].ID != "a" {
		t.Fatal(calls, result)
	}
}

func TestNotificationAnchorRejectsDifferentFormatOrBody(t *testing.T) {
	j := Job{TriggerFormat: "markdown", TriggerText: "**ok**"}
	for _, tc := range []struct{ kind, raw string }{
		{"text", `{"text":"**ok**"}`},
		{"post", `{"content_v2":[[{"tag":"md","text":"ok"}]]}`},
		{"post", `{"content_v2":[[{"tag":"text","text":"**ok**"}]]}`},
		{"post", `{"content_v2":[[{"tag":"md","text":"**ok**"},{"tag":"img"}]]}`},
		{"post", `{"content":[[{"tag":"md","text":"**ok**"}]],"content_v2":[]}`},
	} {
		m := wireMessage{Type: tc.kind}
		m.Body.Content = tc.raw
		if matchesNotificationBody(m, j) {
			t.Fatalf("accepted %s %s", tc.kind, tc.raw)
		}
	}
	m := wireMessage{Type: "post"}
	m.Body.Content = `{"content_v2":[[{"tag":"md","text":"**ok**"}]]}`
	j.TriggerFormat = "text"
	if matchesNotificationBody(m, j) {
		t.Fatal("used a post as evidence for a plain-text notice")
	}
}

func TestNotificationTaskDetailAnchorBindsProviderScopeWithoutReadingCard(t *testing.T) {
	const bearer = "http://192.168.1.55:8080/tasks/fixture_example_not_a_token_1234567890"
	original := "[系统消息]本次执行已结束\n**合成结果**"
	j := Job{TriggerID: "message", ChatID: "group", ThreadID: "topic", TenantID: "tenant", TriggerText: original, TriggerTimeMS: 1760000000000}
	for _, tc := range []struct {
		name, format, kind, body, sender, thread, timestamp string
		want                                                bool
	}{
		{"interactive opaque content", "task_detail_card", "interactive", "opaque data unsuited for decoding", "app", "topic", "1760000000000", true},
		{"interactive wrong type", "task_detail_card", "post", "same type is required", "app", "topic", "1760000000000", false},
		{"interactive wrong source", "task_detail_card", "interactive", "opaque", "other-app", "topic", "1760000000000", false},
		{"interactive wrong thread", "task_detail_card", "interactive", "opaque", "app", "other-topic", "1760000000000", false},
		{"interactive wrong provider time", "task_detail_card", "interactive", "opaque", "app", "topic", "1760000000001", false},
		{"link same body", "task_detail_link", "post", original + "\n\n[查看任务详情](" + bearer + ")", "app", "topic", "1760000000000", true},
		{"link added text", "task_detail_link", "post", original + "extra\n\n[查看任务详情](" + bearer + ")", "app", "topic", "1760000000000", false},
		{"link missing", "task_detail_link", "post", original, "app", "topic", "1760000000000", false},
		{"link invalid", "task_detail_link", "post", original + "\n\n[查看任务详情](file:///tasks/fixture_example_not_a_token_1234567890)", "app", "topic", "1760000000000", false},
		{"link doubled", "task_detail_link", "post", original + "\n\n[查看任务详情](" + bearer + ")\n\n[查看任务详情](" + bearer + ")", "app", "topic", "1760000000000", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			job := j
			job.TriggerFormat = tc.format
			m := wireMessage{ID: j.TriggerID, ChatID: j.ChatID, ThreadID: tc.thread, Created: tc.timestamp, Type: tc.kind}
			m.Sender.Type, m.Sender.ID, m.Sender.TenantKey = "app", tc.sender, j.TenantID
			m.Body.Content = tc.body
			if tc.kind == "post" {
				body, err := json.Marshal(map[string]any{"content_v2": [][]map[string]string{{{"tag": "md", "text": tc.body}}}})
				if err != nil {
					t.Fatal(err)
				}
				m.Body.Content = string(body)
			}
			r := Reader{Authorize: allow, Get: func(_ context.Context, p string) ([]byte, error) {
				if p != "/open-apis/im/v1/messages/message" {
					t.Fatal("queried wrong message", p)
				}
				return page([]wireMessage{m}, false, ""), nil
			}}
			at, err := r.ResolveNotificationAnchor(context.Background(), job, "app")
			if (err == nil) != tc.want || tc.want && at != j.TriggerTimeMS {
				t.Fatal("wrong anchor decision", at, err)
			}
			if err != nil && strings.Contains(err.Error(), bearer) {
				t.Fatal("anchor failure exposed bearer")
			}
		})
	}
}
