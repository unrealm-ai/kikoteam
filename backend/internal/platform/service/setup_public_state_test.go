package service

import (
	"testing"
	"time"
)

func TestSetupVerificationURLSurvivesOnlyLiveConfirmation(t *testing.T) {
	now := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	for _, state := range []string{"awaiting_confirmation", "polling", "connected", "failed", "expired", "interrupted", "ready"} {
		t.Run(state, func(t *testing.T) {
			want := state == "awaiting_confirmation" || state == "polling"
			ding := DingTalkSetupService{Now: func() time.Time { return now }}
			d := DingTalkRegistration{State: state, VerificationURL: "https://accounts.dingtalk.cn/confirm", ExpiresAt: now.Add(time.Minute), UpdatedAt: now}
			ding.publicState(&d)
			if (d.VerificationURL != "") != want {
				t.Fatal("DingTalk URL visibility", d.State, d.VerificationURL)
			}
			feishu := FeishuSetupService{Now: func() time.Time { return now }}
			f := FeishuRegistration{ID: "test", State: state, VerificationURL: "https://accounts.feishu.cn/confirm", ExpiresAt: now.Add(time.Minute), UpdatedAt: now}
			feishu.activeRegistrations.Store(f.ID, f)
			feishu.publicState(&f)
			if (f.VerificationURL != "") != want {
				t.Fatal("Feishu URL visibility", f.State, f.VerificationURL)
			}
		})
	}
	for _, expired := range []bool{true, false} {
		updated, expires := now, now.Add(time.Minute)
		if expired {
			expires = now.Add(-time.Second)
		} else {
			updated = now.Add(-91 * time.Second)
		}
		d := DingTalkRegistration{State: "polling", VerificationURL: "secret-challenge", ExpiresAt: expires, UpdatedAt: updated}
		(&DingTalkSetupService{Now: func() time.Time { return now }}).publicState(&d)
		f := FeishuRegistration{ID: "test", State: "polling", VerificationURL: "secret-challenge", ExpiresAt: expires, UpdatedAt: updated}
		s := FeishuSetupService{Now: func() time.Time { return now }}
		s.activeRegistrations.Store(f.ID, f)
		s.publicState(&f)
		if d.VerificationURL != "" || f.VerificationURL != "" {
			t.Fatal("expired or interrupted challenge leaked")
		}
	}
}
