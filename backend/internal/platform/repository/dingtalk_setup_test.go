package repository

import (
	"context"
	"encoding/json"
	"errors"

	"net/http/httptest"
	"strings"

	"sync/atomic"
	"testing"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/controller"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type dingSetupProvider struct {
	begins, polls atomic.Int32
	result        service.DingTalkPoll
	beginHook     func(string)
	pollHook      func()
	pollErr       error
}

func (p *dingSetupProvider) Begin(_ context.Context, source string) (service.DingTalkBegin, error) {
	p.begins.Add(1)
	if p.beginHook != nil {
		p.beginHook(source)
	}
	return service.DingTalkBegin{DeviceCode: "private-device-code", URL: "https://accounts.dingtalk.cn/confirm?challenge=test", ExpiresIn: 600, Interval: 5}, nil
}
func (p *dingSetupProvider) Poll(context.Context, string) (service.DingTalkPoll, error) {
	p.polls.Add(1)
	if p.pollHook != nil {
		p.pollHook()
	}
	return p.result, p.pollErr
}

func TestDingTalkSetupBackgroundBindsWithoutBrowser(t *testing.T) {
	db, _, _, w, actor, agent, _ := managerFixture(t)
	ctx := context.Background()
	now := time.Now()
	provider := &dingSetupProvider{result: service.DingTalkPoll{State: "SUCCESS", AppID: "ding_background_test", Secret: "synthetic-secret"}}
	channels := &service.ChannelService{Store: &ChannelStore{DB: db}, Secret: []byte(strings.Repeat("s", 32))}
	setup := &service.DingTalkSetupService{Store: &DingTalkSetupStore{DB: db}, Channels: channels, Provider: provider, Now: func() time.Time { return now }}
	if _, err := setup.Start(ctx, w, actor, agent, "scheduled-dingtalk"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(6 * time.Second)
	if _, err := db.Exec(`UPDATE platform_dingtalk_registrations SET record=jsonb_set(record,'{next_poll_at}',to_jsonb(now()-interval '1 second'))`); err != nil {
		t.Fatal(err)
	}
	setup.AdvanceBackground(ctx, func(err error) { t.Error(err) })
	v, err := setup.Status(ctx, w, actor, agent)
	if err != nil || v.Registration.State != "consumed" || provider.polls.Load() != 1 {
		t.Fatal(v, err, provider.polls.Load())
	}
}
func TestDingTalkSetupDurableLifecycleAndHTTP(t *testing.T) {
	db, _, _, w, actor, agent, cookie := managerFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	provider := &dingSetupProvider{
		result: service.DingTalkPoll{State: "WAITING"},
		beginHook: func(source string) {
			if source != "kiko" {
				t.Errorf("registration source = %q, want kiko", source)
			}
		},
	}
	channels := &service.ChannelService{Store: &ChannelStore{DB: db}, Secret: []byte(strings.Repeat("s", 32))}
	setup := &service.DingTalkSetupService{Store: &DingTalkSetupStore{DB: db}, Channels: channels, Provider: provider, Source: "kiko", Now: func() time.Time { return now }}
	auth := &service.AuthService{Store: &AuthStore{DB: db}}
	handler := (&controller.Server{Auth: auth, Channels: channels, DingTalkSetup: setup, Origin: "http://localhost"}).Handler()
	base := "/api/v1/workspaces/" + w + "/agents/" + agent + "/channels/dingtalk"
	request := func(method, path, body, key string, authenticated, csrf bool) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if authenticated {
			r.Header.Set("Cookie", cookie)
		}
		if csrf {
			r.Header.Set("X-CSRF-Protection", "1")
		}
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, r)
		return out
	}
	for _, tc := range []struct {
		auth, csrf bool
		body       string
		status     int
	}{{false, true, "{}", 401}, {true, false, "{}", 403}, {true, true, `{"app_secret":"bad"}`, 400}} {
		r := request("POST", base+"/registration", tc.body, "test-start", tc.auth, tc.csrf)
		if r.Code != tc.status {
			t.Fatal(r.Code, r.Body.String())
		}
	}
	r := request("POST", base+"/registration", "{}", "test-start", true, true)
	if r.Code != 202 {
		t.Fatal(r.Code, r.Body.String())
	}
	if r.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(r.Header())
	}
	var out service.DingTalkSetupStatus
	if e := json.Unmarshal(r.Body.Bytes(), &out); e != nil {
		t.Fatal(e)
	}
	id := out.Registration.ID
	if strings.Contains(r.Body.String(), "private-device-code") || strings.Contains(r.Body.String(), "cipher") {
		t.Fatal("private state exposed")
	}
	replay, e := setup.Start(ctx, w, actor, agent, "test-start")
	if e != nil || replay.Registration.ID != id || provider.begins.Load() != 1 {
		t.Fatal(replay, e)
	}
	var raw string
	var encrypted []byte
	if e = db.QueryRow(`SELECT record::text,device_ciphertext FROM platform_dingtalk_registrations WHERE id=$1`, id).Scan(&raw, &encrypted); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(raw, "private-device-code") || strings.Contains(string(encrypted), "private-device-code") {
		t.Fatal("device not encrypted")
	}
	if _, e = setup.Poll(ctx, w, actor, agent, id); e != nil || provider.polls.Load() != 0 {
		t.Fatal(e, provider.polls.Load())
	}
	now = now.Add(6 * time.Second)
	if _, e = setup.Poll(ctx, w, actor, agent, id); e != nil || provider.polls.Load() != 1 {
		t.Fatal(e)
	}
	provider.result = service.DingTalkPoll{State: "SUCCESS", AppID: "ding_fixture_setup", Secret: "private-app-secret"}
	now = now.Add(6 * time.Second)
	v, e := setup.Poll(ctx, w, actor, agent, id)
	if e != nil || v.Registration.State != "ready" || v.Registration.CredentialSaved {
		t.Fatal(v, e)
	}
	if strings.Contains(dingMustJSON(t, v), "private-app-secret") {
		t.Fatal("secret exposed")
	}
	v, e = setup.Confirm(ctx, w, actor, agent, id)
	if e != nil || v.Registration.State != "consumed" || !v.Registration.CredentialSaved {
		t.Fatal(v, e)
	}
	binding, e := channels.GetProvider(ctx, w, actor, agent, "dingtalk")
	if e != nil || binding.AppID != "ding_fixture_setup" || binding.TenantID != "" || binding.IdentityConfirmed || binding.Access.Mode != "all" || len(binding.Access.AllowIDs) != 0 || !binding.Access.GroupMembersEnabled {
		t.Fatal(binding, e)
	}
	replay, e = setup.Confirm(ctx, w, actor, agent, id)
	if e != nil || replay.Registration.State != "consumed" {
		t.Fatal(replay, e)
	}
	if e = db.QueryRow(`SELECT record::text,secret_ciphertext FROM platform_dingtalk_registrations WHERE id=$1`, id).Scan(&raw, &encrypted); e != nil || len(encrypted) != 0 {
		t.Fatal(e, len(encrypted))
	}
	if strings.Contains(raw, "private-app-secret") {
		t.Fatal("secret exposed")
	}
	if _, e = setup.Start(ctx, w, actor, agent, "another"); e == nil {
		t.Fatal("replaced existing app")
	}
}

func dingMustJSON(t *testing.T, v any) string {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}

func TestDingTalkSetupCancellationVersionAndUnknown(t *testing.T) {
	for _, mode := range []string{"cancel", "manual", "expired", "revoked", "transient", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			db, _, _, w, actor, agent, _ := managerFixture(t)
			ctx := context.Background()
			now := time.Now().UTC()
			p := &dingSetupProvider{result: service.DingTalkPoll{State: "SUCCESS", AppID: "ding_fixture", Secret: "private-secret"}}
			channels := &service.ChannelService{Store: &ChannelStore{DB: db}, Secret: []byte(strings.Repeat("s", 32))}
			s := &service.DingTalkSetupService{Store: &DingTalkSetupStore{DB: db}, Channels: channels, Provider: p, Now: func() time.Time { return now }}
			v, e := s.Start(ctx, w, actor, agent, "start")
			if e != nil {
				t.Fatal(e)
			}
			id := v.Registration.ID
			now = now.Add(6 * time.Second)
			switch mode {
			case "cancel":
				p.pollHook = func() {
					if _, e := s.Cancel(ctx, w, actor, agent, id); e != nil {
						t.Error(e)
					}
				}
			case "manual":
				p.pollHook = func() {
					_, e := channels.PutProvider(ctx, w, actor, agent, "dingtalk", service.ChannelPut{AppID: "manual", AppSecret: dingString("manual-secret"), Enabled: true, Trigger: service.ChannelTrigger{Mention: true}, History: service.ChannelHistory{Limit: 20}, Access: service.ChannelAccess{Mode: "allowlist"}})
					if e != nil {
						t.Error(e)
					}
				}
			case "expired":
				now = now.Add(time.Hour)
			case "revoked":
				p.pollHook = func() {
					if _, e := db.Exec(`UPDATE platform_members SET role='member' WHERE workspace_id=$1 AND user_id=$2`, w, actor); e != nil {
						t.Error(e)
					}
				}
			case "transient":
				p.pollErr = errors.New("network error")
			case "unknown":
				p.result.State = "UNKNOWN"
			}
			v, e = s.Poll(ctx, w, actor, agent, id)
			if mode == "revoked" {
				if e == nil {
					t.Fatal("revoked accepted")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			if mode == "transient" || mode == "unknown" {
				if v.Registration.State != "awaiting_confirmation" {
					t.Fatal(v)
				}
				if _, e = s.Start(ctx, w, actor, agent, "start"); e != nil || p.begins.Load() != 1 {
					t.Fatal(e)
				}
				return
			}
			if v.Registration.State == "ready" || v.Registration.State == "consumed" {
				t.Fatal("late credentials accepted", v)
			}
			if _, e = s.Confirm(ctx, w, actor, agent, id); e == nil {
				t.Fatal("unsafe confirm")
			}
		})
	}
}

func dingString(v string) *string { return &v }
