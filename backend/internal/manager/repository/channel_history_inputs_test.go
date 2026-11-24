package repository

import (
	"strings"
	"testing"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func TestHistoryMessageLabels(t *testing.T) {
	for _, tc := range []struct{ kind, text, want string }{
		{"user", "hello", "[说话人=未知用户]hello"},
		{"app", "reminder", "[系统消息]reminder"},
		{"app", "[系统消息]reminder", "[系统消息]reminder"},
	} {
		got := channelHistoryInputText(service.ChannelHistoryItem{SenderID: "real-user", SenderType: tc.kind, Text: tc.text})
		if got != tc.want {
			t.Fatalf("got %q want %q", got, tc.want)
		}
	}
}

func TestChannelHistoryRedactsTaskDetailBearerAcrossOrigins(t *testing.T) {
	const token = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ"
	for _, origin := range []string{"https://detail.example.test", "http://192.0.2.1:8080", "http://[2001:db8::1]:8080"} {
		raw := "查看 [" + origin + "/tasks/" + token + "] 后继续"
		item := service.ChannelHistoryItem{Text: raw, SenderType: "user"}
		if got := channelHistoryInputText(item); strings.Contains(got, token) || !strings.Contains(got, "[任务详情链接]") {
			t.Fatal("channel context did not redact capability")
		}
		page := []service.SharedHistoryItem{{Text: raw}}
		redactSharedHistoryTaskLinks(page)
		if strings.Contains(page[0].Text, token) {
			t.Fatal("shared group history retained capability")
		}
	}
}
