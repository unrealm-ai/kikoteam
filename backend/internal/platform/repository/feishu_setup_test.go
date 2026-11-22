package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/controller"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type setupProvider struct {
	rangeWrites           atomic.Int32
	rangeErr              error
	skipRange             bool
	rangeHook             func()
	begins, polls, checks atomic.Int32
	result                service.FeishuPoll
	ready                 bool
	authFailed            bool
	pollHook              func()
	beginHook             func()
	pollErr               error
}

func (p *setupProvider) Begin(context.Context, string, string) (service.FeishuBegin, error) {
	p.begins.Add(1)
	if p.beginHook != nil {
		p.beginHook()
	}
	return service.FeishuBegin{DeviceCode: "private-device-code", URL: "https://accounts.feishu.cn/confirm?challenge=test", ExpiresIn: 600, Interval: 5}, nil
}
func (p *setupProvider) Poll(context.Context, string) (service.FeishuPoll, error) {
	p.polls.Add(1)
	if p.pollHook != nil {
		p.pollHook()
	}
	return p.result, p.pollErr
}
func (p *setupProvider) Diagnose(context.Context, string, string) (service.FeishuDiagnostics, error) {
	p.checks.Add(1)
	if p.authFailed {
		return service.FeishuDiagnostics{CredentialStatus: "failed", ErrorCode: "feishu_authentication_failed"}, nil
	}
	permissions := service.FeishuPermissions()
	if p.ready {
		for i := range permissions {
			permissions[i].Status = "granted"
		}
	}
	if p.skipRange {
		for i := range permissions {
			if permissions[i].Scope == service.FeishuContactRangeScope {
				permissions[i].Status = "missing"
			}
		}
	}
	return service.FeishuDiagnostics{CredentialStatus: "pass", BotStatus: "pass", DirectoryStatus: "pass", ScopeStatus: "pass", Ready: p.ready, Permissions: permissions, BotID: "ou_setup_bot", TenantID: "tenant_setup", CheckedAt: time.Now(), ErrorCode: "permissions_required"}, nil
}
func (p *setupProvider) ExpandContactRange(_ context.Context, _, _ string, guard func() error) error {
	if p.rangeHook != nil {
		p.rangeHook()
	}
	if err := guard(); err != nil {
		return err
	}
	p.rangeWrites.Add(1)
	return p.rangeErr
}
func TestFeishuSetupDurableLifecycleAndHTTP(t *testing.T) {
	db, _, _, w, actor, agent, cookie := managerFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	provider := &setupProvider{result: service.FeishuPoll{State: "authorization_pending"}}
	channels := &service.ChannelService{Store: &ChannelStore{DB: db}, Secret: []byte(strings.Repeat("s", 32))}
	setup := &service.FeishuSetupService{Store: &FeishuSetupStore{DB: db}, Channels: channels, Provider: provider, Now: func() time.Time { return now }}
	auth := &service.AuthService{Store: &AuthStore{DB: db}}
	handler := (&controller.Server{Auth: auth, Channels: channels, FeishuSetup: setup, Origin: "http://localhost"}).Handler()
	base := "/api/v1/workspaces/" + w + "/agents/" + agent + "/channels/feishu"
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
	var out service.FeishuSetupStatus
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
	if e = db.QueryRow(`SELECT record::text,device_ciphertext FROM platform_feishu_registrations WHERE id=$1`, id).Scan(&raw, &encrypted); e != nil {
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
	provider.result = service.FeishuPoll{State: "slow_down"}
	now = now.Add(6 * time.Second)
	v, e := setup.Poll(ctx, w, actor, agent, id)
	if e != nil || v.Registration.Interval != 10 {
		t.Fatal(v, e)
	}
	now = now.Add(11 * time.Second)
	provider.result = service.FeishuPoll{State: "credentials_saved", AppID: "cli_fixture_setup", Secret: "private-app-secret"}
	v, e = setup.Poll(ctx, w, actor, agent, id)
	if e != nil || !v.Registration.CredentialSaved || v.Registration.State != "credentials_saved" {
		t.Fatal(v, e)
	}
	v, e = setup.Retry(ctx, w, actor, agent, id)
	if e != nil || v.Registration.State != "needs_attention" {
		t.Fatal(v, e)
	}
	if _, e = channels.Get(ctx, w, actor, agent); !service.IsChannelMissing(e) {
		t.Fatal("unverified binding saved", e)
	}
	provider.ready = true
	v, e = setup.Retry(ctx, w, actor, agent, id)
	if e != nil || v.Registration.State != "connected" {
		t.Fatal(v, e)
	}
	binding, e := channels.Get(ctx, w, actor, agent)
	if e != nil || binding.AppID != "cli_fixture_setup" || !binding.Enabled || !binding.Access.GroupMembersEnabled || !binding.History.Enabled || binding.History.Limit != 20 {
		t.Fatal(binding, e)
	}
	if e = db.QueryRow(`SELECT record::text,secret_ciphertext FROM platform_feishu_registrations WHERE id=$1`, id).Scan(&raw, &encrypted); e != nil || len(encrypted) != 0 {
		t.Fatal(e, len(encrypted))
	}
	if strings.Contains(raw, "private-app-secret") {
		t.Fatal("secret exposed")
	}
	if _, e = setup.Start(ctx, w, actor, agent, "another"); e == nil {
		t.Fatal("replaced existing app")
	}
}
func TestFeishuSetupCancellationAndUnknownAreNotReplayed(t *testing.T) {
	for _, mode := range []string{"cancel", "unknown", "expired", "revoked"} {
		t.Run(mode, func(t *testing.T) {
			db, _, _, w, actor, agent, _ := managerFixture(t)
			ctx := context.Background()
			now := time.Now().UTC()
			provider := &setupProvider{result: service.FeishuPoll{State: "credentials_saved", AppID: "cli_fixture", Secret: "private-secret"}}
			setup := &service.FeishuSetupService{Store: &FeishuSetupStore{DB: db}, Channels: &service.ChannelService{Store: &ChannelStore{DB: db}, Secret: []byte(strings.Repeat("s", 32))}, Provider: provider, Now: func() time.Time { return now }}
			v, e := setup.Start(ctx, w, actor, agent, "start")
			if e != nil {
				t.Fatal(e)
			}
			id := v.Registration.ID
			now = now.Add(6 * time.Second)
			switch mode {
			case "cancel":
				provider.pollHook = func() {
					if _, e := setup.Cancel(ctx, w, actor, agent, id); e != nil {
						t.Error(e)
					}
				}
			case "unknown":
				provider.pollErr = errors.New("uncertain provider outcome")
			case "expired":
				now = now.Add(time.Hour)
			case "revoked":
				provider.pollHook = func() {
					if _, e := db.Exec(`UPDATE platform_members SET role='member' WHERE workspace_id=$1 AND user_id=$2`, w, actor); e != nil {
						t.Error(e)
					}
				}
			}
			v, e = setup.Poll(ctx, w, actor, agent, id)
			if mode == "revoked" {
				if e == nil {
					t.Fatal("revoked admin completed setup")
				}
				return
			}
			expected := map[string]string{"cancel": "cancelled", "unknown": "interrupted", "expired": "expired"}[mode]
			if e != nil || v.Registration.State != expected {
				t.Fatal(v, e)
			}
			count := provider.polls.Load()
			if _, e = setup.Poll(ctx, w, actor, agent, id); e != nil || provider.polls.Load() != count {
				t.Fatal("unexpected replay", e)
			}
			var bindings int
			if e = db.QueryRow(`SELECT count(*) FROM platform_channel_bindings WHERE agent_id=$1`, agent).Scan(&bindings); e != nil || bindings != 0 {
				t.Fatal(bindings, e)
			}
		})
	}
}
func TestFeishuSetupConcurrentStartReusesKey(t *testing.T) {
	db, _, _, w, actor, agent, _ := managerFixture(t)
	provider := &setupProvider{}
	setup := &service.FeishuSetupService{Store: &FeishuSetupStore{DB: db}, Channels: &service.ChannelService{Store: &ChannelStore{DB: db}, Secret: []byte(strings.Repeat("s", 32))}, Provider: provider}
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			if _, e := setup.Start(context.Background(), w, actor, agent, "same-key"); e != nil {
				t.Error(e)
			}
		})
	}
	wg.Wait()
	if provider.begins.Load() != 1 {
		t.Fatal(provider.begins.Load())
	}
}

var _ http.Handler = (*http.ServeMux)(nil)

func TestFeishuSetupOwnershipAndRedactedHTTP(t *testing.T) {
	db, _, _, w, actor, agent, _ := managerFixture(t)
	ctx := context.Background()
	provider := &setupProvider{}
	setup := &service.FeishuSetupService{Store: &FeishuSetupStore{DB: db}, Channels: &service.ChannelService{Store: &ChannelStore{DB: db}, Secret: []byte(strings.Repeat("s", 32))}, Provider: provider}
	got, e := setup.Start(ctx, w, actor, agent, "owned")
	if e != nil {
		t.Fatal(e)
	}
	other := service.NewID()
	if _, e = db.Exec(`INSERT INTO platform_accounts(id,email,name) VALUES($1,'setup-other@example.test','Other')`, other); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`INSERT INTO platform_members(workspace_id,user_id,role) VALUES($1,$2,'admin')`, w, other); e != nil {
		t.Fatal(e)
	}
	status, e := setup.Status(ctx, w, other, agent)
	if e != nil || status.Registration != nil {
		t.Fatal(status, e)
	}
	if _, e = setup.Cancel(ctx, w, other, agent, got.Registration.ID); e == nil {
		t.Fatal("another admin canceled owner's challenge")
	}
	if _, e = setup.Start(ctx, w, other, agent, "owned"); e == nil {
		t.Fatal("another admin replayed owner's key")
	}
	if provider.begins.Load() != 1 {
		t.Fatal(provider.begins.Load())
	}
}

func TestFeishuSetupProviderDelayDoesNotExtendLocalExpiry(t *testing.T) {
	db, _, _, w, actor, agent, _ := managerFixture(t)
	started := time.Now().UTC()
	now := started
	provider := &setupProvider{beginHook: func() { now = now.Add(30 * time.Second) }}
	channels := &service.ChannelService{Store: &ChannelStore{DB: db}, Secret: []byte(strings.Repeat("s", 32))}
	setup := &service.FeishuSetupService{Store: &FeishuSetupStore{DB: db}, Channels: channels, Provider: provider, Now: func() time.Time { return now }}
	got, err := setup.Start(context.Background(), w, actor, agent, "expiry-delay")
	if err != nil {
		t.Fatal(err)
	}
	if got.Registration.State != "awaiting_confirmation" || got.Registration.ExpiresAt.Sub(started.Add(10*time.Minute)).Abs() > time.Millisecond {
		t.Fatalf("registration expiry extended: %s", got.Registration.ExpiresAt)
	}
}

func TestFeishuSetupBackgroundCompletesWithoutBrowserAndClaimsOnce(t *testing.T) {
	db, _, _, w, actor, agent, _ := managerFixture(t)
	now := time.Now().UTC()
	provider := &setupProvider{ready: true, result: service.FeishuPoll{State: "credentials_saved", AppID: "cli_background", Secret: "test-secret"}}
	provider.pollHook = func() { time.Sleep(20 * time.Millisecond) }
	setup := &service.FeishuSetupService{Store: &FeishuSetupStore{DB: db}, Channels: &service.ChannelService{Store: &ChannelStore{DB: db}, Secret: []byte(strings.Repeat("s", 32))}, Provider: provider, Now: func() time.Time { return now }}
	ctx := context.Background()
	started, err := setup.Start(ctx, w, actor, agent, "background")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(6 * time.Second)
	var workers sync.WaitGroup
	for i := 0; i < 2; i++ {
		workers.Add(1)
		go func() { defer workers.Done(); setup.AdvanceBackground(ctx, func(err error) { t.Error(err) }) }()
	}
	workers.Wait()
	state, err := setup.Status(ctx, w, actor, agent)
	if err != nil || state.Registration.State != "connected" || state.Registration.ID != started.Registration.ID {
		t.Fatal(state, err)
	}
	if provider.polls.Load() != 1 || provider.checks.Load() != 1 {
		t.Fatalf("duplicate provider work polls=%d checks=%d", provider.polls.Load(), provider.checks.Load())
	}
	binding, err := setup.Channels.Get(ctx, w, actor, agent)
	if err != nil || binding.AppID != "cli_background" {
		t.Fatal(binding, err)
	}
}

func TestFeishuSetupBackgroundBackoffAndCredentialCancellation(t *testing.T) {
	db, _, _, w, actor, agent, _ := managerFixture(t)
	now := time.Now().UTC()
	provider := &setupProvider{result: service.FeishuPoll{State: "credentials_saved", AppID: "cli_backoff", Secret: "test-secret"}}
	setup := &service.FeishuSetupService{Store: &FeishuSetupStore{DB: db}, Channels: &service.ChannelService{Store: &ChannelStore{DB: db}, Secret: []byte(strings.Repeat("s", 32))}, Provider: provider, Now: func() time.Time { return now }}
	ctx := context.Background()
	started, err := setup.Start(ctx, w, actor, agent, "backoff")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(6 * time.Second)
	v, err := setup.Poll(ctx, w, actor, agent, started.Registration.ID)
	if err != nil || v.Registration.State != "credentials_saved" {
		t.Fatal(v, err)
	}
	v, err = setup.Cancel(ctx, w, actor, agent, started.Registration.ID)
	if err != nil || v.Registration.State != "credentials_saved" || !v.Registration.CredentialSaved {
		t.Fatal("cancel lost saved credentials", v, err)
	}
	setup.AdvanceBackground(ctx, func(err error) { t.Error(err) })
	v, err = setup.Status(ctx, w, actor, agent)
	if err != nil || v.Registration.State != "needs_attention" || v.Registration.NextCheckAt == nil || !v.Registration.NextCheckAt.Equal(now.Add(5*time.Second)) {
		t.Fatal(v, err)
	}
	setup.AdvanceBackground(ctx, func(err error) { t.Error(err) })
	if provider.checks.Load() != 1 {
		t.Fatal("checked before backoff")
	}
	provider.ready = true
	now = now.Add(6 * time.Second)
	setup.AdvanceBackground(ctx, func(err error) { t.Error(err) })
	if provider.checks.Load() != 1 {
		t.Fatal("background must not drive subsequent checks")
	}
	v, err = setup.Retry(ctx, w, actor, agent, started.Registration.ID)
	if err != nil || v.Registration.State != "connected" || provider.checks.Load() != 2 {
		t.Fatal(v, err)
	}
}

func TestFeishuSetupBackgroundUnknownPollNeverReplayedAfterRestart(t *testing.T) {
	db, _, _, w, actor, agent, _ := managerFixture(t)
	now := time.Now().UTC()
	provider := &setupProvider{result: service.FeishuPoll{State: "authorization_pending"}}
	setup := &service.FeishuSetupService{Store: &FeishuSetupStore{DB: db}, Channels: &service.ChannelService{Store: &ChannelStore{DB: db}, Secret: []byte(strings.Repeat("s", 32))}, Provider: provider, Now: func() time.Time { return now }}
	ctx := context.Background()
	started, err := setup.Start(ctx, w, actor, agent, "unknown-poll")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE platform_feishu_registrations SET state='polling',record=jsonb_set(record,'{state}','"polling"') WHERE id=$1`, started.Registration.ID); err != nil {
		t.Fatal(err)
	}
	now = now.Add(91 * time.Second)
	restarted := &service.FeishuSetupService{Store: &FeishuSetupStore{DB: db}, Channels: setup.Channels, Provider: provider, Now: setup.Now}
	restarted.AdvanceBackground(ctx, func(err error) { t.Error(err) })
	state, err := restarted.Status(ctx, w, actor, agent)
	if err != nil || state.Registration.State != "interrupted" || provider.polls.Load() != 0 {
		t.Fatal(state, err)
	}
	restarted.AdvanceBackground(ctx, func(err error) { t.Error(err) })
	if provider.polls.Load() != 0 {
		t.Fatal("unknown redemption replayed")
	}
}

func TestFeishuSetupBackgroundRevocationDoesNotPoll(t *testing.T) {
	db, _, _, w, actor, agent, _ := managerFixture(t)
	now := time.Now().UTC()
	provider := &setupProvider{ready: true, result: service.FeishuPoll{State: "credentials_saved", AppID: "cli_revoked", Secret: "test-secret"}}
	setup := &service.FeishuSetupService{Store: &FeishuSetupStore{DB: db}, Channels: &service.ChannelService{Store: &ChannelStore{DB: db}, Secret: []byte(strings.Repeat("s", 32))}, Provider: provider, Now: func() time.Time { return now }}
	ctx := context.Background()
	if _, err := setup.Start(ctx, w, actor, agent, "revoked-background"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE platform_members SET role='member' WHERE workspace_id=$1 AND user_id=$2`, w, actor); err != nil {
		t.Fatal(err)
	}
	now = now.Add(6 * time.Second)
	setup.AdvanceBackground(ctx, func(err error) { t.Error(err) })
	if provider.polls.Load() != 0 || provider.checks.Load() != 0 {
		t.Fatal("revoked actor advanced setup")
	}
}

func TestFeishuSetupBackgroundManualBindingConflictStopsAutomaticChecks(t *testing.T) {
	db, _, _, w, actor, agent, _ := managerFixture(t)
	now := time.Now().UTC()
	provider := &setupProvider{ready: true, result: service.FeishuPoll{State: "credentials_saved", AppID: "cli_automatic", Secret: "test-secret"}}
	setup := &service.FeishuSetupService{Store: &FeishuSetupStore{DB: db}, Channels: &service.ChannelService{Store: &ChannelStore{DB: db}, Secret: []byte(strings.Repeat("s", 32))}, Provider: provider, Now: func() time.Time { return now }}
	ctx := context.Background()
	started, err := setup.Start(ctx, w, actor, agent, "manual-conflict")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(6 * time.Second)
	if _, err = setup.Poll(ctx, w, actor, agent, started.Registration.ID); err != nil {
		t.Fatal(err)
	}
	secret := "manual-secret"
	_, err = setup.Channels.Put(ctx, w, actor, agent, service.ChannelPut{AppID: "cli_manual", AppSecret: &secret, Enabled: true, History: service.ChannelHistory{Limit: 100}, Access: service.ChannelAccess{Mode: "allowlist"}})
	if err != nil {
		t.Fatal(err)
	}
	setup.AdvanceBackground(ctx, func(err error) { t.Error(err) })
	state, err := setup.Status(ctx, w, actor, agent)
	if err != nil || state.Registration.ErrorCode != "feishu_registration_connection_changed" || !state.Registration.AutoRetryBlocked {
		t.Fatal(state, err)
	}
	now = now.Add(time.Hour)
	setup.AdvanceBackground(ctx, func(err error) { t.Error(err) })
	if provider.checks.Load() != 0 {
		t.Fatal("conflicted binding was probed")
	}
	binding, err := setup.Channels.Get(ctx, w, actor, agent)
	if err != nil || binding.AppID != "cli_manual" {
		t.Fatal("manual binding overwritten", binding, err)
	}
}

func TestFeishuSetupBackgroundAuthenticationFailureBlocksRetries(t *testing.T) {
	db, _, _, w, actor, agent, _ := managerFixture(t)
	now := time.Now().UTC()
	provider := &setupProvider{authFailed: true, result: service.FeishuPoll{State: "credentials_saved", AppID: "cli_invalid", Secret: "test-secret"}}
	setup := &service.FeishuSetupService{Store: &FeishuSetupStore{DB: db}, Channels: &service.ChannelService{Store: &ChannelStore{DB: db}, Secret: []byte(strings.Repeat("s", 32))}, Provider: provider, Now: func() time.Time { return now }}
	ctx := context.Background()
	if _, err := setup.Start(ctx, w, actor, agent, "invalid-auth"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(6 * time.Second)
	setup.AdvanceBackground(ctx, func(err error) { t.Error(err) })
	state, err := setup.Status(ctx, w, actor, agent)
	if err != nil || !state.Registration.AutoRetryBlocked || state.Registration.ErrorCode != "feishu_authentication_failed" {
		t.Fatal(state, err)
	}
	now = now.Add(time.Hour)
	setup.AdvanceBackground(ctx, func(err error) { t.Error(err) })
	if provider.checks.Load() != 1 {
		t.Fatal("invalid credentials repeatedly probed")
	}
}

func TestFeishuReplaceFailedRegistration(t *testing.T) {
	db, _, _, w, actor, agent, _ := managerFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	provider := &setupProvider{authFailed: true, result: service.FeishuPoll{State: "credentials_saved", AppID: "cli_old", Secret: "secret"}}
	setup := &service.FeishuSetupService{Store: &FeishuSetupStore{DB: db}, Channels: &service.ChannelService{Store: &ChannelStore{DB: db}, Secret: []byte(strings.Repeat("s", 32))}, Provider: provider, Now: func() time.Time { return now }}
	old, err := setup.StartWithContactRange(ctx, w, actor, agent, "first", false)
	if err != nil {
		t.Fatal(err)
	}
	id := old.Registration.ID
	replace := func(key string) (service.FeishuSetupStatus, error) {
		return setup.StartRegistration(ctx, w, actor, agent, key, nil, id)
	}
	if _, err = replace("early"); err == nil {
		t.Fatal("replaced an active challenge")
	}
	now = now.Add(6 * time.Second)
	setup.AdvanceBackground(ctx, func(err error) { t.Error(err) })
	fresh, err := replace("replace")
	if err != nil || fresh.Registration.ID == id || fresh.Registration.State != "awaiting_confirmation" || fresh.Registration.ContactRange != nil {
		t.Fatal(fresh, err)
	}
	replay, err := replace("replace")
	if err != nil || replay.Registration.ID != fresh.Registration.ID || provider.begins.Load() != 2 {
		t.Fatal(replay, err)
	}
	if _, err = replace("duplicate"); err == nil {
		t.Fatal("replaced stale registration twice")
	}
	if _, err = setup.Retry(ctx, w, actor, agent, id); err == nil {
		t.Fatal("retried retired credentials")
	}
	var retired bool
	if err = db.QueryRowContext(ctx, `SELECT state='cancelled' AND secret_ciphertext IS NULL AND device_ciphertext IS NULL FROM platform_feishu_registrations WHERE id=$1`, id).Scan(&retired); err != nil || !retired {
		t.Fatal(retired, err)
	}
	provider.authFailed, provider.ready, provider.result.AppID = false, true, "cli_new"
	now = now.Add(6 * time.Second)
	setup.AdvanceBackground(ctx, func(err error) { t.Error(err) })
	state, err := setup.Status(ctx, w, actor, agent)
	if err != nil || state.Registration.ID != fresh.Registration.ID || state.Registration.State != "connected" || state.Registration.AppID != "cli_new" {
		t.Fatal(state, err)
	}
	if _, err = setup.StartRegistration(ctx, w, actor, agent, "after-connect", nil, state.Registration.ID); err == nil {
		t.Fatal("replaced connected registration")
	}
}

func TestFeishuSetupContactRangeAndConnectedExtensions(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		t.Run(fmt.Sprint(uncertain), func(t *testing.T) {
			db, _, _, w, actor, agent, _ := managerFixture(t)
			ctx := context.Background()
			now := time.Now().UTC()
			provider := &setupProvider{ready: true, skipRange: true, result: service.FeishuPoll{State: "credentials_saved", AppID: "cli_contact_range", Secret: "secret"}}
			setup := &service.FeishuSetupService{Store: &FeishuSetupStore{DB: db}, Channels: &service.ChannelService{Store: &ChannelStore{DB: db}, Secret: []byte(strings.Repeat("s", 32))}, Provider: provider, Now: func() time.Time { return now }}
			start, err := setup.Start(ctx, w, actor, agent, "contact-range")
			if err != nil {
				t.Fatal(err)
			}
			now = now.Add(6 * time.Second)
			setup.AdvanceBackground(ctx, func(e error) { t.Error(e) })
			state, err := setup.Status(ctx, w, actor, agent)
			if err != nil || state.Registration.State != "connected" || state.Registration.ContactRange.Status != "waiting_permission" || !state.Registration.AutoCheck.Eligible {
				t.Fatal(state, err)
			}
			provider.skipRange = false
			if uncertain {
				provider.rangeErr = errors.New("network result unknown")
			}
			state, err = setup.Retry(ctx, w, actor, agent, start.Registration.ID)
			want := "applied"
			if uncertain {
				want = "uncertain"
			}
			if err != nil || state.Registration.ContactRange.Status != want || provider.rangeWrites.Load() != 1 {
				t.Fatal(state, err, provider.rangeWrites.Load())
			}
			_, err = setup.Retry(ctx, w, actor, agent, start.Registration.ID)
			if err != nil || provider.rangeWrites.Load() != 1 {
				t.Fatal("write replayed", err, provider.rangeWrites.Load())
			}
		})
	}
}

func TestFeishuSetupBackgroundResumesApprovalAfterRestart(t *testing.T) {
	db, _, _, w, actor, agent, _ := managerFixture(t)
	ctx := context.Background()
	now := time.Now()
	provider := &setupProvider{ready: true, skipRange: true, result: service.FeishuPoll{State: "credentials_saved", AppID: "cli_resume", Secret: "test-secret"}}
	channels := &service.ChannelService{Store: &ChannelStore{DB: db}, Secret: []byte(strings.Repeat("s", 32))}
	makeService := func() *service.FeishuSetupService {
		return &service.FeishuSetupService{Store: &FeishuSetupStore{DB: db}, Channels: channels, Provider: provider, Now: func() time.Time { return now }}
	}
	setup := makeService()
	start, err := setup.Start(ctx, w, actor, agent, "resume-approval")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(6 * time.Second)
	setup.AdvanceBackground(ctx, func(err error) { t.Error(err) })
	if provider.rangeWrites.Load() != 0 {
		t.Fatal("scope changed before approval")
	}
	if _, err = db.Exec(`UPDATE platform_feishu_registrations SET record=jsonb_set(record,'{next_check_at}',to_jsonb(now()-interval '1 second')) WHERE id=$1`, start.Registration.ID); err != nil {
		t.Fatal(err)
	}
	provider.skipRange = false
	for range 2 {
		makeService().AdvanceScheduledChecks(ctx, func(err error) { t.Error(err) })
	}
	state, err := makeService().Status(ctx, w, actor, agent)
	if err != nil || state.Registration.ContactRange.Status != "applied" || provider.rangeWrites.Load() != 1 {
		t.Fatal(state, err, provider.rangeWrites.Load())
	}
}

func TestFeishuSetupWithoutContactRangeDoesNotExpandApp(t *testing.T) {
	db, _, _, w, actor, agent, _ := managerFixture(t)
	ctx := context.Background()
	now := time.Now()
	provider := &setupProvider{ready: true, result: service.FeishuPoll{State: "credentials_saved", AppID: "cli_keep_scope", Secret: "test-secret"}}
	channels := &service.ChannelService{Store: &ChannelStore{DB: db}, Secret: []byte(strings.Repeat("s", 32))}
	setup := &service.FeishuSetupService{Store: &FeishuSetupStore{DB: db}, Channels: channels, Provider: provider, Now: func() time.Time { return now }}
	state, err := setup.StartWithContactRange(ctx, w, actor, agent, "keep-scope", false)
	if err != nil || state.Registration.ContactRange != nil {
		t.Fatal(state, err)
	}
	for _, permission := range state.Permissions {
		if permission.Scope == service.FeishuContactRangeScope {
			t.Fatal("unselected scope advertised in registration")
		}
	}
	now = now.Add(6 * time.Second)
	setup.AdvanceBackground(ctx, func(err error) { t.Error(err) })
	state, err = setup.Status(ctx, w, actor, agent)
	if err != nil || state.Registration.State != "connected" || provider.rangeWrites.Load() != 0 {
		t.Fatal(state, err, provider.rangeWrites.Load())
	}
}

func TestFeishuSetupRestartPendingInterruptsButSavedCredentialsCanRetry(t *testing.T) {
	for _, saved := range []bool{false, true} {
		t.Run(fmt.Sprint(saved), func(t *testing.T) {
			db, _, _, w, actor, agent, _ := managerFixture(t)
			ctx := context.Background()
			now := time.Now().UTC()
			p := &setupProvider{ready: true, result: service.FeishuPoll{State: "credentials_saved", AppID: "cli_restart", Secret: "secret"}}
			setup := &service.FeishuSetupService{Store: &FeishuSetupStore{DB: db}, Channels: &service.ChannelService{Store: &ChannelStore{DB: db}, Secret: []byte(strings.Repeat("s", 32))}, Provider: p, Now: func() time.Time { return now }}
			start, err := setup.Start(ctx, w, actor, agent, "restart")
			if err != nil {
				t.Fatal(err)
			}
			now = now.Add(6 * time.Second)
			if saved {
				if _, err = setup.Poll(ctx, w, actor, agent, start.Registration.ID); err != nil {
					t.Fatal(err)
				}
			}
			restarted := &service.FeishuSetupService{Store: setup.Store, Channels: setup.Channels, Provider: p, Now: setup.Now}
			restarted.AdvanceBackground(ctx, func(e error) { t.Error(e) })
			state, err := restarted.Status(ctx, w, actor, agent)
			if err != nil {
				t.Fatal(err)
			}
			if saved {
				if state.Registration.State != "credentials_saved" {
					t.Fatal(state)
				}
				state, err = restarted.Retry(ctx, w, actor, agent, start.Registration.ID)
				if err != nil || state.Registration.State != "connected" {
					t.Fatal(state, err)
				}
			} else {
				if state.Registration.State != "interrupted" || p.polls.Load() != 0 {
					t.Fatal(state)
				}
			}
		})
	}
}

func TestFeishuSetupContactRangeNeverExpandsLegacyOrChangedBinding(t *testing.T) {
	for _, mode := range []string{"legacy", "revoked", "connection-changed"} {
		t.Run(mode, func(t *testing.T) {
			db, _, _, w, actor, agent, _ := managerFixture(t)
			ctx := context.Background()
			now := time.Now().UTC()
			p := &setupProvider{ready: true, result: service.FeishuPoll{State: "credentials_saved", AppID: "cli_fenced", Secret: "secret"}}
			setup := &service.FeishuSetupService{Store: &FeishuSetupStore{DB: db}, Channels: &service.ChannelService{Store: &ChannelStore{DB: db}, Secret: []byte(strings.Repeat("s", 32))}, Provider: p, Now: func() time.Time { return now }}
			start, err := setup.Start(ctx, w, actor, agent, "fenced")
			if err != nil {
				t.Fatal(err)
			}
			now = now.Add(6 * time.Second)
			if _, err = setup.Poll(ctx, w, actor, agent, start.Registration.ID); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "legacy":
				if _, err = db.Exec(`UPDATE platform_feishu_registrations SET record=record-'profile'-'contact_range' WHERE id=$1`, start.Registration.ID); err != nil {
					t.Fatal(err)
				}
			case "revoked":
				p.rangeHook = func() {
					if _, e := db.Exec(`UPDATE platform_members SET role='member' WHERE workspace_id=$1 AND user_id=$2`, w, actor); e != nil {
						t.Error(e)
					}
				}
			case "connection-changed":
				p.rangeHook = func() {
					secret := "manual"
					if _, e := setup.Channels.Put(ctx, w, actor, agent, service.ChannelPut{AppID: "cli_manual_fence", AppSecret: &secret, Enabled: true, History: service.ChannelHistory{Limit: 100}, Access: service.ChannelAccess{Mode: "all"}}); e != nil {
						t.Error(e)
					}
				}
			}
			result, err := setup.Retry(ctx, w, actor, agent, start.Registration.ID)
			if p.rangeWrites.Load() != 0 {
				t.Fatal("expanded unauthorized or historical application")
			}
			if mode == "legacy" && (err != nil || result.Registration.State != "connected") {
				t.Fatal(result, err)
			}
			if mode == "revoked" && err == nil {
				t.Fatal("revoked admin continued")
			}
			if mode == "connection-changed" && (err != nil || !result.Registration.AutoRetryBlocked) {
				t.Fatal(result, err)
			}
		})
	}
}

func TestFeishuSetupRestartCheckIsRecoverableWithoutReplayOfRange(t *testing.T) {
	db, _, _, w, actor, agent, _ := managerFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	p := &setupProvider{ready: true, result: service.FeishuPoll{State: "credentials_saved", AppID: "cli_crash_check", Secret: "secret"}}
	setup := &service.FeishuSetupService{Store: &FeishuSetupStore{DB: db}, Channels: &service.ChannelService{Store: &ChannelStore{DB: db}, Secret: []byte(strings.Repeat("s", 32))}, Provider: p, Now: func() time.Time { return now }}
	start, err := setup.Start(ctx, w, actor, agent, "crash-check")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(6 * time.Second)
	if _, err = setup.Poll(ctx, w, actor, agent, start.Registration.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE platform_feishu_registrations SET state='checking',record=jsonb_set(jsonb_set(record,'{state}','"checking"'),'{contact_range,status}','"applying"') WHERE id=$1`, start.Registration.ID); err != nil {
		t.Fatal(err)
	}
	restarted := &service.FeishuSetupService{Store: setup.Store, Channels: setup.Channels, Provider: p, Now: setup.Now}
	state, err := restarted.Status(ctx, w, actor, agent)
	if err != nil || !state.Registration.AutoCheck.Eligible || state.Registration.State != "needs_attention" {
		t.Fatal(state, err)
	}
	state, err = restarted.Retry(ctx, w, actor, agent, start.Registration.ID)
	if err != nil || state.Registration.State != "connected" || state.Registration.ContactRange.Status != "uncertain" || p.rangeWrites.Load() != 0 {
		t.Fatal(state, err)
	}
}
