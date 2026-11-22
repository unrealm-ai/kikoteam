package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type channelMemory struct {
	row    ChannelBinding
	actor  Member
	exists bool
}

func (m *channelMemory) WithWorkspace(ctx context.Context, _ string, f func(ChannelTx) error) error {
	return f(m)
}
func (m *channelMemory) Member(context.Context, string) (Member, error) { return m.actor, nil }
func (m *channelMemory) AgentExists(context.Context, string) error      { return nil }
func (m *channelMemory) Get(context.Context, string) (ChannelBinding, error) {
	if !m.exists {
		return ChannelBinding{}, Fail("channel_not_configured", 404, "missing")
	}
	return m.row, nil
}
func (m *channelMemory) Save(_ context.Context, b ChannelBinding, _ bool) error {
	m.row = b
	m.exists = true
	return nil
}
func (m *channelMemory) Snapshot(context.Context) (int64, []ChannelBinding, error) {
	return 1, []ChannelBinding{m.row}, nil
}
func (m *channelMemory) Applied(context.Context, string, ChannelApplied) error { return nil }
func (m *channelMemory) PendingNotification(context.Context) (int64, error)    { return 0, nil }
func (m *channelMemory) MarkNotified(context.Context, int64) error             { return nil }
func TestChannelIndependentVersionsAndEncryption(t *testing.T) {
	m := &channelMemory{actor: Member{Role: "owner", Status: "active"}}
	s := &ChannelService{Store: m, Secret: []byte(strings.Repeat("s", 32))}
	secret := "synthetic-channel-secret"
	q := ChannelPut{AppID: "cli_test", AppSecret: &secret, History: ChannelHistory{Limit: 100}, Access: ChannelAccess{Mode: "allowlist"}}
	a, e := s.Put(context.Background(), "11111111-1111-4111-8111-111111111111", "a", "22222222-2222-4222-8222-222222222222", q)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(a.Ciphertext), secret) {
		t.Fatal("plaintext")
	}
	encoded, e := json.Marshal(a)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatal("secret exposed")
	}
	q.Version = a.Revision
	q.AppSecret = nil
	q.Trigger.Keywords = []string{"hello"}
	b, e := s.Put(context.Background(), "11111111-1111-4111-8111-111111111111", "a", "22222222-2222-4222-8222-222222222222", q)
	if e != nil {
		t.Fatal(e)
	}
	if b.Generation != a.Generation || b.RulesVersion != a.RulesVersion+1 {
		t.Fatal("rule rotated credential")
	}
	got, e := s.open(b)
	if e != nil || got != secret {
		t.Fatal("secret not preserved", e)
	}
	q.Version = b.Revision
	q.AppID = "cli_other"
	if _, e = s.Put(context.Background(), "11111111-1111-4111-8111-111111111111", "a", "22222222-2222-4222-8222-222222222222", q); e == nil {
		t.Fatal("changed app without secret")
	}
	q.AppID = "cli_test"
	q.AppSecret = &secret
	c, e := s.Put(context.Background(), "11111111-1111-4111-8111-111111111111", "a", "22222222-2222-4222-8222-222222222222", q)
	if e != nil || c.Generation != b.Generation+1 || c.RulesVersion != b.RulesVersion {
		t.Fatal("bad credential version", e)
	}
	c.WorkspaceID = "other"
	if _, e = s.open(c); e == nil {
		t.Fatal("cross workspace ciphertext")
	}
	if _, e = s.Put(context.Background(), "11111111-1111-4111-8111-111111111111", "a", "22222222-2222-4222-8222-222222222222", q); e == nil {
		t.Fatal("stale write")
	}
	m.actor.Role = "member"
	if _, e = s.Get(context.Background(), "11111111-1111-4111-8111-111111111111", "a", "22222222-2222-4222-8222-222222222222"); e == nil {
		t.Fatal("member can read")
	}
}
func TestChannelValidation(t *testing.T) {
	q := ChannelPut{AppID: "cli_test", History: ChannelHistory{Limit: 101}, Access: ChannelAccess{Mode: "all"}}
	if validateChannel(&q) == nil {
		t.Fatal("oversize history")
	}
	q.History.Limit = 100
	q.Trigger.Regexes = []string{"("}
	if validateChannel(&q) == nil {
		t.Fatal("invalid regex")
	}
}
