package repository

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type inviteSink struct {
	mu                          sync.Mutex
	calls                       int
	to, link, messageID, status string
	err                         error
}

func (s *inviteSink) SendInvitation(_ context.Context, to, link, messageID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.to = to
	s.link = link
	s.messageID = messageID
	if s.status == "" {
		return "sent", nil
	}
	return s.status, s.err
}
func TestInvitationOutboxAtomicEncryptedConcurrentDelivery(t *testing.T) {
	db := workspaceTestDB(t)
	ctx := context.Background()
	owner := service.NewID()
	insertWorkspaceAccount(t, db, owner, "outbox-owner@example.test")
	secret := []byte("test-only-invitation-secret-32-bytes")
	ws := &service.WorkspaceService{Store: NewWorkspaceStore(db), EmailInvitationsEnabled: true, MailSecret: secret, PublicURL: "https://kiko.example"}
	w, err := ws.Create(ctx, owner, "Outbox", "")
	if err != nil {
		t.Fatal(err)
	}
	inv, err := ws.CreateInvitation(ctx, owner, w.ID, "recipient@example.test", "member")
	if err != nil {
		t.Fatal(err)
	}
	if inv.DeliveryStatus != "pending" {
		t.Fatalf("status=%s", inv.DeliveryStatus)
	}
	var encrypted []byte
	if err = db.QueryRowContext(ctx, `SELECT ciphertext FROM platform_invitation_deliveries WHERE invitation_id=$1`, inv.ID).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encrypted, []byte(inv.Email)) || bytes.Contains(encrypted, []byte(inv.Link)) {
		t.Fatal("outbox contains plaintext credentials")
	}
	link, err := ws.CreateInvitationWithOptions(ctx, owner, w.ID, "", "member", "link", 7)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM platform_invitation_deliveries WHERE invitation_id=$1`, link.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("link queued email")
	}
	sink := &inviteSink{}
	dispatch := &service.DeliveryDispatcher{Store: NewDeliveryStore(db), Workspaces: ws, Sender: sink, Secret: secret}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := dispatch.DispatchOnce(ctx); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if sink.calls != 1 || sink.to != inv.Email || sink.link != inv.Link || !strings.Contains(sink.messageID, inv.ID) {
		t.Fatalf("unexpected sink %+v", sink)
	}
	out, err := ws.RetryInvitationDelivery(ctx, owner, w.ID, inv.ID, false)
	if err != nil || out.DeliveryStatus != "sent" || out.Link != "" {
		t.Fatalf("retry leaked or resent %+v %v", out, err)
	}
	if ok, err := dispatch.DispatchOnce(ctx); err != nil || ok {
		t.Fatalf("sent retry queued: %v %v", ok, err)
	}
}
func TestInvitationOutboxUnknownAndManualRetry(t *testing.T) {
	db := workspaceTestDB(t)
	ctx := context.Background()
	owner := service.NewID()
	insertWorkspaceAccount(t, db, owner, "unknown-owner@example.test")
	secret := []byte("test-only-invitation-secret-32-bytes")
	ws := &service.WorkspaceService{Store: NewWorkspaceStore(db), EmailInvitationsEnabled: true, MailSecret: secret}
	w, err := ws.Create(ctx, owner, "Unknown", "")
	if err != nil {
		t.Fatal(err)
	}
	inv, err := ws.CreateInvitation(ctx, owner, w.ID, "unknown-recipient@example.test", "member")
	if err != nil {
		t.Fatal(err)
	}
	store := NewDeliveryStore(db)
	claim, ok, err := store.Claim(ctx, time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim %v %v", ok, err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE platform_invitation_deliveries SET lease_until=now()-interval '1 second' WHERE invitation_id=$1`, inv.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, err = store.Claim(ctx, time.Minute); err != nil || ok {
		t.Fatal("expired sending automatically reclaimed")
	}
	if allowed, e := store.AuthorizeSend(ctx, claim, time.Minute); e != nil || allowed {
		t.Fatalf("stale worker authorized: %v %v", allowed, e)
	}
	if err = store.Complete(ctx, claim, "sent"); err == nil {
		t.Fatal("stale claim completed")
	}
	if _, err = ws.RetryInvitationDelivery(ctx, owner, w.ID, inv.ID, false); err == nil {
		t.Fatal("unknown retried without acknowledgement")
	}
	out, err := ws.RetryInvitationDelivery(ctx, owner, w.ID, inv.ID, true)
	if err != nil || out.DeliveryStatus != "pending" || out.Link != "" {
		t.Fatalf("retry %+v %v", out, err)
	}
	sink := &inviteSink{status: "failed", err: errors.New("simulated definitive failure")}
	d := &service.DeliveryDispatcher{Store: store, Workspaces: ws, Sender: sink, Secret: secret}
	if _, err = d.DispatchOnce(ctx); err == nil {
		t.Fatal("missing send error")
	}
	if ok, err = d.DispatchOnce(ctx); err != nil || ok || sink.calls != 1 {
		t.Fatal("failed delivery automatically retried")
	}
	if _, err = ws.RetryInvitationDelivery(ctx, owner, w.ID, inv.ID, false); err != nil {
		t.Fatal(err)
	}
	sink.status = "sent"
	sink.err = nil
	if _, err = d.DispatchOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if sink.calls != 2 {
		t.Fatalf("calls=%d", sink.calls)
	}
}
func TestInvitationOutboxCancelsRevokedAndRejectsTampering(t *testing.T) {
	db := workspaceTestDB(t)
	ctx := context.Background()
	owner := service.NewID()
	insertWorkspaceAccount(t, db, owner, "cancel-owner@example.test")
	secret := []byte("test-only-invitation-secret-32-bytes")
	ws := &service.WorkspaceService{Store: NewWorkspaceStore(db), EmailInvitationsEnabled: true, MailSecret: secret}
	w, err := ws.Create(ctx, owner, "Cancelled", "")
	if err != nil {
		t.Fatal(err)
	}
	inv, err := ws.CreateInvitation(ctx, owner, w.ID, "cancel-recipient@example.test", "member")
	if err != nil {
		t.Fatal(err)
	}
	if err = ws.RevokeInvitation(ctx, owner, w.ID, inv.ID); err != nil {
		t.Fatal(err)
	}
	sink := &inviteSink{}
	d := &service.DeliveryDispatcher{Store: NewDeliveryStore(db), Workspaces: ws, Sender: sink, Secret: secret}
	if _, err = d.DispatchOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if sink.calls != 0 {
		t.Fatal("revoked invitation sent")
	}
	var state string
	if err = db.QueryRowContext(ctx, `SELECT status FROM platform_invitation_deliveries WHERE invitation_id=$1`, inv.ID).Scan(&state); err != nil || state != "cancelled" {
		t.Fatalf("state %s %v", state, err)
	}
	corrupt, err := ws.CreateInvitation(ctx, owner, w.ID, "corrupt-recipient@example.test", "member")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE platform_invitation_deliveries SET ciphertext='corrupt' WHERE invitation_id=$1`, corrupt.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = d.DispatchOnce(ctx); err == nil {
		t.Fatal("tampered payload sent")
	}
	if sink.calls != 0 {
		t.Fatal("tampered email reached sender")
	}
}

func TestInvitationOutboxEnqueueFailureRollsBackInvitation(t *testing.T) {
	db := workspaceTestDB(t)
	ctx := context.Background()
	owner := service.NewID()
	insertWorkspaceAccount(t, db, owner, "atomic-owner@example.test")
	ws := &service.WorkspaceService{Store: NewWorkspaceStore(db), EmailInvitationsEnabled: true, MailSecret: []byte("test-only-invitation-secret-32-bytes")}
	w, err := ws.Create(ctx, owner, "Atomic", "")
	if err != nil {
		t.Fatal(err)
	}
	// Force only the enqueue operation to fail after the invitation insert.
	if _, err = db.ExecContext(ctx, `ALTER TABLE platform_invitation_deliveries ADD CONSTRAINT test_reject_outbox CHECK (false)`); err != nil {
		t.Fatal(err)
	}
	if _, err = ws.CreateInvitation(ctx, owner, w.ID, "atomic-recipient@example.test", "member"); err == nil {
		t.Fatal("enqueue failure reported success")
	}
	var count int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM platform_invitations WHERE workspace_id=$1`, w.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial invitation persisted count=%d err=%v", count, err)
	}
}
