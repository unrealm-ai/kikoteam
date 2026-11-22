package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

// The caller has authenticated the callback and holds the trigger lock.
// Reserve before the savepoint so an authenticated, rejected action still
// consumes credit, without leaving an event, task, or deduplication record.
func finishHookIngress(ctx context.Context, tx *sql.Tx, b service.Trigger, action func() error) error {
	var allowed bool
	if err := tx.QueryRowContext(ctx, `SELECT manager_consume_hook_ingress($1,$2)`, b.WorkspaceID, b.ID).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return service.Fail("trigger_rate_limited", 429, "触发请求过于频繁，请稍后重试")
	}
	if _, err := tx.ExecContext(ctx, `SAVEPOINT hook_business_action`); err != nil {
		return err
	}
	if err := action(); err != nil {
		if _, rollbackErr := tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT hook_business_action`); rollbackErr != nil {
			return rollbackErr
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return commitErr
		}
		return err
	}
	return tx.Commit()
}

func hookLookupError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return service.Fail("trigger_authentication_failed", 401, "触发器认证失败")
	}
	var problem *service.Error
	if errors.As(err, &problem) && problem.Status == 404 {
		return service.Fail("trigger_authentication_failed", 401, "触发器认证失败")
	}
	return err
}
