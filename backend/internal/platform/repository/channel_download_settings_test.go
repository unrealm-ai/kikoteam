package repository

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	managercontroller "github.com/unrealm-ai/kikoteam/backend/internal/manager/controller"
	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/controller"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestChannelDownloadSettingsHTTPAndNewMediaClaims(t *testing.T) {
	db, m, internal, workspace, actor, agent, cookie := managerFixture(t)
	ctx := t.Context()
	api := (&controller.Server{
		Auth: &service.AuthService{Store: &AuthStore{DB: db}},
		ChannelDownloads: &service.ChannelDownloadSettingsService{
			Store: &ChannelDownloadSettingsStore{DB: db},
		},
		Origin: "https://kiko.example",
	}).Handler()
	const path = "/api/v1/platform/channel-download-settings"
	request := func(method, body string, auth, csrf bool, want int) service.ChannelDownloadSettings {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if auth {
			r.Header.Set("Cookie", cookie)
		}
		if csrf {
			r.Header.Set("Origin", "https://kiko.example")
			r.Header.Set("X-CSRF-Protection", "1")
		}
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s: HTTP %d, want %d: %s", method, w.Code, want, w.Body)
		}
		var out service.ChannelDownloadSettings
		if want == http.StatusOK {
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
		}
		return out
	}
	request(http.MethodGet, "", false, false, 401)
	request(http.MethodGet, "", true, false, 403)
	const disable = `{"expected_version":0,"validate_public_ip":false}`
	request(http.MethodPatch, disable, true, true, 403)
	reviewExec(t, db, `UPDATE platform_accounts SET is_platform_admin=true WHERE id=$1`, actor)
	request(http.MethodPatch, disable, true, false, 403)
	request(http.MethodPatch, `{"expected_version":0}`, true, true, 400)
	if got := request(http.MethodGet, "", true, false, 200); got.Version != 0 || !got.ValidatePublicIP {
		t.Fatalf("expected default protection: %+v", got)
	}

	store := m.Store.(*managerrepo.Store)
	const hubToken = "fixture-hub-channel-download-token"
	(&managercontroller.ChannelServer{Store: store, Manager: m, Token: hubToken}).Routes(internal)
	providers := []string{"dingtalk", "wecom", "feishu"}
	bindings := make(map[string]string)
	for _, provider := range providers {
		id := manager.NewID()
		bindings[provider] = id
		reviewExec(t, db, `INSERT INTO platform_channel_bindings
 (id,workspace_id,agent_id,provider,connection_generation,rules_version,config_revision,enabled,app_id,external_tenant_id,bot_open_id,credential_ciphertext,trigger,history,access)
 VALUES($1,$2,$3,$4,1,1,1,true,'fixture-bot','fixture-bot','fixture-bot',decode('00','hex'),
 '{"mention":true,"keywords":[],"regexes":[]}','{"enabled":false,"limit":20}','{"mode":"all","allow_ids":[],"deny_ids":[],"allow_chat_ids":[],"deny_chat_ids":[]}')`,
			id, workspace, agent, provider)
	}
	checkClaims := func(version int, validatePublicIP bool) {
		t.Helper()
		for _, provider := range providers {
			msg := inboundFixture(bindings[provider], fmt.Sprintf("%s-file-%d", provider, version), "sender")
			msg.ExternalTenantID, msg.SenderTenant, msg.BotID, msg.SenderIDType = "fixture-bot", "fixture-bot", "fixture-bot", "user_id"
			msg.ProviderDirected = provider == "wecom"
			msg.Media = []manager.MediaReference{{Kind: "file", Name: "report.txt", Locator: "sealed"}}
			inbox, err := store.ReceiveChannel(ctx, msg)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.ProcessChannelInbox(ctx, m); err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(http.MethodPost, "/api/internal/v1/channel-media/claim", nil)
			r.Header.Set("Authorization", "Bearer "+hubToken)
			w := httptest.NewRecorder()
			internal.ServeHTTP(w, r)
			var out struct{ Job *manager.MediaJob }
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Job == nil || out.Job.InboxID != inbox.ID {
				t.Fatalf("%s: unexpected claim: HTTP %d %s", provider, w.Code, w.Body)
			}
			if got, want := out.Job.AllowNonPublicIP, !validatePublicIP && provider != "feishu"; got != want {
				t.Fatalf("%s: allow non-public IP = %t, want %t; version=%d", provider, got, want, version)
			}
		}
	}
	checkClaims(0, true)
	if got := request(http.MethodPatch, disable, true, true, 200); got.Version != 1 || got.ValidatePublicIP {
		t.Fatalf("expected explicit opt-out: %+v", got)
	}
	if got := request(http.MethodGet, "", true, false, 200); got.Version != 1 || got.ValidatePublicIP {
		t.Fatalf("expected persisted opt-out: %+v", got)
	}
	request(http.MethodPatch, disable, true, true, 409)
	checkClaims(1, false)
	if got := request(http.MethodPatch, `{"expected_version":1,"validate_public_ip":true}`, true, true, 200); got.Version != 2 || !got.ValidatePublicIP {
		t.Fatalf("expected protection to be restored: %+v", got)
	}
	checkClaims(2, true)
}
