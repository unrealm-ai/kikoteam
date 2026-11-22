package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type DeliveryStore struct{ DB *sql.DB }

func NewDeliveryStore(db *sql.DB) *DeliveryStore { return &DeliveryStore{DB: db} }
func (s *DeliveryStore) Claim(ctx context.Context, lease time.Duration) (service.InvitationDelivery, bool, error) {
	var d service.InvitationDelivery
	// This terminal transition is safe to repeat across instances; never moves a stale send back to pending.
	if _, err := s.DB.ExecContext(ctx, `UPDATE platform_invitation_deliveries SET status='unknown',claim_id=NULL,lease_until=NULL,updated_at=clock_timestamp() WHERE status='sending' AND lease_until<clock_timestamp()`); err != nil {
		return d, false, err
	}
	claim := service.NewID()
	err := s.DB.QueryRowContext(ctx, `WITH candidate AS (SELECT invitation_id FROM platform_invitation_deliveries WHERE status='pending' ORDER BY created_at,invitation_id FOR UPDATE SKIP LOCKED LIMIT 1) UPDATE platform_invitation_deliveries d SET status='sending',claim_id=$1,lease_until=clock_timestamp()+($2*interval '1 second'),attempts=attempts+1,updated_at=clock_timestamp() FROM candidate c WHERE d.invitation_id=c.invitation_id RETURNING d.invitation_id,d.ciphertext,d.status,d.claim_id`, claim, lease.Seconds()).Scan(&d.InvitationID, &d.Ciphertext, &d.Status, &d.ClaimID)
	if errors.Is(err, sql.ErrNoRows) {
		return d, false, nil
	}
	return d, err == nil, err
}
func (s *DeliveryStore) Complete(ctx context.Context, d service.InvitationDelivery, status string) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE platform_invitation_deliveries SET status=$3,claim_id=NULL,lease_until=NULL,updated_at=clock_timestamp() WHERE invitation_id=$1 AND claim_id=$2 AND status='sending' AND lease_until>clock_timestamp()`, d.InvitationID, d.ClaimID, status)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return errors.New("invitation delivery claim lost")
	}
	return nil
}
func (t *workspaceTx) EnqueueDelivery(ctx context.Context, d service.InvitationDelivery) error {
	_, err := t.tx.ExecContext(ctx, `INSERT INTO platform_invitation_deliveries(invitation_id,ciphertext)VALUES($1,$2)`, d.InvitationID, d.Ciphertext)
	return err
}
func (t *workspaceTx) RetryDelivery(ctx context.Context, id string, ack bool) (string, error) {
	var status string
	err := t.tx.QueryRowContext(ctx, `SELECT status FROM platform_invitation_deliveries WHERE invitation_id=$1 FOR UPDATE`, id).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", service.Fail("delivery_unavailable", 409, "此历史邀请没有可重发的邮件，请重新邀请")
	}
	if err != nil {
		return "", err
	}
	switch status {
	case "pending", "sending", "sent":
		return status, nil
	case "unknown":
		if !ack {
			return "", service.Fail("delivery_unknown", 409, "发送结果未知，重试可能重复发送，请确认")
		}
	case "failed":
	default:
		return "", service.Fail("delivery_cancelled", 409, "邮件已取消，请重新邀请")
	}
	_, err = t.tx.ExecContext(ctx, `UPDATE platform_invitation_deliveries SET status='pending',claim_id=NULL,lease_until=NULL,updated_at=clock_timestamp() WHERE invitation_id=$1`, id)
	return "pending", err
}

func (s *WorkspaceStore) Allow(ctx context.Context, key string, limit int, window time.Duration) error {
	return (&AuthStore{DB: s.DB}).Allow(ctx, key, limit, window)
}

func (s *DeliveryStore) AuthorizeSend(ctx context.Context, d service.InvitationDelivery, lease time.Duration) (bool, error) {
	result, err := s.DB.ExecContext(ctx, `UPDATE platform_invitation_deliveries SET lease_until=clock_timestamp()+($3*interval '1 second'),updated_at=clock_timestamp() WHERE invitation_id=$1 AND claim_id=$2 AND status='sending' AND lease_until>clock_timestamp()`, d.InvitationID, d.ClaimID, lease.Seconds())
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}
