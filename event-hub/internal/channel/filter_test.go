package channel

import (
	larkevent "github.com/larksuite/oapi-sdk-go/v3/event"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"testing"
)

func TestFilterBoundaries(t *testing.T) {
	f, e := Compile(Trigger{Mention: true, Keywords: []string{"日报"}, Regexes: []string{`^report [0-9]+$`}})
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range []struct {
		chat, text    string
		mention, want bool
	}{{"p2p", "anything", false, true}, {"group", "@机器人", false, false}, {"group", "hi", true, true}, {"group", "生成日报", false, true}, {"group", "report 42", false, true}, {"group", "report x", false, false}} {
		got, e := f.Match(v.chat, v.text, v.mention)
		if e != nil || got != v.want {
			t.Fatalf("%+v got %v %v", v, got, e)
		}
	}
	if _, e = Compile(Trigger{Regexes: []string{"("}}); e == nil {
		t.Fatal("invalid regex accepted")
	}
	if _, e = f.Match("unknown", "", false); e == nil {
		t.Fatal("unknown chat type accepted")
	}
}
func ptr(s string) *string { return &s }
func TestNormalizeIdentityAndStructuredMention(t *testing.T) {
	b := Binding{ID: "b", TenantID: "tenant", BotOpenID: "bot", Credentials: Credentials{AppID: "app"}, Trigger: Trigger{Mention: true}}
	f, err := Compile(b.Trigger)
	if err != nil {
		t.Fatal(err)
	}
	e := &larkim.P2MessageReceiveV1{EventV2Base: &larkevent.EventV2Base{Header: &larkevent.EventHeader{EventID: "event", AppID: "app", TenantKey: "tenant"}}, Event: &larkim.P2MessageReceiveV1Data{Sender: &larkim.EventSender{SenderId: &larkim.UserId{OpenId: ptr("user")}, SenderType: ptr("user")}, Message: &larkim.EventMessage{MessageId: ptr("m"), ChatId: ptr("chat"), ChatType: ptr("group"), MessageType: ptr("text"), Content: ptr(`{"text":"@bot hi"}`)}}}
	_, matched, err := normalize(b, f, e)
	if err != nil || matched {
		t.Fatal("textual mention matched", err)
	}
	e.Event.Message.Mentions = []*larkim.MentionEvent{{Id: &larkim.UserId{OpenId: ptr("other")}}}
	_, matched, err = normalize(b, f, e)
	if err != nil || matched {
		t.Fatal("other mention matched", err)
	}
	e.Event.Message.Mentions[0].Id.OpenId = ptr("bot")
	e.Event.Message.Mentions[0].Name = ptr("机器人甲")
	e.Event.Message.Mentions[0].Key = ptr("@_user_1")
	e.Event.Message.Mentions[0].TenantKey = ptr("tenant")
	v, matched, err := normalize(b, f, e)
	if err != nil || !matched || !v.BotMentioned {
		t.Fatal("trusted bot mention missed", err)
	}
	if len(v.Mentions) != 1 || v.Mentions[0].Name != "机器人甲" || v.Mentions[0].IDType != "open_id" || v.Mentions[0].TenantKey != "tenant" || v.SenderIDType != "open_id" {
		t.Fatalf("metadata lost: %+v", v)
	}
	e.Event.Message.MessageType = ptr("post")
	e.Event.Message.Content = ptr(`{"content":[[{"tag":"at","user_id":"bot","user_name":"机器人甲"},{"tag":"text","text":" rich"}]]}`)
	v, matched, err = normalize(b, f, e)
	if err != nil || !matched || v.Text != "@_user_1 rich" || v.MessageType != "post" {
		t.Fatal(v, matched, err)
	}
	e.Event.Message.Mentions = nil
	_, matched, err = normalize(b, f, e)
	if err != nil || matched {
		t.Fatal("post body forged mention authorization", err)
	}
	e.Event.Message.ChatType = ptr("p2p")
	_, matched, err = normalize(b, f, e)
	if err != nil || !matched {
		t.Fatal("private post not admitted", err)
	}

	e.EventV2Base.Header.AppID = "other"
	if _, _, err = normalize(b, f, e); err == nil {
		t.Fatal("cross app accepted")
	}
}

func TestFilterSharedKeywordSemantics(t *testing.T) {
	f, err := Compile(Trigger{Keywords: []string{"HELP，日报"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"help me", "写日报"} {
		got, e := f.Match("group", text, false)
		if e != nil || !got {
			t.Fatal(text, got, e)
		}
	}
	if _, err = Compile(Trigger{Regexes: []string{"/foo/i"}}); err == nil {
		t.Fatal("unsupported syntax accepted")
	}
}
