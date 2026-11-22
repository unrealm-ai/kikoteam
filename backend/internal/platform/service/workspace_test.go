package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type memoryWorkspace struct {
	mu          sync.Mutex
	w           Workspace
	members     map[string]Member
	invitations map[string]Invitation
	emails      map[string]string
	redeemed    map[string]bool
	removed     map[string]time.Time
}

func fixtureWorkspace() *memoryWorkspace {
	return &memoryWorkspace{w: Workspace{ID: "workspace", Name: "Team", Version: 1}, members: map[string]Member{"owner": {ID: "owner", Role: "owner", Status: "active", Version: 1, Email: "owner@example.test"}, "admin": {ID: "admin", Role: "admin", Status: "active", Version: 1, Email: "admin@example.test"}, "member": {ID: "member", Role: "member", Status: "active", Version: 1, Email: "member@example.test"}}, invitations: map[string]Invitation{}, emails: map[string]string{"new": "new@example.test", "member": "member@example.test"}}
}
func (m *memoryWorkspace) List(context.Context, string, int, int) ([]Workspace, int, error) {
	return []Workspace{m.w}, 1, nil
}
func (m *memoryWorkspace) Create(_ context.Context, _ string, w Workspace) error { m.w = w; return nil }
func (m *memoryWorkspace) WithWorkspace(_ context.Context, id string, fn func(WorkspaceTx) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id != m.w.ID {
		return ErrWorkspaceMissing
	}
	return fn(m)
}
func (m *memoryWorkspace) FindInvitation(_ context.Context, hash string) (Invitation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, i := range m.invitations {
		if i.TokenHash == hash {
			return i, nil
		}
	}
	return Invitation{}, ErrWorkspaceMissing
}
func (m *memoryWorkspace) AccountEmail(_ context.Context, id string) (string, bool, error) {
	e, ok := m.emails[id]
	if !ok {
		return "", false, ErrWorkspaceMissing
	}
	return e, true, nil
}
func (m *memoryWorkspace) Workspace() Workspace { return m.w }
func (m *memoryWorkspace) Member(_ context.Context, id string) (Member, error) {
	v, ok := m.members[id]
	if !ok {
		return v, ErrWorkspaceMissing
	}
	return v, nil
}
func (m *memoryWorkspace) SaveWorkspace(_ context.Context, w Workspace) error { m.w = w; return nil }
func (m *memoryWorkspace) SaveMember(_ context.Context, v Member) error {
	m.members[v.ID] = v
	return nil
}
func (m *memoryWorkspace) DeleteMember(_ context.Context, id string) error {
	delete(m.members, id)
	return nil
}
func (m *memoryWorkspace) Members(context.Context, int, int) ([]Member, int, error) {
	return []Member{}, 0, nil
}
func (m *memoryWorkspace) Invitations(context.Context, int, int) ([]Invitation, int, error) {
	return []Invitation{}, 0, nil
}
func (m *memoryWorkspace) Invitation(_ context.Context, id string) (Invitation, error) {
	v, ok := m.invitations[id]
	if !ok {
		return v, ErrWorkspaceMissing
	}
	return v, nil
}
func (m *memoryWorkspace) InsertInvitation(_ context.Context, i Invitation) error {
	m.invitations[i.ID] = i
	return nil
}
func (m *memoryWorkspace) SetInvitationStatus(_ context.Context, id, status string) error {
	i := m.invitations[id]
	i.Status = status
	m.invitations[id] = i
	return nil
}
func errCode(t *testing.T, err error, want string) {
	t.Helper()
	var e *WorkspaceError
	if !errors.As(err, &e) || e.Code != want {
		t.Fatalf("want %s, got %v", want, err)
	}
}
func stringPtr(s string) *string { return &s }
func TestMemberProtectionAndCAS(t *testing.T) {
	for _, tc := range []struct {
		name, actor, target string
		p                   MemberPatch
		code                string
	}{
		{"owner protected", "admin", "owner", MemberPatch{Version: 1}, "member_protected"},
		{"self protected", "owner", "owner", MemberPatch{Status: stringPtr("disabled"), Version: 1}, "member_protected"},
		{"admin cannot promote", "admin", "member", MemberPatch{Role: stringPtr("admin"), Version: 1}, "invalid_role"},
		{"stale version", "owner", "member", MemberPatch{Version: 2}, "version_conflict"},
		{"member cannot manage", "member", "admin", MemberPatch{Version: 1}, "workspace_forbidden"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := fixtureWorkspace()
			s := WorkspaceService{EmailInvitationsEnabled: true, MailSecret: []byte("test-only-invitation-secret-32-bytes"), Store: m}
			_, err := s.UpdateMember(context.Background(), tc.actor, "workspace", tc.target, tc.p)
			errCode(t, err, tc.code)
		})
	}
	m := fixtureWorkspace()
	s := WorkspaceService{EmailInvitationsEnabled: true, MailSecret: []byte("test-only-invitation-secret-32-bytes"), Store: m}
	v, err := s.UpdateMember(context.Background(), "owner", "workspace", "member", MemberPatch{Role: stringPtr("admin"), Version: 1})
	if err != nil || v.Role != "admin" || v.Version != 2 {
		t.Fatalf("v=%+v err=%v", v, err)
	}
}
func TestInvitationIsPendingUntilAcceptAndReplayDoesNotUpgrade(t *testing.T) {
	ctx := context.Background()
	m := fixtureWorkspace()
	s := WorkspaceService{EmailInvitationsEnabled: true, MailSecret: []byte("test-only-invitation-secret-32-bytes"), Store: m, PublicURL: "https://example.test"}
	i, err := s.CreateInvitation(ctx, "owner", "workspace", "new@example.test", "member")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.members["new"]; ok {
		t.Fatal("creation prematurely added membership")
	}
	token := i.Link[len("https://example.test/?invitation_token="):]
	w, err := s.AcceptInvitation(ctx, "new", token)
	if err != nil || w.Role != "member" {
		t.Fatalf("%+v %v", w, err)
	}
	if m.invitations[i.ID].Status != "accepted" {
		t.Fatal("not consumed")
	}
	m.members["new"] = Member{ID: "new", Role: "admin", Status: "active", Version: 2}
	w, err = s.AcceptInvitation(ctx, "new", token)
	if err != nil || w.Role != "admin" {
		t.Fatalf("replay changed role: %+v %v", w, err)
	}
}
func TestInvitationEmailExpiryAndRevokedInviter(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	for _, mode := range []string{"wrong_email", "expired", "revoked_inviter", "disabled_member"} {
		t.Run(mode, func(t *testing.T) {
			m := fixtureWorkspace()
			s := WorkspaceService{EmailInvitationsEnabled: true, MailSecret: []byte("test-only-invitation-secret-32-bytes"), Store: m, Now: func() time.Time { return now }}
			i, err := s.CreateInvitation(ctx, "owner", "workspace", "new@example.test", "member")
			if err != nil {
				t.Fatal(err)
			}
			token := i.Link[len("/?invitation_token="):]
			code := "invitation_invalid"
			switch mode {
			case "wrong_email":
				m.emails["new"] = "other@example.test"
				code = "invitation_email_mismatch"
			case "expired":
				s.Now = func() time.Time { return now.Add(25 * time.Hour) }
			case "revoked_inviter":
				delete(m.members, "owner")
			case "disabled_member":
				m.members["new"] = Member{ID: "new", Role: "member", Status: "disabled", Version: 1}
				code = "member_disabled"
			}
			_, err = s.AcceptInvitation(ctx, "new", token)
			errCode(t, err, code)
			if m.invitations[i.ID].Status != "pending" {
				t.Fatal("invalid acceptance consumed invite")
			}
		})
	}
}
func TestUnknownAndExpiredInvitationInspection(t *testing.T) {
	ctx := context.Background()
	m := fixtureWorkspace()
	s := WorkspaceService{EmailInvitationsEnabled: true, MailSecret: []byte("test-only-invitation-secret-32-bytes"), Store: m}
	out, err := s.InspectInvitation(ctx, "forged")
	if err != nil || out.Status != "unknown" || out.ContactEmail != "" {
		t.Fatalf("%+v %v", out, err)
	}
	i, err := s.CreateInvitation(ctx, "owner", "workspace", "new@example.test", "member")
	if err != nil {
		t.Fatal(err)
	}
	s.Now = func() time.Time { return time.Now().Add(25 * time.Hour) }
	out, err = s.InspectInvitation(ctx, i.Link[len("/?invitation_token="):])
	if err != nil || out.Status != "expired" || out.ContactEmail != "owner@example.test" || out.EmailMasked == "new@example.test" {
		t.Fatalf("%+v %v", out, err)
	}
}
func TestConcurrentInvitationAcceptOneMembership(t *testing.T) {
	m := fixtureWorkspace()
	s := WorkspaceService{EmailInvitationsEnabled: true, MailSecret: []byte("test-only-invitation-secret-32-bytes"), Store: m}
	i, err := s.CreateInvitation(context.Background(), "owner", "workspace", "new@example.test", "member")
	if err != nil {
		t.Fatal(err)
	}
	token := i.Link[len("/?invitation_token="):]
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.AcceptInvitation(context.Background(), "new", token); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if len(m.members) != 4 || m.members["new"].Version != 1 {
		t.Fatal("duplicate membership")
	}
}

func (m *memoryWorkspace) Redeemed(_ context.Context, i, a string) (bool, error) {
	return m.redeemed[i+":"+a], nil
}
func (m *memoryWorkspace) RecordRedemption(_ context.Context, i, a string) error {
	if m.redeemed == nil {
		m.redeemed = map[string]bool{}
	}
	m.redeemed[i+":"+a] = true
	return nil
}
func (m *memoryWorkspace) RemovedMember(_ context.Context, a string) (time.Time, error) {
	return m.removed[a], nil
}
func (m *memoryWorkspace) RecordRemoval(_ context.Context, a string, at time.Time) error {
	if m.removed == nil {
		m.removed = map[string]time.Time{}
	}
	m.removed[a] = at
	return nil
}

func TestReusableLinkOptionsAndRemoval(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	m := fixtureWorkspace()
	s := WorkspaceService{EmailInvitationsEnabled: true, MailSecret: []byte("test-only-invitation-secret-32-bytes"), Store: m, Now: func() time.Time { return now }}
	for _, tc := range []struct {
		email, role, kind string
		days              int
	}{{"", "admin", "link", 7}, {"a@example.test", "member", "link", 7}, {"", "member", "link", 181}, {"", "member", "link", -1}, {"", "member", "other", 7}} {
		if _, err := s.CreateInvitationWithOptions(ctx, "owner", "workspace", tc.email, tc.role, tc.kind, tc.days); err == nil {
			t.Fatalf("accepted invalid %+v", tc)
		}
	}
	i, err := s.CreateInvitationWithOptions(ctx, "owner", "workspace", "", "member", "link", 0)
	if err != nil {
		t.Fatal(err)
	}
	if i.ExpiresAt.Sub(now) != 7*24*time.Hour || i.Kind != "link" {
		t.Fatalf("defaults %+v", i)
	}
	token := i.Link[len("/?invitation_token="):]
	for _, actor := range []string{"link-user-one", "link-user-two"} {
		if _, err = s.AcceptInvitation(ctx, actor, token); err != nil {
			t.Fatal(err)
		}
	}
	if m.invitations[i.ID].Status != "pending" {
		t.Fatal("multi-use link consumed globally")
	}
	now = now.Add(time.Hour)
	if err = s.RemoveMember(ctx, "owner", "workspace", "link-user-one", 1); err != nil {
		t.Fatal(err)
	}
	_, err = s.AcceptInvitation(ctx, "link-user-one", token)
	errCode(t, err, "member_removed")
	// A link created after removal represents a new administrator grant.
	now = now.Add(time.Hour)
	next, err := s.CreateInvitationWithOptions(ctx, "owner", "workspace", "", "member", "link", 180)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AcceptInvitation(ctx, "link-user-one", next.Link[len("/?invitation_token="):]); err != nil {
		t.Fatal(err)
	}
	if err = s.RevokeInvitation(ctx, "owner", "workspace", next.ID); err != nil {
		t.Fatal(err)
	}
	_, err = s.AcceptInvitation(ctx, "link-user-three", next.Link[len("/?invitation_token="):])
	errCode(t, err, "invitation_invalid")
}

func TestEmailInvitationCapability(t *testing.T) {
	ctx := context.Background()
	s := &WorkspaceService{Store: fixtureWorkspace(), EmailInvitationsEnabled: true, MailSecret: []byte("test-only-invitation-secret-32-bytes"), PublicURL: "https://example.test"}
	legacy, err := s.CreateInvitation(ctx, "owner", "workspace", "new@example.test", "member")
	if err != nil {
		t.Fatal(err)
	}
	s.EmailInvitationsEnabled = false
	if _, err = s.CreateInvitation(ctx, "owner", "workspace", "new@example.test", "member"); err == nil {
		t.Fatal("disabled email invite accepted")
	}
	if _, err = s.CreateInvitationWithOptions(ctx, "owner", "workspace", "", "member", "link", 7); err != nil {
		t.Fatal("link blocked", err)
	}
	if _, err = s.Invitations(ctx, "owner", "workspace", 1, 20); err != nil {
		t.Fatal("history blocked", err)
	}
	if err = s.RevokeInvitation(ctx, "owner", "workspace", legacy.ID); err != nil {
		t.Fatal("revoke blocked", err)
	}
	zero := &WorkspaceService{Store: fixtureWorkspace()}
	if _, err = zero.CreateInvitation(ctx, "owner", "workspace", "new@example.test", "member"); err == nil {
		t.Fatal("zero config enabled email")
	}
}

func (m *memoryWorkspace) Allow(context.Context, string, int, time.Duration) error { return nil }
func (m *memoryWorkspace) FindInvitationByID(_ context.Context, id string) (Invitation, error) {
	v, ok := m.invitations[id]
	if !ok {
		return v, ErrWorkspaceMissing
	}
	return v, nil
}
func (m *memoryWorkspace) EnqueueDelivery(_ context.Context, d InvitationDelivery) error {
	i := m.invitations[d.InvitationID]
	i.DeliveryStatus = "pending"
	m.invitations[d.InvitationID] = i
	return nil
}
func (m *memoryWorkspace) RetryDelivery(_ context.Context, id string, _ bool) (string, error) {
	i := m.invitations[id]
	i.DeliveryStatus = "pending"
	m.invitations[id] = i
	return "pending", nil
}

func (m *memoryWorkspace) SharedRecipient(_ context.Context, hash, email string) (Invitation, error) {
	for _, i := range m.invitations {
		if i.Shared && i.TokenHash == hash && i.Email == email {
			return i, nil
		}
	}
	return Invitation{}, ErrWorkspaceMissing
}
func (m *memoryWorkspace) RevokeShared(_ context.Context, hash string) error {
	for id, i := range m.invitations {
		if i.Shared && i.TokenHash == hash && i.Status == "pending" {
			i.Status = "revoked"
			m.invitations[id] = i
		}
	}
	return nil
}
