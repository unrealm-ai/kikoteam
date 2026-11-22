package channel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func dingFixture() (Binding, dingFrame) {
	b := Binding{ID: "binding", Provider: "dingtalk", Generation: 1, Revision: 2, RulesVersion: 3, Enabled: true, Credentials: Credentials{AppID: "app", AppSecret: "secret"}}
	return b, dingFrame{Type: "CALLBACK", Headers: map[string]string{"topic": dingRobotTopic, "messageId": "event"}, Data: `{"appKey":"app","chatbotCorpId":"corp","chatbotUserId":"alias","robotCode":"robot","senderCorpId":"corp","senderStaffId":"user","senderId":"sender-alias","msgId":"message","conversationId":"chat","conversationType":"2","msgtype":"text","isInAtList":true,"createAt":123,"text":{"content":"hello"}}`}
}
func TestDingTalkIdentityAndRouting(t *testing.T) {
	for _, test := range []struct {
		name        string
		edit        func(map[string]any)
		match, fail bool
	}{
		{"group mention", func(map[string]any) {}, true, false},
		{"ordinary group", func(v map[string]any) { v["isInAtList"] = false }, false, false},
		{"private", func(v map[string]any) { v["conversationType"] = "1"; v["isInAtList"] = false }, true, false},
		{"external corp", func(v map[string]any) { v["senderCorpId"] = "foreign" }, false, true},
		{"no staff fallback", func(v map[string]any) { delete(v, "senderStaffId") }, false, true},
		{"wrong app", func(v map[string]any) { v["appKey"] = "other" }, false, true},
		{"self", func(v map[string]any) { v["senderId"] = "alias" }, false, false},
		{"unsupported media", func(v map[string]any) { v["msgtype"] = "image" }, false, false},
		{"malformed mention", func(v map[string]any) { v["isInAtList"] = "true" }, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			b, f := dingFixture()
			var v map[string]any
			if err := json.Unmarshal([]byte(f.Data), &v); err != nil {
				t.Fatal(err)
			}
			test.edit(v)
			raw, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			f.Data = string(raw)
			got, match, err := normalizeDingTalk(b, f)
			if match != test.match || (err != nil) != test.fail {
				t.Fatalf("match %v err %v", match, err)
			}
			if match && (got.BotID != "robot" || got.SenderIDType != "user_id" || got.SenderID != "user" || got.ThreadID != "") {
				t.Fatal(got)
			}
		})
	}
	b, f := dingFixture()
	b.BotOpenID = "other"
	if _, _, err := normalizeDingTalk(b, f); err == nil {
		t.Fatal("robot conflict admitted")
	}
	b.BotOpenID = ""
	b.TenantID = "other"
	if _, _, err := normalizeDingTalk(b, f); err == nil {
		t.Fatal("tenant conflict admitted")
	}
}

func TestDingTalkRichTextKeepsTextAndTrustedRouting(t *testing.T) {
	b, f := dingFixture()
	var body map[string]any
	if err := json.Unmarshal([]byte(f.Data), &body); err != nil {
		t.Fatal(err)
	}
	body["msgtype"] = "richText"
	delete(body, "text")
	for _, test := range []struct {
		name, content, want string
		mentioned, fail     bool
	}{
		{"ordered text", `{"richText":[{"text":"开头\n第二行"},{"type":"picture","downloadCode":"media-secret","text":"ignored"},{"type":"text","text":"结尾"}]}`, "开头\n第二行\n结尾", true, false},
		{"body cannot supply an at", `{"richText":[{"text":"@robot 你好"}]}`, "", false, false},
		{"image only", `{"richText":[{"type":"picture","downloadCode":"media-secret"}]}`, "", true, false},
		{"invalid body", `{"richText":{}}`, "", true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			body["isInAtList"] = test.mentioned
			body["content"] = json.RawMessage(test.content)
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			f.Data = string(raw)
			got, matched, err := normalizeDingTalk(b, f)
			if matched != (test.want != "" || test.name == "image only") || (err != nil) != test.fail || got.Text != test.want {
				t.Fatal(got, matched, err)
			}
			if matched && (got.MessageType != "richText" || !got.BotMentioned || got.SenderID != "user") {
				t.Fatal(got)
			}
		})
	}
}

func TestDingEndpointAllowlist(t *testing.T) {
	for _, v := range []string{"ws://a.dingtalk.com/connect", "wss://dingtalk.com.evil/connect", "wss://evil/connect", "wss://user@a.dingtalk.com/connect", "wss://a.dingtalk.com:444/connect", "wss://a.dingtalk.com/connect?redirect=x"} {
		if _, err := dingEndpoint(v, "ticket"); err == nil {
			t.Fatal(v)
		}
	}
	if _, err := dingEndpoint("wss://wss-open-connection.dingtalk.com:443/connect", "ticket"); err != nil {
		t.Fatal(err)
	}
}
func TestDingStreamACKAfterDurableReceipt(t *testing.T) {
	for _, persist := range []bool{true, false} {
		t.Run(map[bool]string{true: "durable", false: "failed"}[persist], func(t *testing.T) {
			stored := false
			backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/internal/v1/channel-inbox" {
					if !persist {
						w.WriteHeader(503)
						return
					}
					stored = true
					if _, err := w.Write([]byte(`{"id":"00000000-0000-4000-8000-000000000001","status":"accepted"}`)); err != nil {
						t.Error(err)
					}
				}
			}))
			defer backendServer.Close()
			backend, err := NewBackend(backendServer.URL, "internal")
			if err != nil {
				t.Fatal(err)
			}
			result := make(chan bool, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				c, e := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if e != nil {
					return
				}
				defer func() { _ = c.Close() }()
				_, f := dingFixture()
				if e = c.WriteJSON(f); e != nil {
					return
				}
				if err := c.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
					t.Error(err)
					return
				}
				var ack map[string]any
				e = c.ReadJSON(&ack)
				result <- e == nil && ack["data"] == `{"response":{"status":"SUCCESS"}}`
			}))
			defer server.Close()
			conn, response, err := websocket.DefaultDialer.Dial("ws"+server.URL[4:], nil)
			if response != nil && response.Body != nil {
				if e := response.Body.Close(); e != nil {
					t.Error(e)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			b, _ := dingFixture()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := runDingStream(ctx, b, backend, conn); err == nil {
				t.Error("closed stream unexpectedly succeeded")
			}
			if ack := <-result; ack != persist || stored != persist {
				t.Fatalf("ack=%v stored=%v", ack, stored)
			}
		})
	}
}
