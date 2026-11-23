package service

import (
	"bytes"
	"encoding/base64"
	"testing"
	"time"
)

func TestSMTPReceiptBindsActorVersionAndTransport(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	s := &MailSettingsService{Secret: bytes.Repeat([]byte("s"), 32)}
	d := MailSettingsDraft{Version: 7, Password: "test-credential-not-in-wire", MailSettings: MailSettings{
		MailPolicy: MailPolicy{Enabled: true, VerificationRequired: true}, Address: "smtp.example.test:465",
		Host: "smtp.example.test", Username: "mail@example.test", From: "mail@example.test", ImplicitTLS: true,
	}}
	receipt, err := s.issueMailTestReceipt("admin-a", d, now)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(receipt)
	if err != nil || len(raw) != mailTestReceiptPayloadSize || bytes.Contains(raw, []byte(d.Password)) {
		t.Fatal("SMTP receipt leaked input or has an invalid format")
	}
	d.TestReceipt = receipt
	d.VerificationRequired = false
	if !s.validMailTestReceipt("admin-a", d, now.Add(9*time.Minute)) {
		t.Fatal("unchanged profile and changed registration policy should use same receipt")
	}
	for name, change := range map[string]func(*MailSettingsDraft){
		"version":  func(d *MailSettingsDraft) { d.Version++ },
		"endpoint": func(d *MailSettingsDraft) { d.Address = "other.example.test:465" },
		"host":     func(d *MailSettingsDraft) { d.Host = "other.example.test" },
		"username": func(d *MailSettingsDraft) { d.Username = "other@example.test" },
		"from":     func(d *MailSettingsDraft) { d.From = "other@example.test" },
		"tls":      func(d *MailSettingsDraft) { d.ImplicitTLS = false },
		"password": func(d *MailSettingsDraft) { d.Password += "changed" },
		"tamper":   func(d *MailSettingsDraft) { d.TestReceipt = "!" + d.TestReceipt[1:] },
	} {
		t.Run(name, func(t *testing.T) {
			modified := d
			change(&modified)
			if s.validMailTestReceipt("admin-a", modified, now) {
				t.Fatal("wrong or tampered candidate accepted")
			}
		})
	}
	if s.validMailTestReceipt("admin-b", d, now) || s.validMailTestReceipt("admin-a", d, now.Add(mailTestReceiptLifetime)) {
		t.Fatal("wrong administrator or expired receipt accepted")
	}
}
