package repository

import (
	"context"
	"database/sql"
	"errors"
	"github.com/jackc/pgx/v5/pgtype"
	"net/http"

	platform "github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type Store struct{ DB *sql.DB }

func UUID(s string) bool {
	var id pgtype.UUID
	return len(s) == 36 && id.Scan(s) == nil && id.Valid
}

func (s Store) Transaction(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback() //nolint:errcheck // Best effort after commit or the original error.
	if e = fn(tx); e != nil {
		return e
	}
	return tx.Commit()
}

type RowReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func Admin(ctx context.Context, db RowReader, actor string, write bool) error {
	query := `SELECT is_platform_admin FROM platform_accounts WHERE id=$1`
	if write {
		query += ` FOR SHARE`
	}
	var allowed bool
	e := db.QueryRowContext(ctx, query, actor).Scan(&allowed)
	if errors.Is(e, sql.ErrNoRows) || e == nil && !allowed {
		return platform.Fail("platform_admin_required", http.StatusForbidden, "需要平台管理员权限")
	}
	return e
}

func Missing(e error) error {
	if errors.Is(e, sql.ErrNoRows) {
		return platform.Fail("not_found", 404, "记录不存在")
	}
	return e
}
