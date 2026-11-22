package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type credentialProbeFunc func(context.Context, string, string, string) error

func (f credentialProbeFunc) Verify(c context.Context, p, i, s string) error { return f(c, p, i, s) }

type credentialMemory struct{ channelMemory }

func (m *credentialMemory) WithWorkspace(ctx context.Context, _ string, f func(ChannelTx) error) error {
	return f(m)
}

func (m *credentialMemory) GetProvider(c context.Context, _ string, _ string) (ChannelBinding, error) {
	return m.Get(c, "")
}
func TestChannelCredentialEvidenceAndRebind(t *testing.T) {
	const w = "11111111-1111-4111-8111-111111111111"
	const employee = "22222222-2222-4222-8222-222222222222"
	for _, provider := range []string{"feishu", "dingtalk", "wecom"} {
		t.Run(provider, func(t *testing.T) {
			m := &credentialMemory{channelMemory: channelMemory{actor: Member{Role: "owner", Status: "active"}}}
			now := time.Now().UTC()
			calls := 0
			s := &ChannelService{Store: m, Secret: []byte(strings.Repeat("s", 32)), CredentialNow: func() time.Time { return now }, CredentialProbe: credentialProbeFunc(func(context.Context, string, string, string) error { calls++; return nil })}
			q := ChannelCredentialTest{AppID: "cli_test", AppSecret: "synthetic-secret"}
			evidence, e := s.TestCredentials(context.Background(), w, "actor", employee, provider, q)
			if e != nil {
				t.Fatal(e)
			}
			if m.exists {
				t.Fatal("validation persisted binding")
			}
			if provider == "wecom" {
				if calls != 0 || evidence.CredentialStatus != "format_only" || evidence.Receipt != "" {
					t.Fatal("wecom claimed authentication")
				}
			} else {
				if calls != 1 || evidence.CredentialStatus != "verified" || evidence.Receipt == "" {
					t.Fatal("no evidence")
				}
			}
			payload, err := json.Marshal(evidence)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(payload), q.AppSecret) {
				t.Fatal("secret leaked")
			}
			save := credentialTestPut(q)
			if provider != "wecom" {
				if _, e = s.PutManual(context.Background(), w, "actor", employee, provider, save, "", false); e == nil {
					t.Fatal("missing receipt accepted")
				}
				for _, args := range [][]string{{w, "other", employee, provider}, {"33333333-3333-4333-8333-333333333333", "actor", employee, provider}, {w, "actor", "44444444-4444-4444-8444-444444444444", provider}, {w, "actor", employee, "dingtalk"}} {
					if args[3] == provider && args[1] == "actor" && args[0] == w && args[2] == employee {
						continue
					}
					if s.verifyCredentialReceipt(args[0], args[1], args[2], args[3], q, evidence.Receipt) == nil {
						t.Fatal("cross-scope receipt")
					}
				}
				changed := q
				changed.AppSecret = "changed-secret"
				if s.verifyCredentialReceipt(w, "actor", employee, provider, changed, evidence.Receipt) == nil {
					t.Fatal("changed secret accepted")
				}
				now = now.Add(6 * time.Minute)
				if s.verifyCredentialReceipt(w, "actor", employee, provider, q, evidence.Receipt) == nil {
					t.Fatal("expired accepted")
				}
				now = now.Add(-6 * time.Minute)
			}
			saved, e := s.PutManual(context.Background(), w, "actor", employee, provider, save, evidence.Receipt, false)
			if e != nil {
				t.Fatal(e)
			}
			if saved.Applied.Status != "pending" || saved.IdentityConfirmed {
				t.Fatal("save fabricated connection")
			}
			if _, e = s.PutManual(context.Background(), w, "actor", employee, provider, save, evidence.Receipt, false); e == nil {
				t.Fatal("receipt replay")
			}
			q.Version = saved.Revision
			q.AppID = "cli_other"
			evidence, e = s.TestCredentials(context.Background(), w, "actor", employee, provider, q)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = s.PutManual(context.Background(), w, "actor", employee, provider, credentialTestPut(q), evidence.Receipt, false); e == nil {
				t.Fatal("rebind without confirmation")
			}
			rebound, e := s.PutManual(context.Background(), w, "actor", employee, provider, credentialTestPut(q), evidence.Receipt, true)
			if e != nil || rebound.IdentityEpoch != saved.IdentityEpoch+1 {
				t.Fatal("rebind", e)
			}
		})
	}
}
func TestChannelCredentialRechecksAuthorizationAndVersion(t *testing.T) {
	const w = "11111111-1111-4111-8111-111111111111"
	const employee = "22222222-2222-4222-8222-222222222222"
	for _, mode := range []string{"revoked", "changed", "provider_failed"} {
		t.Run(mode, func(t *testing.T) {
			m := &credentialMemory{channelMemory: channelMemory{actor: Member{Role: "owner", Status: "active"}}}
			s := &ChannelService{Store: m, Secret: []byte(strings.Repeat("s", 32))}
			s.CredentialProbe = credentialProbeFunc(func(context.Context, string, string, string) error {
				switch mode {
				case "revoked":
					m.actor.Role = "member"
				case "changed":
					m.exists = true
					m.row.Revision = 1
				case "provider_failed":
					return errors.New("probe failure")
				}
				return nil
			})
			r, e := s.TestCredentials(context.Background(), w, "actor", employee, "feishu", ChannelCredentialTest{AppID: "cli_test", AppSecret: "synthetic-secret"})
			if e == nil || r.Receipt != "" {
				t.Fatal("issued stale evidence")
			}
		})
	}
}

func TestManualChannelSecretRejectsPlaceholders(t *testing.T) {
	for _, v := range []string{"", "  ", "****", "••••"} {
		if validateManualChannelSecret(&v) == nil {
			t.Fatal("accepted placeholder")
		}
	}
	secret := "real-secret"
	if validateManualChannelSecret(&secret) != nil || validateManualChannelSecret(nil) != nil {
		t.Fatal("valid secret rejected")
	}
}
