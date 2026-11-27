package repository

import (
	"context"
	"encoding/json"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/controller"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type manualCredentialProbe struct{}

func (manualCredentialProbe) Verify(context.Context, string, string, string) error { return nil }
func TestManualChannelCredentialsHTTPAndConcurrentSave(t *testing.T) {
	for _, provider := range []string{"feishu", "dingtalk", "wecom"} {
		t.Run(provider, func(t *testing.T) {
			db, _, _, w, actor, agent, cookie := managerFixture(t)
			channels := &service.ChannelService{Store: NewChannelStore(db), Secret: []byte(strings.Repeat("p", 32)), CredentialProbe: manualCredentialProbe{}}
			api := (&controller.Server{Channels: channels, Auth: &service.AuthService{Store: &AuthStore{DB: db}}, Origin: "http://localhost"}).Handler()
			path := "/api/v1/workspaces/" + w + "/agents/" + agent + "/channels/" + provider
			call := func(method, target string, body any, auth, csrf bool) *httptest.ResponseRecorder {
				raw, e := json.Marshal(body)
				if e != nil {
					t.Fatal(e)
				}
				r := httptest.NewRequest(method, target, strings.NewReader(string(raw)))
				if auth {
					r.Header.Set("Cookie", cookie)
				}
				if csrf {
					r.Header.Set("X-CSRF-Protection", "1")
				}
				out := httptest.NewRecorder()
				api.ServeHTTP(out, r)
				return out
			}
			q := service.ChannelCredentialTest{AppID: "cli_manual", AppSecret: "manual-test-secret"}
			if o := call("POST", path+"/test-connection", q, false, true); o.Code != 401 {
				t.Fatal(o.Code)
			}
			if o := call("POST", path+"/test-connection", q, true, false); o.Code != 403 {
				t.Fatal(o.Code)
			}
			o := call("POST", path+"/test-connection", q, true, true)
			if o.Code != 200 {
				t.Fatal(o.Code, o.Body.String())
			}
			var receipt service.ChannelCredentialReceipt
			if e := json.Unmarshal(o.Body.Bytes(), &receipt); e != nil {
				t.Fatal(e)
			}
			if strings.Contains(o.Body.String(), q.AppSecret) {
				t.Fatal("secret leaked")
			}
			if _, e := channels.GetProvider(context.Background(), w, actor, agent, provider); !service.IsChannelMissing(e) {
				t.Fatal("probe persisted", e)
			}
			body := map[string]any{"version": 0, "app_id": q.AppID, "app_secret": q.AppSecret, "enabled": true}
			if provider != "wecom" {
				if o = call("PUT", path, body, true, true); o.Code != 409 {
					t.Fatal("unguarded save", o.Code)
				}
				body["test_receipt"] = receipt.Receipt
			} else if receipt.CredentialStatus != "format_only" || receipt.Receipt != "" {
				t.Fatal("wecom fake authentication")
			}
			var wg sync.WaitGroup
			results := make(chan int, 2)
			for i := 0; i < 2; i++ {
				wg.Add(1)
				go func() { defer wg.Done(); results <- call("PUT", path, body, true, true).Code }()
			}
			wg.Wait()
			close(results)
			counts := map[int]int{}
			for status := range results {
				counts[status]++
			}
			if counts[200] != 1 || counts[409] != 1 {
				t.Fatal("concurrent save", counts)
			}
			b, e := channels.GetProvider(context.Background(), w, actor, agent, provider)
			if e != nil {
				t.Fatal(e)
			}
			if b.Applied.Status != "pending" || b.IdentityConfirmed {
				t.Fatal("invented ready", b)
			}
			q.Version = b.Revision
			q.AppID = "cli_replacement"
			o = call("POST", path+"/test-connection", q, true, true)
			if o.Code != 200 {
				t.Fatal(o.Code, o.Body.String())
			}
			if e = json.Unmarshal(o.Body.Bytes(), &receipt); e != nil {
				t.Fatal(e)
			}
			body = map[string]any{"version": q.Version, "app_id": q.AppID, "app_secret": q.AppSecret, "enabled": true, "test_receipt": receipt.Receipt}
			if o = call("PUT", path, body, true, true); o.Code != 409 {
				t.Fatal("unconfirmed rebind", o.Code)
			}
			body["confirmed_impact"] = true
			if o = call("PUT", path, body, true, true); o.Code != 200 {
				t.Fatal("rebind", o.Code, o.Body.String())
			}
			assignDifferentTestCreator(t, db, w, agent)
			if _, e = db.Exec(`UPDATE platform_members SET role='member' WHERE workspace_id=$1 AND user_id=$2`, w, actor); e != nil {
				t.Fatal(e)
			}
			q.Version++
			if o = call("POST", path+"/test-connection", q, true, true); o.Code != 403 {
				t.Fatal("revoked probe", o.Code)
			}
			if o = call("PUT", path, body, true, true); o.Code != 403 {
				t.Fatal("revoked save", o.Code)
			}
		})
	}
}
