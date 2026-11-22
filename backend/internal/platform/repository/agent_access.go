package repository

import (
	"context"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func (t *modelTx) AuthorizeWebEmployee(ctx context.Context, actor, employee string) error {
	var allowed bool
	if e := t.tx.QueryRowContext(ctx, `SELECT platform_agent_web_allowed($1,$2,$3)`, t.workspace, employee, actor).Scan(&allowed); e != nil {
		return e
	}
	if !allowed {
		return service.Fail("employee_access_denied", 403, "没有员工使用权限")
	}
	return nil
}
func (t *skillTx) AuthorizeWebEmployee(ctx context.Context, a, id string) error {
	return t.model.AuthorizeWebEmployee(ctx, a, id)
}
func (t *pluginTx) AuthorizeWebEmployee(ctx context.Context, a, id string) error {
	return t.model.AuthorizeWebEmployee(ctx, a, id)
}
func (t *packageTx) AuthorizeWebEmployee(ctx context.Context, a, id string) error {
	return t.model.AuthorizeWebEmployee(ctx, a, id)
}
