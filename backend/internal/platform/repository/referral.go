package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type ReferralStore struct{ DB *sql.DB }

func (s *ReferralStore) Link(ctx context.Context, actor string, create bool) (out service.ReferralLink, err error) {
	if !create {
		err = s.DB.QueryRowContext(ctx, `SELECT settings.referral_rewards_enabled,
 CASE WHEN settings.referral_rewards_enabled THEN COALESCE(link.code::text, '') ELSE '' END
 FROM platform_credit_settings settings LEFT JOIN platform_referral_links link
 ON link.inviter_account_id=$1 AND link.revoked_at IS NULL WHERE settings.id=1`, actor).Scan(&out.Enabled, &out.Code)
		return
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback() //nolint:errcheck // Commit owns success.
	policy, err := lockReferralPolicy(ctx, tx)
	if err != nil || !policy.Enabled {
		return out, err
	}
	out.Enabled = true
	// The unique inviter key arbitrates simultaneous link creation.
	_, err = tx.ExecContext(ctx, `INSERT INTO platform_referral_links(code,inviter_account_id)
 VALUES($1,$2) ON CONFLICT(inviter_account_id) DO NOTHING`, service.NewID(), actor)
	if err != nil {
		return out, err
	}
	err = tx.QueryRowContext(ctx, `SELECT code::text FROM platform_referral_links
 WHERE inviter_account_id=$1 AND revoked_at IS NULL`, actor).Scan(&out.Code)
	if errors.Is(err, sql.ErrNoRows) {
		return out, service.Fail("referral_link_revoked", 409, "邀请链接已撤销")
	}
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}

func (s *ReferralStore) Settings(ctx context.Context, actor string) (out service.ReferralSettings, err error) {
	err = s.DB.QueryRowContext(ctx, `SELECT referral_rewards_enabled,referral_rewards_version
 FROM platform_credit_settings WHERE id=1 AND EXISTS
 (SELECT 1 FROM platform_accounts WHERE id=$1 AND is_platform_admin)`, actor).Scan(&out.Enabled, &out.Version)
	if errors.Is(err, sql.ErrNoRows) {
		err = service.Fail("platform_admin_required", 403, "需要平台管理员权限")
	}
	return
}

func (s *ReferralStore) SaveSettings(ctx context.Context, actor string, version int64, enabled bool) (out service.ReferralSettings, err error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback() //nolint:errcheck // Commit owns success.
	if err = requireSettingsAdmin(ctx, tx, actor, true); err != nil {
		return out, err
	}
	err = tx.QueryRowContext(ctx, `UPDATE platform_credit_settings SET referral_rewards_enabled=$2,
 referral_rewards_version=referral_rewards_version+CASE WHEN referral_rewards_enabled<>$2 THEN 1 ELSE 0 END
 WHERE id=1 AND referral_rewards_version=$1 RETURNING referral_rewards_enabled,referral_rewards_version`, version, enabled).Scan(&out.Enabled, &out.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return out, service.Fail("referral_settings_conflict", 409, "邀请奖励设置已更新，请刷新后重试")
	}
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}

func lockReferralPolicy(ctx context.Context, tx *sql.Tx) (out service.ReferralSettings, err error) {
	err = tx.QueryRowContext(ctx, `SELECT referral_rewards_enabled,referral_rewards_version
 FROM platform_credit_settings WHERE id=1 FOR SHARE`).Scan(&out.Enabled, &out.Version)
	return
}

func (t *authTx) recordReferral(ctx context.Context, a service.Account) error {
	code := service.RegistrationReferral(ctx)
	if code == "" || t.rewardReferral == nil {
		return nil
	}
	policy, err := lockReferralPolicy(ctx, t.tx)
	if err != nil || !policy.Enabled {
		return err
	}
	_, err = t.tx.ExecContext(ctx, `INSERT INTO platform_referral_attributions(new_account_id,referral_code,rewards_version)
 SELECT $1,code,$3 FROM platform_referral_links WHERE code=$2 AND revoked_at IS NULL AND inviter_account_id<>$1
 FOR SHARE ON CONFLICT(new_account_id) DO NOTHING`, a.ID, code, policy.Version)
	if err != nil || !a.EmailVerified {
		return err
	}
	return t.payReferral(ctx, a.ID, policy.Version)
}

func (t *authTx) settleReferral(ctx context.Context, id string) error {
	if t.rewardReferral == nil {
		return nil
	}
	var pending bool
	if err := t.tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM platform_referral_attributions
 WHERE new_account_id=$1 AND rewarded_at IS NULL)`, id).Scan(&pending); err != nil || !pending {
		return err
	}
	policy, err := lockReferralPolicy(ctx, t.tx)
	if err != nil || !policy.Enabled {
		return err
	}
	return t.payReferral(ctx, id, policy.Version)
}

func (t *authTx) payReferral(ctx context.Context, id string, version int64) error {
	var inviter string
	err := t.tx.QueryRowContext(ctx, `SELECT l.inviter_account_id FROM platform_referral_attributions a
 JOIN platform_referral_links l ON l.code=a.referral_code
 WHERE a.new_account_id=$1 AND a.rewarded_at IS NULL AND a.rewards_version=$2 AND l.revoked_at IS NULL
 FOR UPDATE OF a FOR SHARE OF l`, id, version).Scan(&inviter)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if err = t.rewardReferral(ctx, t.tx, inviter, id); err != nil {
		return err
	}
	_, err = t.tx.ExecContext(ctx, `UPDATE platform_referral_attributions SET rewarded_at=now() WHERE new_account_id=$1`, id)
	return err
}
