package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestInvitationMailCipherBindsInvitationID(t *testing.T) {
	secret := []byte("test-only-outbox-secret-with-32-bytes")
	sealed, err := encryptInvitation(secret, "invite-1", "person@example.test", "https://kiko.example/?invitation_token=secret")
	if err != nil {
		t.Fatal(err)
	}
	d := InvitationDelivery{InvitationID: "invite-1", Ciphertext: sealed}
	payload, err := decryptInvitation(secret, d)
	if err != nil || payload.Recipient != "person@example.test" {
		t.Fatalf("%+v %v", payload, err)
	}
	d.InvitationID = "invite-2"
	if _, err = decryptInvitation(secret, d); err == nil {
		t.Fatal("ciphertext transplanted across invitation IDs")
	}
}

type unavailableDeliveryStore struct{ calls atomic.Int32 }

func (s *unavailableDeliveryStore) Claim(context.Context, time.Duration) (InvitationDelivery, bool, error) {
	s.calls.Add(1)
	return InvitationDelivery{}, false, errors.New("temporary database unavailable")
}
func (s *unavailableDeliveryStore) Complete(context.Context, InvitationDelivery, string) error {
	return nil
}
func TestDeliveryRunRecoversAfterDependencyErrors(t *testing.T) {
	store := &unavailableDeliveryStore{}
	var reported atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	d := DeliveryDispatcher{Store: store, PollInterval: time.Millisecond, OnError: func() {
		if reported.Add(1) == 3 {
			cancel()
		}
	}}
	go func() { done <- d.Run(ctx) }()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ended before cancellation: %v", err)
		}
	case <-time.After(time.Second):
		cancel()
		t.Fatal("dispatcher did not shut down")
	}
	if store.calls.Load() < 3 {
		t.Fatal("dispatcher stopped on first error")
	}
}

func (s *unavailableDeliveryStore) AuthorizeSend(context.Context, InvitationDelivery, time.Duration) (bool, error) {
	return false, nil
}

type fixedDeliveryStore struct {
	delivery     InvitationDelivery
	authorized   bool
	seenDeadline bool
}

func (s *fixedDeliveryStore) Claim(ctx context.Context, _ time.Duration) (InvitationDelivery, bool, error) {
	deadline, ok := ctx.Deadline()
	s.seenDeadline = ok && time.Until(deadline) <= 45*time.Second
	return s.delivery, true, nil
}
func (s *fixedDeliveryStore) Complete(context.Context, InvitationDelivery, string) error { return nil }
func (s *fixedDeliveryStore) AuthorizeSend(context.Context, InvitationDelivery, time.Duration) (bool, error) {
	s.authorized = true
	return true, nil
}

type countedInvitationSender struct{ calls int }

func (s *countedInvitationSender) SendInvitation(context.Context, string, string, string) (string, error) {
	s.calls++
	return "sent", nil
}

type slowWorkspaceStore struct{ *memoryWorkspace }

func (s *slowWorkspaceStore) WithWorkspace(ctx context.Context, _ string, _ func(WorkspaceTx) error) error {
	<-ctx.Done()
	return ctx.Err()
}
func TestDispatchCancelledOrSlowPolicyNeverSends(t *testing.T) {
	for _, slow := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancelled", true: "slow_policy"}[slow], func(t *testing.T) {
			secret := []byte("test-only-outbox-secret-with-32-bytes")
			m := fixtureWorkspace()
			i := Invitation{ID: "inv", WorkspaceID: "workspace", InviterID: "owner", Kind: "email", Role: "member", Status: "pending", ExpiresAt: time.Now().Add(time.Hour)}
			m.invitations[i.ID] = i
			ciphertext, err := encryptInvitation(secret, i.ID, "to@example.test", "https://example.test/invite")
			if err != nil {
				t.Fatal(err)
			}
			ws := &WorkspaceService{Store: m}
			if slow {
				ws.Store = &slowWorkspaceStore{m}
			}
			store := &fixedDeliveryStore{delivery: InvitationDelivery{InvitationID: i.ID, Ciphertext: ciphertext, Status: "sending", ClaimID: "claim"}}
			sender := &countedInvitationSender{}
			d := DeliveryDispatcher{Store: store, Workspaces: ws, Sender: sender, Secret: secret}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
			defer cancel()
			if !slow {
				cancel()
			}
			if _, err = d.DispatchOnce(ctx); err == nil {
				t.Fatal("cancellation ignored")
			}
			if sender.calls != 0 || store.authorized {
				t.Fatal("cancelled policy reached sending")
			}
			if !store.seenDeadline {
				t.Fatal("DispatchOnce did not apply bounded deadline")
			}
		})
	}
}
