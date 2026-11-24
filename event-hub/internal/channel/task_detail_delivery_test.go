package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	"github.com/unrealm-ai/kikoteam/event-hub/internal/taskdetailurl"
)

const (
	fakeTaskDetailURL         = "https://details.example.test/tasks/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testIntranetTaskDetailURL = "http://192.168.10.20:8080/tasks/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

func testTaskDetailCard() *taskDetailCard {
	return &taskDetailCard{Title: "合成任务", State: "执行中", URL: fakeTaskDetailURL}
}

func TestTaskDetailDeliveryAuthorizationFenceAndReceipt(t *testing.T) {
	for _, mode := range []string{"sent", "fallback", "revoked_between", "unknown", "changed_title", "invalid_url"} {
		t.Run(mode, func(t *testing.T) {
			card := testTaskDetailCard()
			preview := *card
			preview.URL = ""
			d := Delivery{ID: "delivery", LeaseToken: "lease", BindingID: "binding", Provider: "feishu", Generation: 3, ChatID: "chat", Text: "initial", IdempotencyKey: "operation", TaskDetailCard: &preview}
			checks := 0
			receipts := make(chan deliveryReceipt, 1)
			manager := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/authorize"):
					v := d
					v.TargetDigest = "authorized"
					v.TaskDetailCard = card
					if mode == "changed_title" {
						v.TaskDetailCard = &taskDetailCard{Title: "OTHER", State: card.State, URL: card.URL}
					}
					if mode == "invalid_url" {
						v.TaskDetailCard = &taskDetailCard{Title: card.Title, State: card.State, URL: "http://untrusted.example/task"}
					}
					fixtureWriteJSON(t, w, struct {
						Delivery
						Allowed bool `json:"allowed"`
					}{v, true})
				case strings.HasSuffix(r.URL.Path, "/dispatch-check"):
					checks++
					s, err := io.ReadAll(r.Body)
					if err != nil || string(s) != `{"lease_token":"lease"}` {
						t.Error("dispatch check did not use original lease")
					}
					if mode == "revoked_between" && checks == 2 {
						w.WriteHeader(409)
						return
					}
					w.WriteHeader(204)
				case strings.HasSuffix(r.URL.Path, "/complete"):
					var receipt deliveryReceipt
					if err := json.NewDecoder(r.Body).Decode(&receipt); err != nil {
						t.Error(err)
					}
					receipts <- receipt
					w.WriteHeader(204)
				default:
					t.Error("unexpected Manager path")
				}
			}))
			defer manager.Close()
			backend, err := NewBackend(manager.URL, "internal")
			if err != nil {
				t.Fatal(err)
			}
			var logs bytes.Buffer
			backend.Logger = slog.New(slog.NewTextHandler(&logs, nil))
			binding := Binding{ID: d.BindingID, Provider: "feishu", Enabled: true, Generation: 3, Credentials: Credentials{AppID: "app", AppSecret: "secret"}}
			var sends []string
			sender := func(_ context.Context, _ Binding, v Delivery) deliveryReceipt {
				if err := v.BeforeDispatch(); err != nil {
					return deliveryReceipt{Status: "failed", EvidenceType: "not_dispatched", ErrorCode: "gate_rejected"}
				}
				sends = append(sends, v.Format)
				if v.ChatID != d.ChatID || v.IdempotencyKey != d.IdempotencyKey {
					t.Error("delivery operation or target changed")
				}
				if len(sends) == 2 {
					if v.Text != taskdetailurl.Link(fakeTaskDetailURL) || v.TaskDetailCard != nil {
						t.Error("fallback did not carry only the same grant")
					}
					return deliveryReceipt{Status: "sent", MessageID: "link"}
				}
				if v.TaskDetailCard == nil || v.TaskDetailCard.URL != fakeTaskDetailURL || v.Format != taskDetailCardFormat {
					t.Error("missing authorized native card")
				}
				switch mode {
				case "fallback", "revoked_between":
					return deliveryReceipt{Status: "failed", EvidenceType: "send_response", ErrorCode: taskDetailRejected}
				case "unknown":
					return deliveryReceipt{Status: "unknown", ErrorCode: "timeout"}
				}
				return deliveryReceipt{Status: "sent", MessageID: "card"}
			}
			err = deliver(context.Background(), backend, func(string) (Binding, bool) { return binding, true }, sender, d)
			if strings.Contains(logs.String(), "aaaaaaaaaaaaaaaaaaaa") {
				t.Fatal("grant leaked into pipeline logs")
			}
			if mode == "changed_title" {
				if err == nil || len(sends) != 0 || len(receipts) != 0 {
					t.Fatal("authorize substituted another card")
				}
				return
			}
			if err != nil || len(receipts) != 1 {
				t.Fatal("durable completion missing", err)
			}
			r := <-receipts
			wantStatus, wantPresentation, wantSends := "sent", taskDetailCardFormat, 1
			switch mode {
			case "fallback":
				wantPresentation, wantSends = taskDetailLinkFormat, 2
			case "revoked_between":
				wantStatus, wantPresentation = "failed", taskDetailLinkFormat
			case "invalid_url":
				wantStatus, wantPresentation, wantSends = "failed", "", 0
			case "unknown":
				wantStatus = "unknown"
			}
			if r.Status != wantStatus || r.Presentation != wantPresentation || len(sends) != wantSends || r.TargetDigest != "authorized" {
				t.Fatalf("unexpected delivery outcome: %+v sends=%v", r, sends)
			}
		})
	}
}

func fixtureWriteJSON(t *testing.T, w http.ResponseWriter, data any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(data); err != nil {
		t.Error(err)
	}
}

func fixtureWriteRaw(t *testing.T, w http.ResponseWriter, data string) {
	t.Helper()
	if _, err := io.WriteString(w, data); err != nil {
		t.Error(err)
	}
}

func TestTaskDetailFeishuProviderShapeFallbackAndUnknown(t *testing.T) {
	for _, mode := range []string{"direct", "group", "topic", "fallback", "intranet_card", "intranet_fallback", "ambiguous", "contradictory"} {
		t.Run(mode, func(t *testing.T) {
			sends := 0
			detail := testTaskDetailCard()
			if mode == "intranet_card" || mode == "intranet_fallback" {
				detail.URL = testIntranetTaskDetailURL
			}
			d := Delivery{ChatID: "chat", ChatType: "group", IdempotencyKey: "operation", TaskDetailCard: detail}
			switch mode {
			case "direct":
				d.ChatID, d.ChatType, d.RecipientID = "", "direct", "recipient"
			case "topic":
				d.ThreadID, d.ReplyToMessageID = "topic-id", "original"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if strings.Contains(r.URL.Path, "tenant_access_token") {
					fixtureWriteRaw(t, w, `{"code":0,"tenant_access_token":"fixture-token","expire":7200}`)
					return
				}
				if r.Method == http.MethodGet && r.URL.Path == "/open-apis/im/v1/messages/original" {
					fixtureWriteRaw(t, w, `{"code":0,"data":{"items":[{"message_id":"original","chat_id":"chat","thread_id":"topic-id"}]}}`)
					return
				}
				sends++
				var data map[string]any
				if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
					t.Error(err)
				}
				body, _ := data["content"].(string)
				if mode == "topic" && (!strings.HasSuffix(r.URL.Path, "/original/reply") || data["reply_in_thread"] != true) {
					t.Error("card did not preserve topic route")
				}
				if mode == "direct" && (data["receive_id"] != "recipient" || r.URL.Query().Get("receive_id_type") != "open_id") {
					t.Error("card did not preserve direct route")
				}
				if sends == 1 {
					if data["msg_type"] != "interactive" || !strings.Contains(body, `"type":"open_url"`) || !strings.Contains(body, detail.URL) || !strings.Contains(body, "合成任务") {
						t.Error("incorrect Feishu card body")
					}
					switch mode {
					case "fallback", "intranet_fallback":
						fixtureWriteRaw(t, w, `{"code":230099,"msg":"card validation rejected"}`)
						return
					case "ambiguous":
						w.WriteHeader(502)
						fixtureWriteRaw(t, w, `{"code":230099,"msg":"from proxy"}`)
						return
					case "contradictory":
						fixtureWriteRaw(t, w, `{"code":230099,"data":{"message_id":"possibly-sent"}}`)
						return
					}
				} else if data["msg_type"] != "post" || !strings.Contains(body, detail.URL) {
					t.Error("incorrect Feishu ordinary link")
				}
				if mode == "topic" {
					fixtureWriteRaw(t, w, `{"code":0,"data":{"message_id":"posted","chat_id":"chat","thread_id":"topic-id"}}`)
				} else {
					fixtureWriteRaw(t, w, `{"code":0,"data":{"message_id":"posted","chat_id":"chat"}}`)
				}
			}))
			defer server.Close()
			client := lark.NewClient("app", "secret", lark.WithOpenBaseUrl(server.URL), lark.WithEnableTokenCache(false), lark.WithLogger(quietLogger{}))
			r := sendTaskDetail(context.Background(), Binding{Provider: "feishu"}, d, func(ctx context.Context, _ Binding, v Delivery) deliveryReceipt {
				return sendFeishuWithClient(ctx, client, v)
			})
			want, status, format := 1, "sent", taskDetailCardFormat
			switch mode {
			case "fallback", "intranet_fallback":
				want, format = 2, taskDetailLinkFormat
			case "ambiguous", "contradictory":
				status = "unknown"
			}
			if sends != want || r.Status != status || r.Presentation != format {
				t.Fatal(mode, r, sends)
			}
		})
	}
}

func TestTaskDetailDingProviderAndResultQuery(t *testing.T) {
	for _, mode := range []string{"direct", "group", "fallback", "intranet_card", "intranet_fallback", "contradictory", "ambiguous", "permission", "unsafe_url_policy"} {
		t.Run(mode, func(t *testing.T) {
			var sends int
			b, _ := dingFixture()
			b.BotOpenID, b.TenantID = "robot", "corp"
			detail := testTaskDetailCard()
			if mode == "intranet_card" || mode == "intranet_fallback" {
				detail.URL = testIntranetTaskDetailURL
			}
			d := Delivery{ChatType: "group", ChatID: "chat", TaskDetailCard: detail}
			if mode == "direct" {
				d.ChatType, d.RecipientID = "direct", "recipient"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1.0/oauth2/accessToken":
					fixtureWriteRaw(t, w, `{"accessToken":"fixture","expireIn":7200}`)
					return
				case "/v1.0/robot/groupMessages/query", "/v1.0/robot/oToMessages/readStatus":
					fixtureWriteRaw(t, w, `{"sendStatus":"SUCCESS"}`)
					return
				}
				sends++
				var body struct {
					Chat  string   `json:"openConversationId"`
					Users []string `json:"userIds"`
					Key   string   `json:"msgKey"`
					Param string   `json:"msgParam"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				var param map[string]string
				if err := json.Unmarshal([]byte(body.Param), &param); err != nil {
					t.Error(err)
				}
				if d.ChatType == "direct" && (r.URL.Path != "/v1.0/robot/oToMessages/batchSend" || len(body.Users) != 1 || body.Users[0] != d.RecipientID) ||
					d.ChatType == "group" && (r.URL.Path != "/v1.0/robot/groupMessages/send" || body.Chat != d.ChatID) {
					t.Error("incorrect DingTalk target")
				}
				if sends == 1 {
					if body.Key != "sampleActionCard" || param["singleTitle"] != taskDetailLabel || param["singleURL"] != detail.URL || param["text"] != "执行中" {
						t.Error("incorrect native actionCard")
					}
					switch mode {
					case "fallback", "intranet_fallback":
						w.WriteHeader(400)
						fixtureWriteRaw(t, w, `{"code":"invalidParameter.msgKey.invalid"}`)
						return
					case "ambiguous":
						w.WriteHeader(400)
						fixtureWriteRaw(t, w, `{"code":"unknown.send.result"}`)
						return
					case "contradictory":
						fixtureWriteRaw(t, w, `{"code":"invalidParameter.msgKey.invalid","processQueryKey":"possible"}`)
						return
					case "unsafe_url_policy":
						w.WriteHeader(400)
						fixtureWriteRaw(t, w, `{"code":"contain.unsafe.url"}`)
						return
					case "permission":
						w.WriteHeader(403)
						fixtureWriteRaw(t, w, `{"code":"Forbidden.AccessDenied.AccessTokenPermissionDenied"}`)
						return
					}
				} else if body.Key != "sampleMarkdown" || param["text"] != taskdetailurl.Link(detail.URL) {
					t.Error("incorrect DingTalk ordinary link")
				}
				fixtureWriteRaw(t, w, `{"processQueryKey":"query-id"}`)
			}))
			defer server.Close()
			h := dingHTTP{client: server.Client(), origin: server.URL}
			r := sendTaskDetail(context.Background(), b, d, func(ctx context.Context, current Binding, v Delivery) deliveryReceipt {
				return sendDingTalkWithHTTP(ctx, current, v, h)
			})
			wantSends, wantFormat, status := 1, taskDetailCardFormat, "accepted"
			switch mode {
			case "fallback", "intranet_fallback":
				wantSends, wantFormat = 2, taskDetailLinkFormat
			case "contradictory", "ambiguous", "permission", "unsafe_url_policy":
				status = "unknown"
			}
			if sends != wantSends || r.Status != status || r.Presentation != wantFormat || r.MessageID != "" {
				t.Fatal(sends, r)
			}
			if r.Status == "accepted" {
				d.ProviderRequestID = r.ProviderRequestID
				if q := queryDingTalkWithHTTP(context.Background(), b, d, h); q.Status != "sent" || q.EvidenceID != "query-id" || sends != wantSends {
					t.Fatal("not confirmed by read-only query", q)
				}
			}
		})
	}
}

func TestTaskDetailWeComCorrelatedCardAndFallback(t *testing.T) {
	for _, mode := range []string{"direct", "group", "fallback", "intranet_card", "intranet_fallback", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			f := setupWecom(t, true, false)
			detail := testTaskDetailCard()
			if mode == "intranet_card" || mode == "intranet_fallback" {
				detail.URL = testIntranetTaskDetailURL
			}
			d := Delivery{BindingID: f.b.ID, Generation: 1, ChatID: "recipient", ChatType: "direct", ReplyToMessageID: "old-callback", TaskDetailCard: detail}
			if mode == "group" {
				d.ChatType, d.ChatID = "group", "group-id"
			}
			done := make(chan deliveryReceipt, 1)
			go func() { done <- sendTaskDetail(f.ctx, f.b, d, sendWeComDelivery) }()
			first := nextWecomFrame(t, f, "aibot_send_msg")
			var body struct {
				Type string `json:"msgtype"`
				Chat string `json:"chatid"`
				Card struct {
					Type   string           `json:"card_type"`
					Action map[string]any   `json:"card_action"`
					Jumps  []map[string]any `json:"jump_list"`
				} `json:"template_card"`
			}
			if err := json.Unmarshal(first.Body, &body); err != nil || body.Chat != d.ChatID || body.Type != "template_card" || body.Card.Type != "text_notice" ||
				body.Card.Action["url"] != detail.URL || len(body.Card.Jumps) != 1 || body.Card.Jumps[0]["title"] != taskDetailLabel ||
				body.Card.Jumps[0]["url"] != detail.URL || first.Headers.ReqID == "old-callback" {
				t.Fatal("invalid card or uncorrelated original callback")
			}
			code, wantStatus, wantPresentation := 0, "sent", taskDetailCardFormat
			switch mode {
			case "fallback", "intranet_fallback":
				code, wantPresentation = 40008, taskDetailLinkFormat
			case "unknown":
				code, wantStatus = 50000, "unknown"
			}
			f.push(t, map[string]any{"headers": map[string]string{"req_id": "unrelated"}, "errcode": 0})
			f.push(t, map[string]any{"headers": first.Headers, "errcode": code})
			if mode == "fallback" || mode == "intranet_fallback" {
				next := nextWecomFrame(t, f, "aibot_send_msg")
				if next.Headers.ReqID == first.Headers.ReqID || !strings.Contains(string(next.Body), detail.URL) {
					t.Fatal("ordinary link did not use a fresh correlation or same grant")
				}
				f.push(t, map[string]any{"headers": next.Headers, "errcode": 0})
			}
			select {
			case r := <-done:
				if r.Status != wantStatus || r.Presentation != wantPresentation || r.Status == "sent" && r.EvidenceType != "business_ack" {
					t.Fatal(r)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("missing scoped WeCom result")
			}
		})
	}
}

func TestTaskDetailTriggerURLAppendsWithoutChangingOriginal(t *testing.T) {
	for _, tdURL := range []string{"", fakeTaskDetailURL, testIntranetTaskDetailURL, fakeTaskDetailURL + "?param=1", "https://me:other@example.test/tasks/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "file:///tasks/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"} {
		d := Delivery{ID: "trigger", Text: "**原通知**", Format: "markdown", ChatID: "chat", TaskDetailURL: tdURL}
		var count int
		wantLink := tdURL == fakeTaskDetailURL || tdURL == testIntranetTaskDetailURL
		r := sendWithTaskDetail(context.Background(), Binding{Provider: "feishu"}, d, true, func(_ context.Context, _ Binding, v Delivery) deliveryReceipt {
			count++
			want := d.Text
			if wantLink {
				want += "\n\n" + taskdetailurl.Link(tdURL)
			}
			if v.ChatID != "chat" || v.Text != want || v.Format != "markdown" || v.TaskDetailCard != nil {
				t.Error("trigger result route/body changed")
			}
			return deliveryReceipt{Status: "unknown"}
		})
		if count != 1 || r.Status != "unknown" || (r.Presentation == taskDetailLinkFormat) != wantLink {
			t.Fatal("trigger addon created duplicate send or fake confirmation")
		}
	}
}

func TestTaskDetailFeishuDoesNotFollowProviderRedirectWithGrant(t *testing.T) {
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("bearer-bearing card followed provider redirect")
	}))
	defer destination.Close()
	sends := 0
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "tenant_access_token") {
			fixtureWriteRaw(t, w, `{"code":0,"tenant_access_token":"fixture-token","expire":7200}`)
			return
		}
		sends++
		w.Header().Set("Location", destination.URL+"/unexpected")
		w.WriteHeader(http.StatusTemporaryRedirect)
		fixtureWriteRaw(t, w, `{"code":230099,"msg":"synthetic redirect"}`)
	}))
	defer source.Close()
	client := lark.NewClient("app", "secret", lark.WithOpenBaseUrl(source.URL), lark.WithEnableTokenCache(false), lark.WithLogger(quietLogger{}), lark.WithHttpClient(noRedirectTaskDetailHTTP()))
	d := Delivery{ChatID: "chat", ChatType: "group", IdempotencyKey: "operation", TaskDetailCard: testTaskDetailCard()}
	r := sendTaskDetail(context.Background(), Binding{Provider: "feishu"}, d, func(ctx context.Context, _ Binding, v Delivery) deliveryReceipt {
		return sendFeishuWithClient(ctx, client, v)
	})
	if r.Status != "unknown" || sends != 1 {
		t.Fatal("redirect triggered fallback or incorrect state", r, sends)
	}
}

func TestTaskDetailCapabilityHeaderAndDingQueryPresentation(t *testing.T) {
	receipt := make(chan deliveryReceipt, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("X-Kiko-Delivery-Task-Detail")
		switch r.URL.Path {
		case "/api/internal/v1/channel-deliveries/claim", "/api/internal/v1/trigger-notifications/id/authorize":
			if header != "1" {
				t.Error("legacy compatibility capability not advertised")
			}
			w.WriteHeader(204)
		case "/api/internal/v1/channel-delivery-queries/claim":
			fixtureWriteJSON(t, w, map[string]any{"items": []Delivery{{ID: "id", BindingID: "binding", Generation: 1, Provider: "dingtalk", TenantID: "tenant",
				ProviderRequestID: "query", TargetDigest: "digest", LeaseToken: "lease", QueryToken: "claim", Presentation: taskDetailLinkFormat}}})
		case "/api/internal/v1/channel-deliveries/id/complete":
			var v deliveryReceipt
			if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
				t.Error(err)
			}
			receipt <- v
			w.WriteHeader(204)
		default:
			t.Error("unexpected Manager route")
		}
	}))
	defer server.Close()
	b, err := NewBackend(server.URL, "internal")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, path := range []string{"/api/internal/v1/channel-deliveries/claim", "/api/internal/v1/trigger-notifications/id/authorize"} {
		if err = b.request(ctx, http.MethodPost, path, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	err = reconcileDeliveryQueries(ctx, b, func(string) (Binding, bool) {
		return Binding{ID: "binding", Provider: "dingtalk", Generation: 1, TenantID: "tenant", Credentials: Credentials{AppID: "app", AppSecret: "secret"}}, true
	}, func(_ context.Context, _ Binding, d Delivery) deliveryReceipt {
		if d.TaskDetailCard != nil || d.TaskDetailURL != "" {
			t.Error("status-only request exposed bearer")
		}
		return deliveryReceipt{Status: "sent", ProviderRequestID: d.ProviderRequestID, EvidenceType: "status_query", EvidenceID: d.ProviderRequestID}
	})
	if err != nil || len(receipt) != 1 {
		t.Fatal("query completion unavailable", err)
	}
	r := <-receipt
	if r.Status != "sent" || r.Presentation != taskDetailLinkFormat || r.TargetDigest != "digest" || r.QueryToken != "claim" {
		t.Fatal("query lost attempt fact", r)
	}
}

func TestTaskDetailInternalOriginURLBoundary(t *testing.T) {
	const suffix = "/tasks/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for _, tc := range []struct {
		raw  string
		want bool
	}{
		{"http://intranet.example.test" + suffix, true},
		{"https://192.168.10.20:8443" + suffix, true},
		{"http://[fd00::20]:8080" + suffix, true},
		{"http://fd00::20" + suffix, false},
		{"http://[malformed.example]:8080" + suffix, false},
		{testIntranetTaskDetailURL, true},
		{"//192.168.10.20:8080" + suffix, false},
		{"file://intranet.example.test" + suffix, false},
		{"http://user@192.168.10.20:8080" + suffix, false},
		{"http://192.168.10.20:65536" + suffix, false},
		{"http://192.168.10.20:0" + suffix, false},
		{"http://192.168.10.20:" + suffix, false},
		{testIntranetTaskDetailURL + "?token=extra", false},
		{testIntranetTaskDetailURL + "?", false},
		{testIntranetTaskDetailURL + "#", false},
		{testIntranetTaskDetailURL + "/more", false},
		{"http://192.168.10.20:8080/%74asks/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", false},
		{"http://192.168.10.20:8080/" + suffix, false},
	} {
		if got := taskdetailurl.Valid(tc.raw); got != tc.want {
			t.Errorf("link accepted=%v, want=%v (%s)", got, tc.want, tc.raw)
		}
	}
}

func TestOrdinaryTaskDetailCarriesDescriptionAndSameLink(t *testing.T) {
	for _, provider := range []string{"feishu", "dingtalk", "wecom"} {
		t.Run(provider, func(t *testing.T) {
			for _, mode := range []string{"native", "rejected", "long", "unknown"} {
				t.Run(mode, func(t *testing.T) {
					card := testTaskDetailCard()
					card.Description = "已检查。"
					if mode == "long" {
						card.Description = strings.Repeat("已", 113)
					}
					d := Delivery{TaskDetailCard: card}
					var sends []Delivery
					got := sendTaskDetail(t.Context(), Binding{Provider: provider}, d, func(_ context.Context, _ Binding, v Delivery) deliveryReceipt {
						sends = append(sends, v)
						if v.Format == taskDetailCardFormat {
							switch mode {
							case "rejected":
								return deliveryReceipt{Status: "failed", EvidenceType: "send_response", ErrorCode: taskDetailRejected}
							case "unknown":
								return deliveryReceipt{Status: "unknown"}
							}
						}
						return deliveryReceipt{Status: "sent"}
					})
					if len(sends) == 0 || sends[0].Text == "" || !strings.Contains(sends[0].Text, card.Description) {
						t.Fatalf("description lost: %+v", sends)
					}
					last := sends[len(sends)-1]
					switch {
					case mode == "rejected" || mode == "long" && provider == "wecom":
						if got.Presentation != taskDetailLinkFormat || last.Text != card.Description+"\n\n"+taskdetailurl.Link(card.URL) {
							t.Fatalf("original text + same authorized link not sent: %+v", got)
						}
					case mode == "unknown":
						if len(sends) != 1 || got.Status != "unknown" {
							t.Fatal("unknown report was resent")
						}
					default:
						if len(sends) != 1 || got.Presentation != taskDetailCardFormat || last.TaskDetailCard.URL != card.URL {
							t.Fatal("native report card missing")
						}
						var payload map[string]any
						if provider == "feishu" {
							payload = feishuTaskDetailContent(last)
						}
						if provider == "wecom" {
							payload = wecomTaskDetailContent(last)
						}
						if payload != nil {
							raw, err := json.Marshal(payload)
							if err != nil || !strings.Contains(string(raw), card.Description) {
								t.Fatal("renderer lost body", err)
							}
						}
					}
				})
			}
		})
	}
}
