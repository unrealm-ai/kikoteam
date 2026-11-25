package channel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	lark "github.com/larksuite/oapi-sdk-go/v3"
)

func TestHumanCardRefreshThroughManagerAndProvider(t *testing.T) {
	for _, mode := range []string{"feishu", "dingtalk", "wecom", "stale", "unknown", "ding_incomplete_ack"} {
		t.Run(mode, func(t *testing.T) {
			provider := mode
			if mode == "ding_incomplete_ack" {
				provider = "dingtalk"
			}
			if mode == "stale" || mode == "unknown" {
				provider = "feishu"
			}
			j := humanCardRefresh{ID: "a0000000-0000-4000-8000-000000000001", LeaseToken: "a0000000-0000-4000-8000-000000000002",
				BindingID: "binding", Provider: provider, TenantID: "tenant", Generation: 1, Revision: 2, Version: 1,
				State: "rejected", ProviderCardID: "om_original", Card: &humanConfirmationCard{Title: "测试", Description: "original safe summary", DecisionRequired: true}}
			if provider == "dingtalk" {
				j.ProviderCardID = j.ID
			}
			binding := Binding{ID: j.BindingID, Provider: provider, TenantID: j.TenantID, Generation: 1, Revision: 2, Enabled: true, Credentials: Credentials{AppID: "app", AppSecret: "secret"}}
			var writes, authorizes int
			var completion string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == humanRefreshPath+"/claim":
					fixtureWriteJSON(t, w, map[string]any{"items": []humanCardRefresh{j}})
				case strings.HasSuffix(r.URL.Path, "/authorize"):
					authorizes++
					authorized := j
					if mode == "stale" {
						// Simulate a changed/reissued original just before transport.
						authorized.Version++
						authorized.ProviderCardID = "om_other"
					}
					fixtureWriteJSON(t, w, authorized)
				case strings.HasSuffix(r.URL.Path, "/complete"):
					var body map[string]string
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["lease_token"] != j.LeaseToken {
						t.Error("completion mismatched lease", err)
					}
					completion = body["result"]
					w.WriteHeader(204)
				case strings.Contains(r.URL.Path, "tenant_access_token"):
					fixtureWriteJSON(t, w, map[string]any{"code": 0, "tenant_access_token": "fixture", "expire": 7200})
				case strings.HasSuffix(r.URL.Path, "/accessToken"):
					fixtureWriteJSON(t, w, map[string]any{"accessToken": "fixture", "expireIn": 7200})
				case r.Method == http.MethodPatch && r.URL.Path == "/open-apis/im/v1/messages/"+j.ProviderCardID:
					writes++
					var raw map[string]string
					if err := json.NewDecoder(r.Body).Decode(&raw); err != nil || strings.Contains(raw["content"], `"tag":"button"`) ||
						!strings.Contains(raw["content"], "已拒绝") || !strings.Contains(raw["content"], j.Card.Description) {
						t.Error("incorrect replacement", err)
					}
					if mode == "unknown" {
						fixtureWriteJSON(t, w, map[string]any{}) // Not proved by default numeric zero.
					} else {
						fixtureWriteJSON(t, w, map[string]int{"code": 0})
					}
				case r.Method == http.MethodPut && r.URL.Path == "/v1.0/card/instances":
					writes++
					var raw struct {
						Track string `json:"outTrackId"`
						Card  struct {
							Params map[string]string `json:"cardParamMap"`
						} `json:"cardData"`
					}
					if err := json.NewDecoder(r.Body).Decode(&raw); err != nil || raw.Track != j.ProviderCardID ||
						!strings.Contains(strings.Join(valuesForHumanRefresh(raw.Card.Params), " "), j.Card.Description) {
						t.Error("incorrect Ding update", err)
					}
					if mode == "ding_incomplete_ack" {
						fixtureWriteJSON(t, w, map[string]bool{"success": true})
					} else {
						fixtureWriteJSON(t, w, map[string]bool{"success": true, "result": true})
					}
				default:
					t.Errorf("unexpected new send or cross-target provider route: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			manager, err := NewBackend(server.URL, "hub")
			if err != nil {
				t.Fatal(err)
			}
			fapi := lark.NewClient("app", "secret", lark.WithOpenBaseUrl(server.URL), lark.WithLogger(quietLogger{}), lark.WithEnableTokenCache(false))
			write := func(ctx context.Context, b Binding, j humanCardRefresh, check func() bool) string {
				if j.Provider == "feishu" {
					return refreshFeishuHumanCard(ctx, fapi, j, check)
				}
				if j.Provider != "dingtalk" {
					t.Error("attempted proactive WeCom update")
					return "failed"
				}
				return refreshDingHumanCard(ctx, b, dingHTTP{server.Client(), server.URL}, j, check)
			}
			if err = refreshTerminalHumanCards(context.Background(), manager, func(string) (Binding, bool) { return binding, true }, write); err != nil {
				t.Fatal(err)
			}
			wantWrites, wantAuth, wantCompletion := 1, 1, "updated"
			switch mode {
			case "wecom":
				wantWrites, wantAuth, wantCompletion = 0, 0, ""
			case "stale":
				wantWrites, wantCompletion = 0, "unknown"
			case "unknown", "ding_incomplete_ack":
				wantCompletion = "unknown"
			}
			if writes != wantWrites || authorizes != wantAuth || completion != wantCompletion {
				t.Fatalf("writes=%d authorize=%d completion=%q", writes, authorizes, completion)
			}
		})
	}
}

func valuesForHumanRefresh(p map[string]string) []string {
	out := make([]string, 0, len(p))
	for _, v := range p {
		out = append(out, v)
	}
	return out
}
