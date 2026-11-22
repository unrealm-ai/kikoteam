package repository

import (
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"testing"
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
