package channel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	lark "github.com/larksuite/oapi-sdk-go/v3"
)

func TestNativeReceiptReactionRequest(t *testing.T) {
	t.Run("Feishu", func(t *testing.T) {
		var writes atomic.Int32
		v := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal" {
				_, _ = fmt.Fprint(w, `{"code":0,"tenant_access_token":"synthetic-token","expire":7200}`) //nolint:errcheck // fixture
				return
			}
			var body struct {
				Reaction map[string]string `json:"reaction_type"`
			}
			writes.Add(1)
			if r.Method != "POST" || r.URL.Path != "/open-apis/im/v1/messages/om_original/reactions" ||
				r.Header.Get("Authorization") != "Bearer synthetic-token" || json.NewDecoder(r.Body).Decode(&body) != nil || body.Reaction["emoji_type"] != "OK" {
				t.Error("incorrect reaction request", r.URL.Path, body)
			}
			// Permission denied is swallowed by the worker; the adapter returns only a sanitized result.
			_, _ = fmt.Fprint(w, `{"code":99991672,"msg":"private-provider-diagnostic"}`) //nolint:errcheck // fixture
		}))
		defer v.Close()
		api := lark.NewClient("cli_reaction_fixture", "fixture-secret", lark.WithOpenBaseUrl(v.URL), lark.WithLogger(quietLogger{}))
		target := reactionTarget{MessageID: "om_original"}
		if e := sendFeishuReceipt(t.Context(), api, target, func() bool { return false }); e != nil || writes.Load() != 0 {
			t.Fatal("withdrawn attempt wrote a reaction", e, writes.Load())
		}
		if e := sendFeishuReceipt(t.Context(), api, target, func() bool { return true }); e == nil || e.Error() != "feishu reaction unavailable" || writes.Load() != 1 {
			t.Fatal("reaction failed without non-retry/sanitized boundary", e, writes.Load())
		}
	})
	t.Run("DingTalk", func(t *testing.T) {
		var writes, mode atomic.Int32
		v := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v1.0/oauth2/accessToken":
				_, _ = fmt.Fprint(w, `{"accessToken":"synthetic-token","expireIn":7200}`) //nolint:errcheck // fixture
			case "/v1.0/robot/emotion/reply":
				writes.Add(1)
				var body struct {
					Robot, Chat, Message, Label string
				}
				var raw map[string]json.RawMessage
				if r.Method != "POST" || r.Header.Get("x-acs-dingtalk-access-token") != "synthetic-token" || json.NewDecoder(r.Body).Decode(&raw) != nil {
					t.Error("incorrect app-auth request")
				}
				body.Robot, body.Chat, body.Message, body.Label = string(raw["robotCode"]), string(raw["openConversationId"]), string(raw["openMsgId"]), string(raw["emotionName"])
				var emotion map[string]string
				if body.Robot != `"robot_app"` || body.Chat != `"chat_original"` || body.Message != `"original_callback_msgId"` || body.Label != `"已收到"` ||
					string(raw["emotionType"]) != "2" || json.Unmarshal(raw["textEmotion"], &emotion) != nil ||
					emotion["emotionId"] != "2659900" || emotion["emotionName"] != "已收到" || emotion["text"] != "已收到" || emotion["backgroundId"] != "im_bg_1" {
					t.Error("incorrect DingTalk reaction target or body", body, emotion)
				}
				switch mode.Load() {
				case 1:
					_, _ = fmt.Fprint(w, `{"success":true}`) //nolint:errcheck // fixture
				case 2:
					_, _ = fmt.Fprint(w, `{"success":false}`) //nolint:errcheck // fixture
				case 3:
					_, _ = fmt.Fprint(w, `{}`) //nolint:errcheck // fixture
				default:
					w.WriteHeader(http.StatusForbidden)
				}
			default:
				t.Error("unexpected provider request", r.URL.Path)
			}
		}))
		defer v.Close()
		target := reactionTarget{BotID: "robot_app", ChatID: "chat_original", MessageID: "original_callback_msgId"}
		h := dingHTTP{client: v.Client(), origin: v.URL}
		if e := sendDingReceipt(t.Context(), h, Binding{}, target, func() bool { return false }); e != nil || writes.Load() != 0 {
			t.Fatal("withdrawn attempt wrote a reaction", e, writes.Load())
		}
		if e := sendDingReceipt(t.Context(), h, Binding{}, target, func() bool { return true }); e == nil || writes.Load() != 1 {
			t.Fatal("expected a single denied write", e, writes.Load())
		}
		for _, status := range []int32{1, 2, 3} {
			mode.Store(status)
			e := sendDingReceipt(t.Context(), h, Binding{}, target, func() bool { return true })
			if (e == nil) != (status == 1) || writes.Load() != status+1 {
				t.Fatal("incorrect DingTalk receipt classification", status, writes.Load(), e)
			}
		}
	})
}

func TestReceiptReactionWorkerRechecksAndDoesNotRetry(t *testing.T) {
	for _, tc := range []string{"revoked", "failure", "wrong_binding", "claim_response_lost"} {
		t.Run(tc, func(t *testing.T) {
			var claims, checks, sends atomic.Int32
			target := reactionTarget{BindingID: "binding", Provider: "dingtalk", Generation: 1, TenantID: "tenant", BotID: "bot"}
			v := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == reactionEndpoint+"/claim" {
					if claims.Add(1) == 1 {
						if tc == "claim_response_lost" {
							w.WriteHeader(http.StatusServiceUnavailable)
							return
						}
						_, _ = fmt.Fprint(w, `{"claim":{"inbox_id":"orig","claim_token":"claim"}}`) //nolint:errcheck // fixture
						return
					}
					_, _ = fmt.Fprint(w, `{"claim":null}`) //nolint:errcheck // fixture
					return
				}
				if checks.Add(1) == 2 && tc == "revoked" {
					_, _ = fmt.Fprint(w, `{"target":null}`) //nolint:errcheck // fixture
					return
				}
				if r.URL.Path != reactionEndpoint+"/orig/authorize" || json.NewEncoder(w).Encode(map[string]any{"target": target}) != nil {
					t.Error("incorrect authorization")
				}
			}))
			defer v.Close()
			backend, err := NewBackend(v.URL, "synthetic-token")
			if err != nil {
				t.Fatal(err)
			}
			b := Binding{Enabled: true, ID: target.BindingID, Provider: target.Provider, Generation: 1, TenantID: target.TenantID, BotOpenID: target.BotID}
			if tc == "wrong_binding" {
				b.Generation++
			}
			lookup := func(string) (Binding, bool) { return b, true }
			send := func(_ context.Context, _ Binding, _ reactionTarget, allowed func() bool) error {
				if !allowed() {
					return nil
				}
				sends.Add(1)
				return errors.New("synthetic failure")
			}
			_, err = backend.processReaction(t.Context(), lookup, send)
			if (err != nil) != (tc == "failure" || tc == "claim_response_lost") {
				t.Fatal("incorrect worker error", err)
			}
			if more, err := backend.processReaction(t.Context(), lookup, send); more || err != nil {
				t.Fatal("unexpected second claim", more, err)
			}
			if got := sends.Load(); (got == 1) != (tc == "failure") {
				t.Fatal("incorrect provider attempt count", got)
			}
		})
	}
}
