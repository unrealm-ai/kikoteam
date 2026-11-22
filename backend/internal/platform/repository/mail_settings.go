package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type MailSettingsStore struct{ DB *sql.DB }

func (s *MailSettingsStore) Read(ctx context.Context) (r service.MailSettingsRecord, e error) {
	e = s.DB.QueryRowContext(ctx, `SELECT version, enabled, verification_required, address, host, username, sender, implicit_tls, password_ciphertext FROM platform_mail_settings WHERE singleton`).
		Scan(&r.Version, &r.Enabled, &r.VerificationRequired, &r.Address, &r.Host, &r.Username, &r.From, &r.ImplicitTLS, &r.Ciphertext)
	if errors.Is(e, sql.ErrNoRows) {
		e = nil
	}
	return r, e
}

func (s *MailSettingsStore) ReadPolicy(ctx context.Context, fallback service.MailPolicy) (service.MailPolicy, error) {
	return readMailPolicy(ctx, s.DB, fallback)
}

func readMailPolicy(ctx context.Context, db querier, fallback service.MailPolicy) (service.MailPolicy, error) {
	var policy service.MailPolicy
	err := db.QueryRowContext(ctx, `SELECT enabled, verification_required FROM platform_mail_settings WHERE singleton`).Scan(&policy.Enabled, &policy.VerificationRequired)
	if errors.Is(err, sql.ErrNoRows) {
		return fallback, nil
	}
	return policy, err
}

func (s *MailSettingsStore) AdminEmail(ctx context.Context, actor string) (string, error) {
	return mailAdminEmail(ctx, s.DB, actor, false)
}

func mailAdminEmail(ctx context.Context, db querier, actor string, lock bool) (string, error) {
	var email string
	query := `SELECT email FROM platform_accounts WHERE id=$1 AND is_platform_admin`
	if lock {
		query += ` FOR UPDATE`
	}
	err := db.QueryRowContext(ctx, query, actor).Scan(&email)
	if errors.Is(err, sql.ErrNoRows) {
		return "", service.Fail("platform_admin_required", 403, "需要平台管理员权限")
	}
	return email, err
}

func (s *MailSettingsStore) Save(ctx context.Context, actor string, version int64, r service.MailSettingsRecord) (service.MailSettingsRecord, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return service.MailSettingsRecord{}, err
	}
	defer tx.Rollback() //nolint:errcheck // Commit or the original error determines the result.
	// Follow platform-role writes' singleton/account lock order so concurrent
	// revocation is serialized with settings publication.
	if _, err = tx.ExecContext(ctx, `SELECT singleton FROM platform_admin_state WHERE singleton FOR UPDATE`); err != nil {
		return service.MailSettingsRecord{}, err
	}
	if _, err = mailAdminEmail(ctx, tx, actor, true); err != nil {
		return service.MailSettingsRecord{}, err
	}
	if !r.Enabled {
		if err = requirePasswordAdmin(ctx, tx, nil); err != nil {
			return service.MailSettingsRecord{}, err
		}
	}
	args := []any{r.Enabled, r.VerificationRequired, r.Address, r.Host, r.Username, r.From, r.ImplicitTLS, r.Ciphertext, actor}
	if version == 0 {
		err = tx.QueryRowContext(ctx, `INSERT INTO platform_mail_settings(singleton,version,enabled,verification_required,address,host,username,sender,implicit_tls,password_ciphertext,updated_by)
		 VALUES(true,1,$1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT (singleton) DO NOTHING RETURNING version`, args...).Scan(&r.Version)
	} else {
		err = tx.QueryRowContext(ctx, `UPDATE platform_mail_settings SET version=version+1,enabled=$1,verification_required=$2,address=$3,host=$4,username=$5,
		 sender=$6,implicit_tls=$7,password_ciphertext=$8,updated_by=$9,updated_at=now() WHERE singleton AND version=$10 RETURNING version`, append(args, version)...).Scan(&r.Version)
	}
	if errors.Is(err, sql.ErrNoRows) {
		err = service.MailSettingsConflict()
	}
	if err != nil {
		return service.MailSettingsRecord{}, err
	}
	return r, tx.Commit()
}

func requirePasswordAdmin(ctx context.Context, db querier, excluding *string) error {
	var available bool
	err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM platform_accounts
		WHERE is_platform_admin AND COALESCE(password_hash,'') <> '' AND ($1::uuid IS NULL OR id <> $1))`, excluding).Scan(&available)
	if err != nil {
		return err
	}
	if !available {
		return service.Fail("platform_admin_password_required", 409, "关闭邮件后须保留至少一位可用密码登录的平台管理员；请先设置密码或授权已有密码账号")
	}
	return nil
}

func preservePasswordAdminWhenMailDisabled(ctx context.Context, db querier, excluding string) error {
	var disabled bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM platform_mail_settings WHERE singleton AND NOT enabled)`).Scan(&disabled); err != nil {
		return err
	}
	if !disabled {
		return nil
	}
	return requirePasswordAdmin(ctx, db, &excluding)
}
