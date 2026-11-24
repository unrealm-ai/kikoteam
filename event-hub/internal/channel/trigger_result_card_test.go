package channel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	"github.com/unrealm-ai/kikoteam/event-hub/internal/taskdetailurl"
)

const sampleTriggerResult = "[系统消息]本次执行已结束\n**合成结果**\n- 第一行"

func triggerResultFixture() Delivery {
	return Delivery{ChatType: "group", ChatID: "group", Text: sampleTriggerResult, Format: "markdown", IdempotencyKey: "operation", TriggerResultCard: true, TaskDetailURL: testIntranetTaskDetailURL}
}

func assertTriggerResultHeader(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if r.Header.Get("X-Kiko-Delivery-Task-Detail") != "1" || r.Header.Get("X-Kiko-Trigger-Result-Card") != "1" {
		t.Error("trigger result negotiation did not send both version headers")
	}
	w.WriteHeader(204)
}

func TestTriggerResultNativeFeishuPreservesBodyAndAnchor(t *testing.T) {
	for _, mode := range []string{"native_markdown", "native_text", "rejected", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			d := triggerResultFixture()
			if mode == "native_text" {
				d.Format = "text"
			}
			sends := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/authorize") {
					assertTriggerResultHeader(t, w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if strings.Contains(r.URL.Path, "tenant_access_token") {
					fixtureWriteRaw(t, w, `{"code":0,"tenant_access_token":"fixture","expire":7200}`)
					return
				}
				sends++
				var req struct {
					Kind    string `json:"msg_type"`
					ID      string `json:"receive_id"`
					Content string `json:"content"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
				}
				if req.ID != d.ChatID {
					t.Error("new card changed notification target")
				}
				var content map[string]any
				if err := json.Unmarshal([]byte(req.Content), &content); err != nil {
					t.Error(err)
				}
				if sends == 1 {
					if req.Kind != "interactive" {
						t.Error("expected native card")
					}
					body := content["body"].(map[string]any)["elements"].([]any)
					first := body[0].(map[string]any)
					if mode == "native_text" {
						if first["tag"] != "div" || first["text"].(map[string]any)["content"] != d.Text {
							t.Error("plain original result lost or interpreted as Markdown")
						}
					} else if first["tag"] != "markdown" || first["content"] != d.Text {
						t.Error("original result body missing")
					}
					button := body[1].(map[string]any)["behaviors"].([]any)[0].(map[string]any)
					if button["type"] != "open_url" || button["default_url"] != d.TaskDetailURL {
						t.Error("native card lost original authorized URL")
					}
					switch mode {
					case "rejected":
						fixtureWriteRaw(t, w, `{"code":230099}`)
						return
					case "unknown":
						w.WriteHeader(502)
						fixtureWriteRaw(t, w, `{"code":230099}`)
						return
					}
				} else {
					lines := content["zh_cn"].(map[string]any)["content"].([]any)
					text := lines[0].([]any)[0].(map[string]any)["text"]
					if req.Kind != "post" || text != d.Text+"\n\n"+taskdetailurl.Link(d.TaskDetailURL) {
						t.Error("fallback did not retain exactly one original summary + same link")
					}
				}
				fixtureWriteRaw(t, w, `{"code":0,"data":{"message_id":"notice-id","chat_id":"group","create_time":"1760000000000"}}`)
			}))
			defer srv.Close()
			backend, err := NewBackend(srv.URL, "internal")
			if err != nil {
				t.Fatal(err)
			}
			if err = backend.request(context.Background(), http.MethodPost, "/api/internal/v1/trigger-notifications/notice/authorize", nil, nil); err != nil {
				t.Fatal(err)
			}
			client := lark.NewClient("app", "secret", lark.WithLogger(quietLogger{}), lark.WithOpenBaseUrl(srv.URL), lark.WithEnableTokenCache(false))
			r := sendWithTaskDetail(context.Background(), Binding{Provider: "feishu"}, d, true, func(ctx context.Context, _ Binding, msg Delivery) deliveryReceipt {
				return sendFeishuWithClient(ctx, client, msg)
			})
			wantStatus, wantPresentation, wantSends := "sent", taskDetailCardFormat, 1
			switch mode {
			case "unknown":
				wantStatus = "unknown"
			case "rejected":
				wantPresentation, wantSends = taskDetailLinkFormat, 2
			}
			if r.Status != wantStatus || r.Presentation != wantPresentation || sends != wantSends {
				t.Fatal(r, sends)
			}
			if wantStatus == "sent" && (r.MessageID != "notice-id" || r.ProviderChatID != d.ChatID || r.ProviderCreatedAtMS != 1760000000000) {
				t.Fatal("lost real provider anchor", r)
			}
		})
	}
}

func TestTriggerResultNativeDingTalkPreservesBody(t *testing.T) {
	for _, mode := range []string{"native", "explicit_reject", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			b, _ := dingFixture()
			b.BotOpenID, b.TenantID = "robot", "corp"
			d := triggerResultFixture()
			sends := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/accessToken") {
					fixtureWriteRaw(t, w, `{"accessToken":"fixture","expireIn":7200}`)
					return
				}
				sends++
				var req map[string]string
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
				}
				var fields map[string]string
				if err := json.Unmarshal([]byte(req["msgParam"]), &fields); err != nil {
					t.Error(err)
				}
				if req["openConversationId"] != d.ChatID || r.URL.Path != "/v1.0/robot/groupMessages/send" {
					t.Error("incorrect notification destination")
				}
				if sends == 1 {
					if req["msgKey"] != "sampleActionCard" || fields["text"] != d.Text || fields["singleURL"] != d.TaskDetailURL {
						t.Error("native card lost original summary or authorized URL")
					}
					switch mode {
					case "explicit_reject":
						w.WriteHeader(400)
						fixtureWriteRaw(t, w, `{"code":"sendMessage.model.notMatch"}`)
						return
					case "unknown":
						fixtureWriteRaw(t, w, `{"code":"unknown.send.result"}`)
						return
					}
				} else if req["msgKey"] != "sampleMarkdown" || fields["text"] != d.Text+"\n\n"+taskdetailurl.Link(d.TaskDetailURL) {
					t.Error("safe fallback lost original summary or same URL")
				}
				fixtureWriteRaw(t, w, `{"processQueryKey":"query-id"}`)
			}))
			defer srv.Close()
			h := dingHTTP{srv.Client(), srv.URL}
			r := sendWithTaskDetail(context.Background(), b, d, true, func(ctx context.Context, b Binding, msg Delivery) deliveryReceipt {
				return sendDingTalkWithHTTP(ctx, b, msg, h)
			})
			wantStatus, wantPresentation, wantSends := "accepted", taskDetailCardFormat, 1
			switch mode {
			case "explicit_reject":
				wantPresentation, wantSends = taskDetailLinkFormat, 2
			case "unknown":
				wantStatus = "unknown"
			}
			if r.Status != wantStatus || r.Presentation != wantPresentation || sends != wantSends {
				t.Fatal(r, sends)
			}
		})
	}
}

func TestTriggerResultNativeWeComPreservesBodyOrUsesOneLink(t *testing.T) {
	for _, mode := range []string{"native", "explicit_reject", "unknown", "too_long", "reminder"} {
		t.Run(mode, func(t *testing.T) {
			f := setupWecom(t, true, false)
			d := triggerResultFixture()
			d.BindingID, d.Generation = f.b.ID, f.b.Generation
			if mode == "too_long" {
				d.Text = strings.Repeat("长", 113)
			}
			if mode == "reminder" {
				d.TaskDetailURL, d.TriggerResultCard = "", false
			}
			done := make(chan deliveryReceipt, 1)
			go func() { done <- sendWithTaskDetail(f.ctx, f.b, d, true, sendWeComDelivery) }()
			frame := nextWecomFrame(t, f, "aibot_send_msg")
			var content map[string]any
			if err := json.Unmarshal(frame.Body, &content); err != nil {
				t.Fatal(err)
			}
			if content["chatid"] != d.ChatID {
				t.Fatal("wrong WeCom result group")
			}
			wantPresentation, wantStatus, ack := taskDetailCardFormat, "sent", 0
			switch mode {
			case "reminder":
				wantPresentation = ""
				if content["msgtype"] != "markdown" || content["markdown"].(map[string]any)["content"] != d.Text {
					t.Fatal("fixed reminder changed presentation/body")
				}
			case "too_long":
				wantPresentation = taskDetailLinkFormat
				if content["msgtype"] != "markdown" || content["markdown"].(map[string]any)["content"] != d.Text+"\n\n"+taskdetailurl.Link(d.TaskDetailURL) {
					t.Fatal("oversize card truncated original notice")
				}
			default:
				card := content["template_card"].(map[string]any)
				if content["msgtype"] != "template_card" || card["sub_title_text"] != d.Text || card["card_action"].(map[string]any)["url"] != d.TaskDetailURL {
					t.Fatal("card did not contain original result")
				}
			}
			switch mode {
			case "explicit_reject":
				ack, wantPresentation = 40008, taskDetailLinkFormat
			case "unknown":
				ack, wantStatus = 50000, "unknown"
			}
			f.push(t, map[string]any{"headers": frame.Headers, "errcode": ack})
			if mode == "explicit_reject" {
				next := nextWecomFrame(t, f, "aibot_send_msg")
				if err := json.Unmarshal(next.Body, &content); err != nil {
					t.Fatal(err)
				}
				if next.Headers.ReqID == frame.Headers.ReqID || content["msgtype"] != "markdown" || content["markdown"].(map[string]any)["content"] != d.Text+"\n\n"+taskdetailurl.Link(d.TaskDetailURL) {
					t.Fatal("fallback did not produce fresh send with original full result and the same link")
				}
				f.push(t, map[string]any{"headers": next.Headers, "errcode": 0})
			}
			select {
			case r := <-done:
				if r.Status != wantStatus || r.Presentation != wantPresentation {
					t.Fatal(r)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("unexpected extra send or missing outcome")
			}
		})
	}
}
