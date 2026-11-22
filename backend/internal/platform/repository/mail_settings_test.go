package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/controller"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestPlatformMailSettingsRuntimeAndHTTP(t *testing.T) {
	ctx := context.Background()
	db := workspaceTestDB(t)
	fallback := service.MailPolicy{Enabled: true}
	authStore := &AuthStore{DB: db, MailFallback: &fallback}
	mailStore := &MailSettingsStore{DB: db}
	candidate := service.MailSettings{
		MailPolicy: fallback, Address: "mail.example.test:465", Host: "mail.example.test",
		Username: "root@example.test", From: "noreply@example.test", ImplicitTLS: true,
	}
	var deliveryTarget string
	probes := 0
	settings := &service.MailSettingsService{
		Store: mailStore, Secret: bytes.Repeat([]byte("x"), 32),
		Fallback: service.MailSettingsDraft{MailSettings: candidate, Password: "environment-fixture-secret"},
		Probe: func(_ context.Context, d service.MailSettingsDraft) error {
			probes++
			if d.Password == "reject-fixture" {
				return errors.New("SMTP fixture rejection")
			}
			return nil
		},
		SendTest: func(_ context.Context, d service.MailSettingsDraft, to string) error {
			deliveryTarget = to
			if d.Password != "environment-fixture-secret" {
				return errors.New("unexpected effective test password")
			}
			return nil
		},
	}
	auth := &service.AuthService{Store: authStore, ReadMailPolicy: settings.Policy, Secret: settings.Secret}
	_, adminToken, err := auth.RegisterWithPassword(ctx, "admin@example.test", "Admin", "fixture-password", "smtp-fixture")
	if err != nil {
		t.Fatal(err)
	}
	_, memberToken, err := auth.RegisterWithPassword(ctx, "member@example.test", "Member", "fixture-password", "smtp-fixture")
	if err != nil {
		t.Fatal(err)
	}
	h := (&controller.Server{Origin: "https://example.test", Auth: auth, MailSettings: settings}).Handler()
	request := func(method, path, token string, value any, want int) *httptest.ResponseRecorder {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("X-CSRF-Protection", "1")
		r.Header.Set("Origin", "https://example.test")
		if token != "" {
			r.AddCookie(&http.Cookie{Name: "kiko_session", Value: token})
		}
		rw := httptest.NewRecorder()
		h.ServeHTTP(rw, r)
		if rw.Code != want {
			t.Fatalf("%s %s: status %d; want %d; body %s", method, path, rw.Code, want, rw.Body)
		}
		return rw
	}
	const path = "/api/v1/platform/mail-settings"
	const optionsPath = "/api/v1/auth/options"
	request("GET", path, "", nil, 401)
	request("GET", path, memberToken, nil, 403)
	fallbackView := request("GET", path, adminToken, nil, 200)
	assertSafeMailView(t, fallbackView, "environment", 0)
	draft := service.MailSettingsDraft{MailSettings: candidate}
	request("POST", path+"/test", memberToken, draft, 403)
	if deliveryTarget != "" {
		t.Fatal("unauthorized mail test reached sender")
	}
	request("POST", path+"/test", adminToken, draft, 200)
	if deliveryTarget != "admin@example.test" {
		t.Fatal("mail test must target current admin")
	}
	draft.Address = "other.example.test:465"
	request("POST", path+"/test", adminToken, draft, 400)
	draft.Address = candidate.Address
	draft.Password = "reject-fixture"
	request("PATCH", path, adminToken, draft, 400)
	fallbackView = request("GET", path, adminToken, nil, 200)
	assertSafeMailView(t, fallbackView, "environment", 0)
	draft.Password = "database-fixture-secret"
	draft.VerificationRequired = true
	view := request("PATCH", path, adminToken, draft, 200)
	assertSafeMailView(t, view, "database", 1)
	if probes != 2 {
		t.Fatal("mail setup probe not enforced")
	}
	record, err := mailStore.Read(ctx)
	if err != nil || len(record.Ciphertext) == 0 || bytes.Contains(record.Ciphertext, []byte(draft.Password)) {
		t.Fatal("expected encrypted stored secret", err)
	}
	request("PATCH", path, adminToken, draft, 409)
	flags := request("GET", optionsPath, "", nil, 200)
	if !strings.Contains(flags.Body.String(), `"password_registration_enabled":false`) || strings.Contains(flags.Body.String(), "mail.example.test") {
		t.Fatal("public options not derived from DB policy")
	}
	request("POST", "/api/v1/auth/registrations", "", map[string]any{
		"method": "password", "email": "other@example.test", "name": "Other", "password": "fixture-password",
	}, 403)
	draft.Version = 1
	draft.Password = ""
	draft.Enabled = false
	request("PATCH", path, adminToken, draft, 400)
	draft.VerificationRequired = false
	view = request("PATCH", path, adminToken, draft, 200)
	assertSafeMailView(t, view, "database", 2)
	flags = request("GET", optionsPath, "", nil, 200)
	if !strings.Contains(flags.Body.String(), `"email_code_enabled":false`) {
		t.Fatal("explicit DB disable did not override enabled environment")
	}
	request("POST", "/api/v1/auth/email-codes", "", map[string]string{"email": "other@example.test", "purpose": "register"}, 403)
	draft.Version = 2
	draft.Enabled = true
	view = request("PATCH", path, adminToken, draft, 200)
	assertSafeMailView(t, view, "database", 3)
	effective, err := settings.Runtime(ctx)
	if err != nil || effective.Password != "database-fixture-secret" {
		t.Fatal("blank password did not keep same-target secret", err)
	}
	// Simulate an unavailable settings lookup without touching any real data.
	if _, err := db.ExecContext(ctx, "ALTER TABLE platform_mail_settings RENAME TO missing_mail_settings_for_test"); err != nil {
		t.Fatal(err)
	}
	request("GET", optionsPath, "", nil, 503)
	request("POST", "/api/v1/auth/registrations", "", map[string]any{
		"method": "password", "email": "other@example.test", "name": "Other", "password": "fixture-password",
	}, 503)
}

func assertSafeMailView(t *testing.T, w *httptest.ResponseRecorder, source string, version int64) {
	t.Helper()
	var out service.MailSettingsView
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Version != version || out.Source != source || !out.PasswordConfigured ||
		strings.Contains(w.Body.String(), `"password":`) || strings.Contains(w.Body.String(), "fixture-secret") {
		t.Fatalf("unexpected mail settings projection for source %s version %d", source, version)
	}
}

func TestInvitationDispatcherFollowsDatabaseMailPolicyWithoutRestart(t *testing.T) {
	ctx := t.Context()
	db := workspaceTestDB(t)
	auth := &service.AuthService{Store: &AuthStore{DB: db}}
	admin, _, err := auth.RegisterWithPassword(ctx, "outbox-admin@example.test", "Admin", "fixture-password", "smtp-fixture")
	if err != nil {
		t.Fatal(err)
	}
	actor := admin.ID
	settings := &service.MailSettingsService{
		Store: &MailSettingsStore{DB: db}, Secret: bytes.Repeat([]byte("x"), 32),
		Probe: func(context.Context, service.MailSettingsDraft) error { return nil },
	}
	ws := &service.WorkspaceService{Store: NewWorkspaceStore(db), ReadMailPolicy: settings.Policy,
		MailSecret: settings.Secret, PublicURL: "https://example.test"}
	workspace, err := ws.Create(ctx, actor, "Outbox", "")
	if err != nil {
		t.Fatal(err)
	}
	sink := &inviteSink{}
	d := &service.DeliveryDispatcher{Store: NewDeliveryStore(db), Workspaces: ws, Sender: sink,
		Secret: settings.Secret, ReadMailPolicy: settings.Policy}
	if _, err := ws.CreateInvitation(ctx, actor, workspace.ID, "first@example.test", "member"); err == nil {
		t.Fatal("mail enabled despite disabled fallback")
	}
	if ok, err := d.DispatchOnce(ctx); err != nil || ok || sink.calls != 0 {
		t.Fatal("cold disabled worker ran", ok, err)
	}
	draft := service.MailSettingsDraft{MailSettings: service.MailSettings{
		MailPolicy: service.MailPolicy{Enabled: true}, Address: "mail.example.test:465",
		Host: "mail.example.test", From: "sender@example.test", ImplicitTLS: true,
	}}
	on, err := settings.Save(ctx, actor, draft)
	if err != nil {
		t.Fatal(err)
	}
	first, err := ws.CreateInvitation(ctx, actor, workspace.ID, "first@example.test", "member")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := d.DispatchOnce(ctx); err != nil || !ok || sink.calls != 1 || sink.to != first.Email {
		t.Fatal("enabled worker did not send", ok, err)
	}
	pending, err := ws.CreateInvitation(ctx, actor, workspace.ID, "pending@example.test", "member")
	if err != nil {
		t.Fatal(err)
	}
	draft.Version, draft.Enabled = on.Version, false
	if _, err := settings.Save(ctx, actor, draft); err != nil {
		t.Fatal(err)
	}
	if ok, err := d.DispatchOnce(ctx); err != nil || ok || sink.calls != 1 {
		t.Fatal("disabled worker did not pause", ok, err)
	}
	var status string
	if err := db.QueryRowContext(ctx, `SELECT status FROM platform_invitation_deliveries WHERE invitation_id=$1`, pending.ID).Scan(&status); err != nil || status != "pending" {
		t.Fatal("paused delivery was claimed", status, err)
	}
}

func TestDatabaseMailDisablePreservesPasswordAdmin(t *testing.T) {
	ctx := t.Context()
	db := workspaceTestDB(t)
	otp := service.NewID()
	insertWorkspaceAccount(t, db, otp, "otp-admin@example.test")
	if _, err := db.ExecContext(ctx, `UPDATE platform_accounts SET is_platform_admin=true WHERE id=$1`, otp); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE platform_admin_state SET initialized_by=$1,initialized_at=now() WHERE singleton`, otp); err != nil {
		t.Fatal(err)
	}
	settings := &service.MailSettingsService{Store: &MailSettingsStore{DB: db}}
	_, err := settings.Save(ctx, otp, service.MailSettingsDraft{})
	assertPlatformPasswordAdminError(t, err)
	persisted, err := settings.Policy(ctx)
	if err != nil || persisted.Enabled {
		t.Fatal("rejected config unexpectedly changed policy", err)
	}
	auth := &service.AuthService{Store: &AuthStore{DB: db}}
	passwordAdmin, _, err := auth.RegisterWithPassword(ctx, "password-admin@example.test", "Admin", "fixture-password", "smtp-fixture")
	if err != nil {
		t.Fatal(err)
	}
	adminStore := &AdminStore{DB: db}
	granted, err := adminStore.Set(ctx, otp, passwordAdmin.ID, service.AdminPatch{Version: 1, IsPlatformAdmin: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := settings.Save(ctx, otp, service.MailSettingsDraft{}); err != nil {
		t.Fatal("password admin did not permit disabling email", err)
	}
	_, err = adminStore.Set(ctx, otp, passwordAdmin.ID, service.AdminPatch{Version: granted.Version, IsPlatformAdmin: false})
	assertPlatformPasswordAdminError(t, err)
	if _, err := adminStore.Set(ctx, passwordAdmin.ID, otp, service.AdminPatch{Version: 1, IsPlatformAdmin: false}); err != nil {
		t.Fatal("OTP admin should be removable while password admin remains", err)
	}
}

func assertPlatformPasswordAdminError(t *testing.T, err error) {
	t.Helper()
	var failure *service.Error
	if !errors.As(err, &failure) || failure.Code != "platform_admin_password_required" || failure.Status != 409 {
		t.Fatal("expected password admin protection", err)
	}
}
