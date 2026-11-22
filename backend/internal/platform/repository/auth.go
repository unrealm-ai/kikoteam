// Package repository provides platform PostgreSQL persistence adapters.
package repository

import (
	"context"
	"database/sql"
	"errors"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"time"
)

type AuthStore struct {
	DB           *sql.DB
	MailFallback *service.MailPolicy
	InitCredits  func(context.Context, *sql.Tx, string) error
}
type authTx struct {
	tx          *sql.Tx
	initCredits func(context.Context, *sql.Tx, string) error
}

func (s *AuthStore) Allow(ctx context.Context, key string, limit int, window time.Duration) error {
	var n int
	err := s.DB.QueryRowContext(ctx, `INSERT INTO platform_rate_limits(key,window_end,count) VALUES($1,now()+($2*interval '1 second'),1) ON CONFLICT(key) DO UPDATE SET count=CASE WHEN platform_rate_limits.window_end<=now() THEN 1 ELSE platform_rate_limits.count+1 END,window_end=CASE WHEN platform_rate_limits.window_end<=now() THEN excluded.window_end ELSE platform_rate_limits.window_end END RETURNING count`, key, window.Seconds()).Scan(&n)
	if err != nil {
		return err
	}
	if n > limit {
		return service.Fail("rate_limited", 429, "请求过于频繁，请稍后重试")
	}
	return nil
}
func (s *AuthStore) Issue(ctx context.Context, c service.Challenge) error {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback() //nolint:errcheck // Best-effort cleanup after commit or original transaction failure.
	_, e = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, c.Email+":"+c.Purpose)
	if e != nil {
		return e
	}
	var recent bool
	e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM platform_email_challenges WHERE email=$1 AND purpose=$2 AND created_at>now()-interval '60 seconds')`, c.Email, c.Purpose).Scan(&recent)
	if e != nil {
		return e
	}
	if recent {
		return service.Fail("resend_too_soon", 429, "请稍后重新发送验证码")
	}
	_, e = tx.ExecContext(ctx, `UPDATE platform_email_challenges SET consumed_at=now() WHERE email=$1 AND purpose=$2 AND consumed_at IS NULL`, c.Email, c.Purpose)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO platform_email_challenges(id,email,purpose,code_hash,expires_at) VALUES($1,$2,$3,$4,$5)`, c.ID, c.Email, c.Purpose, c.Hash, c.ExpiresAt)
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (s *AuthStore) CancelChallenge(ctx context.Context, id string) error {
	_, e := s.DB.ExecContext(ctx, `UPDATE platform_email_challenges SET consumed_at=now() WHERE id=$1`, id)
	return e
}
func (s *AuthStore) Consume(ctx context.Context, c service.Challenge, fn func(service.AuthTx) error) error {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback() //nolint:errcheck // Best-effort cleanup after commit or original transaction failure.
	var hash string
	var attempts int
	var valid bool
	if e = lockAdminInitialization(ctx, tx); e != nil {
		return e
	}
	if s.MailFallback != nil {
		policy, err := readMailPolicy(ctx, tx, *s.MailFallback)
		if err != nil {
			return err
		}
		if !policy.Enabled {
			return service.Fail("email_code_disabled", 403, "当前部署未启用邮箱验证码，请使用密码登录或联系管理员")
		}
	}
	e = tx.QueryRowContext(ctx, `SELECT code_hash,attempts, consumed_at IS NULL AND expires_at>now() FROM platform_email_challenges WHERE id::text=$1 AND email=$2 AND purpose=$3 FOR UPDATE`, c.ID, c.Email, c.Purpose).Scan(&hash, &attempts, &valid)
	if errors.Is(e, sql.ErrNoRows) {
		return invalidCode()
	}
	if e != nil {
		return e
	}
	if !valid || attempts >= 5 {
		return invalidCode()
	}
	if hash != c.Hash {
		_, e = tx.ExecContext(ctx, `UPDATE platform_email_challenges SET attempts=attempts+1 WHERE id::text=$1`, c.ID)
		if e != nil {
			return e
		}
		if e = tx.Commit(); e != nil {
			return e
		}
		return invalidCode()
	}
	if e = fn(&authTx{tx: tx, initCredits: s.InitCredits}); e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `UPDATE platform_email_challenges SET consumed_at=now() WHERE id::text=$1`, c.ID)
	if e != nil {
		return e
	}
	return tx.Commit()
}
func invalidCode() error {
	return service.Fail("invalid_code", 400, "验证码错误、已使用或已过期")
}

type querier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func findAccount(ctx context.Context, q querier, email string) (service.Account, error) {
	var a service.Account
	e := q.QueryRowContext(ctx, `SELECT id,email,name,email_verified,coalesce(password_hash,'') FROM platform_accounts WHERE email=$1`, email).Scan(&a.ID, &a.Email, &a.Name, &a.EmailVerified, &a.PasswordHash)
	if errors.Is(e, sql.ErrNoRows) {
		return a, service.Fail("invalid_credentials", 401, "账号不存在或凭证无效")
	}
	return a, e
}
func (s *AuthStore) FindAccount(ctx context.Context, email string) (service.Account, error) {
	return findAccount(ctx, s.DB, email)
}
func (t *authTx) FindAccount(ctx context.Context, email string) (service.Account, error) {
	var a service.Account
	e := t.tx.QueryRowContext(ctx, `SELECT id,email,name,email_verified,coalesce(password_hash,'') FROM platform_accounts WHERE email=$1 FOR UPDATE`, email).Scan(&a.ID, &a.Email, &a.Name, &a.EmailVerified, &a.PasswordHash)
	if errors.Is(e, sql.ErrNoRows) {
		return a, service.Fail("invalid_credentials", 401, "账号不存在或凭证无效")
	}
	return a, e
}
func (t *authTx) CreateAccount(ctx context.Context, a service.Account) error {
	_, e := t.tx.ExecContext(ctx, `INSERT INTO platform_accounts(id,email,name,password_hash,email_verified) VALUES($1,$2,$3,NULLIF($4,''),$5)`, a.ID, a.Email, a.Name, a.PasswordHash, a.EmailVerified)
	var pe *pgconn.PgError
	if errors.As(e, &pe) && pe.Code == "23505" {
		return service.Fail("account_exists", 409, "该邮箱已注册，请登录")
	}
	if e == nil && t.initCredits != nil {
		return t.initCredits(ctx, t.tx, a.ID)
	}
	return e
}
func (t *authTx) SetPassword(ctx context.Context, id, hash string) error {
	_, e := t.tx.ExecContext(ctx, `UPDATE platform_accounts SET password_hash=$2 WHERE id=$1`, id, hash)
	return e
}
func (t *authTx) RevokeSessions(ctx context.Context, id string) error {
	_, e := t.tx.ExecContext(ctx, `DELETE FROM platform_sessions WHERE account_id=$1`, id)
	return e
}
func createSession(ctx context.Context, q querier, hash, id string, expiry time.Time) error {
	if _, e := q.ExecContext(ctx, `WITH claim AS (UPDATE platform_admin_state SET initialized_by=$1,initialized_at=now() WHERE singleton AND initialized_at IS NULL RETURNING initialized_by) UPDATE platform_accounts SET is_platform_admin=true,platform_admin_version=platform_admin_version+1 WHERE id IN (SELECT initialized_by FROM claim)`, id); e != nil {
		return e
	}
	_, e := q.ExecContext(ctx, `INSERT INTO platform_sessions(token_hash,account_id,expires_at) VALUES($1,$2,$3)`, hash, id, expiry)
	return e
}
func (s *AuthStore) CreateSession(ctx context.Context, hash, id string, expiry time.Time) error {
	return createSession(ctx, s.DB, hash, id, expiry)
}
func (t *authTx) CreateSession(ctx context.Context, hash, id string, expiry time.Time) error {
	return createSession(ctx, t.tx, hash, id, expiry)
}
func (s *AuthStore) SessionAccount(ctx context.Context, hash string) (service.Account, error) {
	var a service.Account
	e := s.DB.QueryRowContext(ctx, `SELECT a.id,a.email,a.name,a.email_verified,a.is_platform_admin,a.platform_admin_version,a.created_at FROM platform_accounts a JOIN platform_sessions s ON s.account_id=a.id WHERE s.token_hash=$1 AND s.expires_at>now()`, hash).Scan(&a.ID, &a.Email, &a.Name, &a.EmailVerified, &a.IsPlatformAdmin, &a.PlatformAdminVersion, &a.CreatedAt)
	if errors.Is(e, sql.ErrNoRows) {
		return a, service.Fail("unauthenticated", 401, "请先登录")
	}
	return a, e
}
func (s *AuthStore) DeleteSession(ctx context.Context, hash string) error {
	_, e := s.DB.ExecContext(ctx, `DELETE FROM platform_sessions WHERE token_hash=$1`, hash)
	return e
}

// PasswordSession serializes session issuance with password reset. The hash
// verified outside the lock must still be current when the session is committed.
func (s *AuthStore) PasswordSession(ctx context.Context, email, expectedHash, token string, expiry time.Time) error {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback() //nolint:errcheck // Best-effort cleanup after commit or original transaction failure.
	if e = lockAdminInitialization(ctx, tx); e != nil {
		return e
	}
	a, e := (&authTx{tx: tx, initCredits: s.InitCredits}).FindAccount(ctx, email)
	if e != nil {
		return e
	}
	if a.PasswordHash != expectedHash {
		return service.Fail("invalid_credentials", 401, "邮箱或密码错误")
	}
	if e = createSession(ctx, tx, token, a.ID, expiry); e != nil {
		return e
	}
	return tx.Commit()
}

// CheckChallenge rejects invalid requests before expensive password hashing.
// Consume repeats this check under lock for the final one-time transaction.
func (s *AuthStore) CheckChallenge(ctx context.Context, c service.Challenge) error {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback() //nolint:errcheck // Best-effort cleanup after commit or original transaction failure.
	var hash string
	var attempts int
	var valid bool
	e = tx.QueryRowContext(ctx, `SELECT code_hash,attempts,consumed_at IS NULL AND expires_at>now() FROM platform_email_challenges WHERE id::text=$1 AND email=$2 AND purpose=$3 FOR UPDATE`, c.ID, c.Email, c.Purpose).Scan(&hash, &attempts, &valid)
	if errors.Is(e, sql.ErrNoRows) {
		return invalidCode()
	}
	if e != nil {
		return e
	}
	if !valid || attempts >= 5 {
		return invalidCode()
	}
	if hash != c.Hash {
		if _, e = tx.ExecContext(ctx, `UPDATE platform_email_challenges SET attempts=attempts+1 WHERE id::text=$1`, c.ID); e != nil {
			return e
		}
		if e = tx.Commit(); e != nil {
			return e
		}
		return invalidCode()
	}
	return tx.Commit()
}

func (s *AuthStore) RegisterPassword(ctx context.Context, a service.Account, workspace service.Workspace, hash string, expiry time.Time) error {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback() //nolint:errcheck // Cleanup after commit or original transaction error.
	if e = lockAdminInitialization(ctx, tx); e != nil {
		return e
	}
	if s.MailFallback != nil {
		policy, err := readMailPolicy(ctx, tx, *s.MailFallback)
		if err != nil {
			return err
		}
		if policy.VerificationRequired {
			return service.Fail("email_verification_required", 403, "当前部署要求邮箱验证码注册")
		}
	}
	auth := &authTx{tx: tx, initCredits: s.InitCredits}
	if e = auth.CreateAccount(ctx, a); e != nil {
		return e
	}
	if e = auth.CreatePersonalWorkspace(ctx, a.ID, workspace); e != nil {
		return e
	}
	if e = auth.CreateSession(ctx, hash, a.ID, expiry); e != nil {
		return e
	}
	return tx.Commit()
}
func (t *authTx) VerifyEmail(ctx context.Context, id string) error {
	_, e := t.tx.ExecContext(ctx, `UPDATE platform_accounts SET email_verified=true WHERE id=$1`, id)
	return e
}

func (t *authTx) CreatePersonalWorkspace(ctx context.Context, accountID string, w service.Workspace) error {
	if _, err := t.tx.ExecContext(ctx, `INSERT INTO platform_workspaces(id,name,personal_owner_id) VALUES($1,$2,$3)`, w.ID, w.Name, accountID); err != nil {
		return err
	}
	_, err := t.tx.ExecContext(ctx, `INSERT INTO platform_members(workspace_id,user_id,role,status) VALUES($1,$2,'owner','active')`, w.ID, accountID)
	return err
}
