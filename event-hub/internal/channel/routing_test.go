package channel

import (
	"encoding/json"
	"testing"

	larkevent "github.com/larksuite/oapi-sdk-go/v3/event"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestEmployeeScopedGroupAndThreadRouting(t *testing.T) {
	for _, tc := range []struct {
		name, chat, thread, text, mention, senderType string
		a, b                                          bool
	}{
		{"ordinary group", "group", "", "hello", "", "user", false, false},
		{"mention A", "group", "", "hello", "bot-a", "user", true, false},
		{"mention B in topic", "group", "topic", "hello", "bot-b", "user", false, true},
		{"keyword A in topic", "group", "topic", "alpha", "", "user", true, false},
		{"keyword B", "group", "", "beta", "", "user", false, true},
		{"textual mention is not identity", "group", "topic", "@bot-b", "", "user", false, false},
		{"direct", "p2p", "", "hello", "", "user", true, true},
		{"direct topic", "p2p", "topic", "hello", "", "user", true, true},
		{"bot reminder not an inbound trigger", "group", "", "reminder", "", "app", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for i, binding := range []Binding{
				{ID: "a", BotOpenID: "bot-a", Trigger: Trigger{Mention: true, Keywords: []string{"alpha"}}},
				{ID: "b", BotOpenID: "bot-b", Trigger: Trigger{Mention: true, Keywords: []string{"beta"}}},
			} {
				binding.TenantID = "tenant"
				binding.Credentials.AppID = "app-" + binding.ID
				f, err := Compile(binding.Trigger)
				if err != nil {
					t.Fatal(err)
				}
				content, err := json.Marshal(map[string]string{"text": tc.text})
				if err != nil {
					t.Fatal(err)
				}
				event := &larkim.P2MessageReceiveV1{
					EventV2Base: &larkevent.EventV2Base{Header: &larkevent.EventHeader{EventID: "event", AppID: binding.Credentials.AppID, TenantKey: "tenant"}},
					Event: &larkim.P2MessageReceiveV1Data{
						Sender:  &larkim.EventSender{SenderId: &larkim.UserId{OpenId: ptr("sender")}, SenderType: ptr(tc.senderType)},
						Message: &larkim.EventMessage{MessageId: ptr("message"), ParentId: ptr("trusted-parent"), ChatId: ptr("chat"), ChatType: ptr(tc.chat), ThreadId: ptr(tc.thread), MessageType: ptr("text"), Content: ptr(string(content))},
					},
				}
				if tc.mention != "" {
					event.Event.Message.Mentions = []*larkim.MentionEvent{{Id: &larkim.UserId{OpenId: ptr(tc.mention)}}}
				}
				got, matched, err := normalize(binding, f, event)
				want := tc.a
				if i == 1 {
					want = tc.b
				}
				if err != nil || matched != want {
					t.Fatalf("employee %s: matched=%v want=%v err=%v", binding.ID, matched, want, err)
				}
				wantQuote := ""
				if tc.chat == "p2p" && tc.thread == "" {
					wantQuote = "trusted-parent"
				}
				if got.ReplyMessageID != wantQuote {
					t.Fatalf("quote escaped the main private conversation: %+v", got)
				}
				if got.ThreadID != tc.thread || got.BindingID != binding.ID {
					t.Fatalf("lost routing identity: %+v", got)
				}
			}
		})
	}
}
