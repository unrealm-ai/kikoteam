package service

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// InvitationDelivery is operational metadata, never a plaintext invitation credential.
type InvitationDelivery struct {
	InvitationID string
	Ciphertext   []byte
	Status       string
	ClaimID      string
}
type invitationMail struct {
	Recipient string `json:"recipient"`
	Link      string `json:"link"`
}

func mailCipher(secret []byte) (cipher.AEAD, error) {
	if len(secret) < 32 {
		return nil, workspaceFailure("email_delivery_unavailable", 503, "邀请邮件服务尚未配置")
	}
	key := sha256.Sum256(append([]byte("kiko:invitation-outbox:v1:"), secret...))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
func encryptInvitation(secret []byte, id, to, link string) ([]byte, error) {
	aead, err := mailCipher(secret)
	if err != nil {
		return nil, err
	}
	plain, err := json.Marshal(invitationMail{Recipient: to, Link: link})
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, plain, []byte(id)), nil
}
func decryptInvitation(secret []byte, d InvitationDelivery) (invitationMail, error) {
	var out invitationMail
	aead, err := mailCipher(secret)
	if err != nil {
		return out, err
	}
	if len(d.Ciphertext) < aead.NonceSize() {
		return out, errors.New("invalid mail ciphertext")
	}
	plain, err := aead.Open(nil, d.Ciphertext[:aead.NonceSize()], d.Ciphertext[aead.NonceSize():], []byte(d.InvitationID))
	if err != nil {
		return out, errors.New("mail ciphertext authentication failed")
	}
	if err = json.Unmarshal(plain, &out); err != nil {
		return out, errors.New("invalid encrypted mail payload")
	}
	return out, nil
}

// DeliverySender reports sent only when the mail server has accepted DATA, not delivery to an inbox.
// A connection failure after DATA may return unknown. Never return failed for uncertain acceptance.
type DeliverySender interface {
	SendInvitation(context.Context, string, string, string) (string, error)
}
type DeliveryStore interface {
	Claim(context.Context, time.Duration) (InvitationDelivery, bool, error)
	Complete(context.Context, InvitationDelivery, string) error
	AuthorizeSend(context.Context, InvitationDelivery, time.Duration) (bool, error)
}
type DeliveryDispatcher struct {
	ReadMailPolicy MailPolicyReader
	Store          DeliveryStore
	Workspaces     *WorkspaceService
	Sender         DeliverySender
	Secret         []byte
	PollInterval   time.Duration
	Lease          time.Duration
	OnError        func()
}

func (d *DeliveryDispatcher) DispatchOnce(ctx context.Context) (bool, error) {
	if d.ReadMailPolicy != nil {
		policy, err := d.ReadMailPolicy(ctx)
		if err != nil || !policy.Enabled {
			return false, err
		}
	}
	ctx, cancelDispatch := context.WithTimeout(ctx, 45*time.Second)
	defer cancelDispatch()
	lease := d.Lease
	if lease < time.Minute {
		lease = time.Minute
	}
	delivery, ok, err := d.Store.Claim(ctx, lease)
	if err != nil || !ok {
		return ok, err
	}
	// Claims are committed before querying workspace policy: no row/workspace inverse lock order.
	valid, err := d.Workspaces.deliveryAllowed(ctx, delivery.InvitationID)
	if err != nil {
		return true, err
	} // Lease expiry becomes unknown; never auto resend after an uncertain failure.
	if !valid {
		return true, d.Store.Complete(ctx, delivery, "cancelled")
	}
	payload, err := decryptInvitation(d.Secret, delivery)
	if err != nil {
		return true, errors.Join(err, d.Store.Complete(ctx, delivery, "failed"))
	}
	if err := ctx.Err(); err != nil {
		return true, err
	}
	authorized, err := d.Store.AuthorizeSend(ctx, delivery, lease)
	if err != nil {
		return true, err
	}
	if !authorized {
		return true, errors.New("invitation delivery claim lost before send")
	}
	sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	status, sendErr := d.Sender.SendInvitation(sendCtx, payload.Recipient, payload.Link, "kiko-invitation-"+delivery.InvitationID+"@notifications.kiko")
	if status != "sent" && status != "failed" && status != "unknown" {
		status = "unknown"
	}
	// If completion cannot be persisted, the sending lease eventually becomes unknown.
	completeErr := d.Store.Complete(ctx, delivery, status)
	if sendErr != nil {
		return true, errors.Join(fmt.Errorf("invitation send %s", status), completeErr)
	}
	return true, completeErr
}
func (d *DeliveryDispatcher) Run(ctx context.Context) error {
	interval := d.PollInterval
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			attemptCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
			_, err := d.DispatchOnce(attemptCtx)
			cancel()
			if err != nil && d.OnError != nil {
				d.OnError()
			}
		}
	}
}

// deliveryAllowed rechecks current invitation and inviter permission before external I/O.
func (s *WorkspaceService) deliveryAllowed(ctx context.Context, id string) (bool, error) {
	i, err := s.Store.FindInvitationByID(ctx, id)
	if errors.Is(err, ErrWorkspaceMissing) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	valid := false
	err = s.Store.WithWorkspace(ctx, i.WorkspaceID, func(tx WorkspaceTx) error {
		current, e := tx.Invitation(ctx, id)
		if e != nil {
			return e
		}
		if current.Kind != "email" || invitationStatus(current, s.now()) != "valid" {
			return nil
		}
		if e = s.verifyInviter(ctx, tx, current); e != nil {
			var denied *WorkspaceError
			if errors.As(e, &denied) {
				return nil
			}
			return e
		}
		valid = true
		return nil
	})
	return valid, err
}
func (s *WorkspaceService) limitInvitationMail(ctx context.Context, actor, recipient string) error {
	if err := s.Store.Allow(ctx, "invite-actor:"+HashToken(actor), 20, time.Hour); err != nil {
		return err
	}
	return s.Store.Allow(ctx, "invite-recipient:"+HashToken(recipient), 5, time.Hour)
}
func (s *WorkspaceService) RetryInvitationDelivery(ctx context.Context, actor, workspace, id string, ack bool) (Invitation, error) {
	var out Invitation
	enabled, policyErr := s.invitationMailEnabled(ctx)
	if policyErr != nil {
		return out, policyErr
	}
	if !enabled || len(s.MailSecret) < 32 {
		return out, workspaceFailure("email_delivery_unavailable", 503, "邀请邮件服务尚未配置")
	}
	check := func(tx WorkspaceTx) error {
		a, e := workspaceAuthorized(ctx, tx, actor, true)
		if e != nil {
			return e
		}
		out, e = tx.Invitation(ctx, id)
		if e != nil {
			return e
		}
		if a.Role != "owner" && out.Role == "admin" {
			return workspaceFailure("invitation_forbidden", 403, "不能重发管理员邀请")
		}
		if out.Kind != "email" {
			return workspaceFailure("not_email_invitation", 400, "此邀请不是邮箱邀请")
		}
		if invitationStatus(out, s.now()) != "valid" {
			return workspaceFailure("invitation_invalid", 410, "邀请已失效，请重新邀请")
		}
		if e = s.verifyInviter(ctx, tx, out); e != nil {
			return e
		}
		if out.DeliveryStatus == "unknown" && !ack {
			return workspaceFailure("delivery_unknown", 409, "发送结果未知，重试可能重复发送，请确认")
		}
		return nil
	}
	if err := s.Store.WithWorkspace(ctx, workspace, check); err != nil {
		return out, err
	}
	switch out.DeliveryStatus {
	case "pending", "sending", "sent":
		return out, nil
	}
	if err := s.limitInvitationMail(ctx, actor, out.Email); err != nil {
		return out, err
	}
	err := s.Store.WithWorkspace(ctx, workspace, func(tx WorkspaceTx) error {
		if e := check(tx); e != nil {
			return e
		}
		status, e := tx.RetryDelivery(ctx, id, ack)
		if e != nil {
			return e
		}
		out.DeliveryStatus = status
		return nil
	})
	return out, err
}
