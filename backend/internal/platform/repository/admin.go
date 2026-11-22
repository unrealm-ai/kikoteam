package repository

import (
	"context"
	"database/sql"
	proxy "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func lockAdminInitialization(ctx context.Context, tx *sql.Tx) error {
	var done bool
	if e := tx.QueryRowContext(ctx, `SELECT initialized_at IS NOT NULL FROM platform_admin_state WHERE singleton`).Scan(&done); e != nil {
		return e
	}
	if done {
		return nil
	}
	_, e := tx.ExecContext(ctx, `SELECT singleton FROM platform_admin_state WHERE singleton FOR UPDATE`)
	return e
}

type AdminStore struct{ DB *sql.DB }

func (s *AdminStore) List(ctx context.Context, actor, q, filter string, p, n int) (service.AccountPage, error) {
	out := service.AccountPage{Items: []service.Account{}, Page: p, PageSize: n}
	var allowed bool
	if e := s.DB.QueryRowContext(ctx, `SELECT is_platform_admin FROM platform_accounts WHERE id=$1`, actor).Scan(&allowed); e != nil {
		return out, e
	}
	if !allowed {
		return out, service.Fail("platform_admin_required", 403, "需要平台管理员权限")
	}
	pattern := "%" + escapeLike(q) + "%"
	where := ` WHERE (name ILIKE $1 ESCAPE '\' OR email ILIKE $1 ESCAPE '\') AND ($2='' OR is_platform_admin::text=$2)`
	if e := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM platform_accounts`+where, pattern, filter).Scan(&out.Total); e != nil {
		return out, e
	}
	rows, e := s.DB.QueryContext(ctx, `SELECT id,name,email,email_verified,is_platform_admin,platform_admin_version,created_at,COALESCE((SELECT balance_units FROM platform_credit_wallets WHERE account_id=platform_accounts.id),0) FROM platform_accounts`+where+` ORDER BY created_at DESC,id DESC LIMIT $3 OFFSET $4`, pattern, filter, n, (p-1)*n)
	if e != nil {
		return out, e
	}
	defer rows.Close() //nolint:errcheck // Iteration errors are checked below.
	for rows.Next() {
		var a service.Account
		var balance int64
		if e = rows.Scan(&a.ID, &a.Name, &a.Email, &a.EmailVerified, &a.IsPlatformAdmin, &a.PlatformAdminVersion, &a.CreatedAt, &balance); e != nil {
			return out, e
		}
		a.CreditBalance = proxy.Amount(balance)
		out.Items = append(out.Items, a)
	}
	return out, rows.Err()
}
func (s *AdminStore) Set(ctx context.Context, actor, id string, r service.AdminPatch) (service.AdminResult, error) {
	out := service.AdminResult{AccountID: id}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return out, e
	}
	defer tx.Rollback() //nolint:errcheck // Best-effort rollback after commit or the original error.
	if _, e = tx.ExecContext(ctx, `SELECT singleton FROM platform_admin_state WHERE singleton FOR UPDATE`); e != nil {
		return out, e
	}
	rows, e := tx.QueryContext(ctx, `SELECT id,is_platform_admin,platform_admin_version FROM platform_accounts WHERE id IN ($1,$2) ORDER BY id FOR UPDATE`, actor, id)
	if e != nil {
		return out, e
	}
	admin := false
	defer rows.Close() //nolint:errcheck // Rows are explicitly closed before subsequent queries.
	found := false
	for rows.Next() {
		var key string
		var flag bool
		var v int64
		if e = rows.Scan(&key, &flag, &v); e != nil {
			return out, e
		}
		if key == actor {
			admin = flag
		}
		if key == id {
			found = true
			out.IsPlatformAdmin = flag
			out.Version = v
		}
	}
	e = rows.Err()
	if closeErr := rows.Close(); e == nil {
		e = closeErr
	}
	if e != nil {
		return out, e
	}
	if !admin {
		return out, service.Fail("platform_admin_required", 403, "需要平台管理员权限")
	}
	if !found {
		return out, service.ErrWorkspaceMissing
	}
	if out.IsPlatformAdmin != r.IsPlatformAdmin {
		if r.Version != out.Version {
			return out, service.Fail("platform_admin_version_conflict", 409, "管理员资格已变化，请刷新")
		}
		if !r.IsPlatformAdmin {
			var count int
			if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM platform_accounts WHERE is_platform_admin`).Scan(&count); e != nil {
				return out, e
			}
			if count <= 1 {
				return out, service.Fail("last_platform_admin", 409, "至少保留一位平台管理员")
			}
			if e = preservePasswordAdminWhenMailDisabled(ctx, tx, id); e != nil {
				return out, e
			}
		}
		out.IsPlatformAdmin = r.IsPlatformAdmin
		out.Version++
		if _, e = tx.ExecContext(ctx, `UPDATE platform_accounts SET is_platform_admin=$2,platform_admin_version=$3 WHERE id=$1`, id, out.IsPlatformAdmin, out.Version); e != nil {
			return out, e
		}
	}
	return out, tx.Commit()
}
