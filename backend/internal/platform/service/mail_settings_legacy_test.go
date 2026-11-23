package service

import (
	"context"
	"errors"
	"testing"
)

type legacySMTPStore struct{ record MailSettingsRecord }

func (s *legacySMTPStore) Read(context.Context) (MailSettingsRecord, error) { return s.record, nil }
func (s *legacySMTPStore) ReadPolicy(context.Context, MailPolicy) (MailPolicy, error) {
	return s.record.MailPolicy, nil
}
func (s *legacySMTPStore) AdminEmail(context.Context, string) (string, error) {
	return "admin@example.test", nil
}
func (s *legacySMTPStore) Save(_ context.Context, _ string, _ int64, value MailSettingsRecord) (MailSettingsRecord, error) {
	value.Version = s.record.Version + 1
	s.record = value
	return value, nil
}

func TestLegacySMTPOnlyRuntimeAndDisable(t *testing.T) {
	ctx := t.Context()
	old := MailSettings{Address: "127.0.0.1:2525", Host: "relay.internal", Username: "legacy-configured", From: "sender@example.test", MailPolicy: MailPolicy{Enabled: true}}
	store := &legacySMTPStore{}
	svc := &MailSettingsService{Store: store, Secret: []byte("fixture-secret-not-for-real-apps-0123456789"), Fallback: MailSettingsDraft{MailSettings: old, Password: "old-fixture"}}
	for _, fromDB := range []bool{false, true} {
		if fromDB {
			sealed, err := svc.seal("old-fixture")
			if err != nil {
				t.Fatal(err)
			}
			store.record = MailSettingsRecord{MailSettings: old, Version: 4, Ciphertext: sealed}
		}
		runtime, err := svc.Runtime(ctx)
		if err != nil || runtime.MailSettings != old || runtime.Password != "old-fixture" {
			t.Fatalf("runtime changed: %v", err)
		}
		draft := MailSettingsDraft{MailSettings: old, Version: store.record.Version}
		if _, err = svc.Test(ctx, "admin", draft); !legacySMTPInvalid(err) {
			t.Fatalf("legacy candidate reached test: %v", err)
		}
		if _, err = svc.Save(ctx, "admin", draft); !legacySMTPInvalid(err) {
			t.Fatalf("legacy candidate reached save: %v", err)
		}
		draft.Enabled = false
		confused := draft
		confused.Host = "other.relay.internal"
		if _, err = svc.Save(ctx, "admin", confused); !legacySMTPInvalid(err) {
			t.Fatalf("disable rewrites settings: %v", err)
		}
		saved, err := svc.Save(ctx, "admin", draft)
		if err != nil || saved.MailSettings != draft.MailSettings || !saved.PasswordConfigured {
			t.Fatalf("disable lost connection: %+v %v", saved, err)
		}
		secret, err := svc.open(store.record.Ciphertext)
		if err != nil || secret != "old-fixture" {
			t.Fatalf("disable lost stored credentials: %v", err)
		}
	}
}

func legacySMTPInvalid(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == "invalid_mail_settings"
}
