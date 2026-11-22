package channel

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	lark "github.com/larksuite/oapi-sdk-go/v3"
)

func TestDeliveryAuthorizationAndCancellation(t *testing.T) {
	for _, mode := range []string{"sent", "cancel_before", "cancel_authorize", "stale_generation", "target_changed", "thread_changed", "format_changed", "cancel_send", "revoked", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			d := Delivery{ID: "delivery", LeaseToken: "lease", BindingID: "binding", Generation: 3, ChatID: "chat", Text: "hello", IdempotencyKey: "stable-operation"}
			sends, authorizes, completes := 0, 0, 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer internal" {
					t.Error("missing internal authentication")
				}
				switch {
				case strings.HasSuffix(r.URL.Path, "/authorize"):
					authorizes++
					if mode == "revoked" {
						w.WriteHeader(409)
						return
					}
					v := d
					v.TargetDigest = "verified-target"
					if mode == "cancel_authorize" {
						cancel()
					}
					if mode == "target_changed" {
						v.ChatID = "other"
					}
					if mode == "thread_changed" {
						v.ThreadID = "other-thread"
					}
					if mode == "format_changed" {
						v.Format = "markdown"
					}
					if err := json.NewEncoder(w).Encode(struct {
						Delivery
						Allowed bool `json:"allowed"`
					}{v, true}); err != nil {
						t.Error(err)
					}
				case strings.HasSuffix(r.URL.Path, "/complete"):
					completes++
					var v deliveryReceipt
					if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
						t.Error(err)
					}
					want := "sent"
					if mode == "unknown" {
						want = "unknown"
					}
					if v.Status != want || v.LeaseToken != d.LeaseToken {
						t.Errorf("receipt %+v", v)
					}
					w.WriteHeader(204)
				default:
					t.Error("unexpected path", r.URL.Path)
				}
			}))
			defer srv.Close()
			backend, err := NewBackend(srv.URL, "internal")
			if err != nil {
				t.Fatal(err)
			}
			lookup := func(string) (Binding, bool) {
				generation := int64(3)
				if mode == "stale_generation" {
					generation = 4
				}
				return Binding{ID: "binding", Enabled: true, Provider: "feishu", Generation: generation, Credentials: Credentials{AppID: "app", AppSecret: "secret"}}, true
			}
			send := func(ctx context.Context, _ Binding, v Delivery) deliveryReceipt {
				if ctx.Err() != nil {
					t.Error("send after cancellation")
				}
				sends++
				if mode == "cancel_send" {
					cancel()
				}
				if v.IdempotencyKey != d.IdempotencyKey {
					t.Error("operation replaced")
				}
				if mode == "unknown" {
					return deliveryReceipt{Status: "unknown"}
				}
				return deliveryReceipt{Status: "sent", MessageID: "message"}
			}
			if mode == "cancel_before" {
				cancel()
			}
			err = deliver(ctx, backend, lookup, send, d)
			if mode == "format_changed" && err == nil {
				t.Fatal("accepted a format change during authorization")
			}
			if mode == "sent" || mode == "unknown" {
				if err != nil || sends != 1 || authorizes != 1 || completes != 1 {
					t.Fatalf("err=%v sends=%d auth=%d complete=%d", err, sends, authorizes, completes)
				}
			} else if mode == "cancel_send" {
				if sends != 1 || completes != 1 || err != nil {
					t.Fatal("real receipt after cancellation must still be persisted")
				}
			} else if sends != 0 || completes != 0 {
				t.Fatal("unauthorized/cancelled delivery sent")
			}
		})
	}
}

func TestFeishuDeliveryOfficialUUIDAndUnknown(t *testing.T) {
	for _, mode := range []string{"create", "reply", "thread_reply", "markdown_create", "markdown_thread_reply", "missing_thread_target", "missing_chat_target", "failure", "missing_receipt"} {
		t.Run(mode, func(t *testing.T) {
			lookups, sends := 0, 0
			markdown := strings.HasPrefix(mode, "markdown_")
			thread := strings.Contains(mode, "thread_reply")
			reply := strings.HasSuffix(mode, "reply")
			const markdownText = "# 完成 🎉\n\n**已汇总** 👩🏽‍💻"
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if strings.Contains(r.URL.Path, "tenant_access_token") {
					if _, err := w.Write([]byte(`{"code":0,"tenant_access_token":"fixture-token","expire":7200}`)); err != nil {
						t.Error(err)
					}
					return
				}
				if r.Method == http.MethodGet {
					lookups++
					if !thread || r.URL.Path != "/open-apis/im/v1/messages/original" {
						t.Errorf("unexpected lookup: %s", r.URL.Path)
					}
					if _, err := w.Write([]byte(`{"code":0,"data":{"items":[{"message_id":"original","chat_id":"chat","thread_id":"thread-id"}]}}`)); err != nil {
						t.Error(err)
					}
					return
				}
				sends++
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				kind := "text"
				if markdown {
					kind = "post"
					var content map[string]struct {
						Content [][]map[string]string `json:"content"`
					}
					err := json.Unmarshal([]byte(body["content"].(string)), &content)
					blocks := content["zh_cn"].Content
					if err != nil || len(blocks) != 1 || len(blocks[0]) != 1 || blocks[0][0]["tag"] != "md" || blocks[0][0]["text"] != markdownText {
						t.Errorf("incorrect Feishu markdown body %#v (%v)", content, err)
					}
				}
				if body["uuid"] != "stable-operation" || body["msg_type"] != kind {
					t.Errorf("incorrect provider body %#v", body)
				}
				if reply && !strings.HasSuffix(r.URL.Path, "/original/reply") {
					t.Error("not replying to original message")
				}
				if thread && body["reply_in_thread"] != true {
					t.Error("topic reply lost thread routing")
				}
				if !thread && body["reply_in_thread"] != nil {
					t.Error("main chat reply created a new topic")
				}
				if mode == "failure" {
					w.WriteHeader(502)
					if _, err := w.Write([]byte(`{"code":999,"msg":"failure"}`)); err != nil {
						t.Error(err)
					}
					return
				}
				if mode == "missing_receipt" {
					if _, err := w.Write([]byte(`{"code":0,"data":{}}`)); err != nil {
						t.Error(err)
					}
					return
				}
				response := `{"code":0,"data":{"message_id":"sent-message"}}`
				if thread {
					response = `{"code":0,"data":{"message_id":"sent-message","chat_id":"chat","thread_id":"thread-id"}}`
				}
				if _, err := w.Write([]byte(response)); err != nil {
					t.Error(err)
				}
			}))
			defer srv.Close()
			client := lark.NewClient("app", "secret", lark.WithOpenBaseUrl(srv.URL), lark.WithEnableTokenCache(false), lark.WithLogger(quietLogger{}))
			d := Delivery{ChatID: "chat", Text: "hello", IdempotencyKey: "stable-operation"}
			if markdown {
				d.Text, d.Format = markdownText, "markdown"
			}
			if reply {
				d.ReplyToMessageID = "original"
			}
			if thread || mode == "missing_thread_target" {
				d.ThreadID = "thread-id"
			}
			if mode == "missing_chat_target" {
				d.ChatID, d.ThreadID, d.ReplyToMessageID = "", "thread-id", "original"
			}
			got := sendFeishuWithClient(context.Background(), client, d)
			if thread && lookups != 1 || !thread && lookups != 0 {
				t.Fatalf("incorrect lookup count %d", lookups)
			}
			if mode == "missing_thread_target" || mode == "missing_chat_target" {
				if got.Status != "failed" || got.ErrorCode != "thread_reply_target_missing" || sends != 0 {
					t.Fatalf("topic fell back to main chat: %+v sends=%d", got, sends)
				}
				return
			}
			want := "sent"
			if mode == "missing_receipt" {
				want = "unknown"
			}
			if mode == "failure" {
				want = "failed"
			}
			if got.Status != want || sends != 1 {
				t.Fatalf("receipt=%+v sends=%d", got, sends)
			}
		})
	}
}

func TestFeishuTopicReplyChecksSourceAndDestination(t *testing.T) {
	const source = `{"message_id":"original","chat_id":"chat","thread_id":"thread"}`
	const validSource = `{"code":0,"data":{"items":[` + source + `]}}`
	const confirmed = `{"code":0,"data":{"message_id":"sent","chat_id":"chat","thread_id":"thread"}}`
	for _, tc := range []struct {
		name, lookup, reply, status, code string
	}{
		{"verified", validSource, confirmed, "sent", ""},
		{"wrong original", strings.Replace(validSource, "original", "other", 1), "", "failed", "thread_reply_source_mismatch"},
		{"original in other chat", strings.Replace(validSource, `"chat"`, `"other"`, 1), "", "failed", "thread_reply_source_mismatch"},
		{"original in other topic", strings.Replace(validSource, `"thread"`, `"other"`, 1), "", "failed", "thread_reply_source_mismatch"},
		{"original in main chat", strings.Replace(validSource, `,"thread_id":"thread"`, "", 1), "", "failed", "thread_reply_source_mismatch"},
		{"source lookup denied", `{"code":99991672}`, "", "failed", "thread_reply_source_unavailable"},
		{"source lookup malformed", `{`, "", "failed", "thread_reply_source_unavailable"},
		{"source missing", `{"code":0,"data":{"items":[]}}`, "", "failed", "thread_reply_source_unavailable"},
		{"source ambiguous", strings.Replace(validSource, source, source+","+source, 1), "", "failed", "thread_reply_source_unavailable"},
		{"source recalled", strings.Replace(validSource, `"message_id"`, `"deleted":true,"message_id"`, 1), "", "failed", "thread_reply_source_unavailable"},
		{"binding changed after lookup", validSource, "", "failed", "binding_changed_before_send"},
		{"receipt in other chat", validSource, strings.Replace(confirmed, `"chat"`, `"other"`, 1), "unknown", "thread_reply_receipt_unconfirmed"},
		{"receipt in other topic", validSource, strings.Replace(confirmed, `"thread"`, `"other"`, 1), "unknown", "thread_reply_receipt_unconfirmed"},
		{"receipt missing chat", validSource, strings.Replace(confirmed, `,"chat_id":"chat"`, "", 1), "unknown", "thread_reply_receipt_unconfirmed"},
		{"receipt missing topic", validSource, strings.Replace(confirmed, `,"thread_id":"thread"`, "", 1), "unknown", "thread_reply_receipt_unconfirmed"},
		{"receipt missing message", validSource, strings.Replace(confirmed, `"message_id":"sent",`, "", 1), "unknown", "provider_receipt_missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reads, writes atomic.Int32
			const path = "/open-apis/im/v1/messages/original"
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				var response string
				switch {
				case strings.Contains(r.URL.Path, "tenant_access_token"):
					response = `{"code":0,"tenant_access_token":"fixture-token","expire":7200}`
				case r.Method == http.MethodGet && r.URL.Path == path:
					reads.Add(1)
					response = tc.lookup
				case r.Method == http.MethodPost && r.URL.Path == path+"/reply":
					writes.Add(1)
					response = tc.reply
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				if _, err := w.Write([]byte(response)); err != nil {
					t.Error(err)
				}
			}))
			defer srv.Close()
			client := lark.NewClient("app", "secret", lark.WithOpenBaseUrl(srv.URL), lark.WithEnableTokenCache(false), lark.WithLogger(quietLogger{}))
			d := Delivery{ChatID: "chat", ThreadID: "thread", ReplyToMessageID: "original", IdempotencyKey: "attempt", Text: "reply"}
			d.BeforeDispatch = func() error {
				if reads.Load() != 1 {
					t.Error("binding check ran before source lookup")
				}
				if tc.code == "binding_changed_before_send" {
					return errors.New("revoked")
				}
				return nil
			}
			got := sendFeishuWithClient(context.Background(), client, d)
			if got.Status != tc.status || got.ErrorCode != tc.code || reads.Load() != 1 {
				t.Fatalf("receipt=%+v reads=%d", got, reads.Load())
			}
			if tc.status == "failed" {
				if writes.Load() != 0 || got.EvidenceType != "not_dispatched" {
					t.Fatalf("preflight failure wrote a message: receipt=%+v writes=%d", got, writes.Load())
				}
			} else if writes.Load() != 1 {
				t.Fatalf("expected a single reply, got %d", writes.Load())
			}
		})
	}
}
