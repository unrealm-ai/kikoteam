package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func canManageEmployee(ctx context.Context, tx *sql.Tx, workspace, employee, actor string) (bool, error) {
	if !service.ValidWorkspaceSubject(workspace) || !service.ValidWorkspaceSubject(employee) || !service.ValidWorkspaceSubject(actor) {
		return false, nil
	}
	var allowed bool
	err := tx.QueryRowContext(ctx, "SELECT platform_can_manage_agent($1,$2,$3)", workspace, employee, actor).Scan(&allowed)
	return allowed, err
}

// AuthorizeEmployeeManagement must be used with a workspace-scoped target.
func AuthorizeEmployeeManagement(ctx context.Context, tx *sql.Tx, workspace, employee, actor string) error {
	allowed, err := canManageEmployee(ctx, tx, workspace, employee, actor)
	if err == nil && !allowed {
		return service.Fail("workspace_forbidden", 403, "没有此员工的管理权限")
	}
	return err
}

func (t *agentTx) CanManageEmployee(ctx context.Context, employee, actor string) (bool, error) {
	return canManageEmployee(ctx, t.model.tx, t.model.workspace, employee, actor)
}

func (t *channelTx) AuthorizeEmployee(ctx context.Context, employee, actor string) error {
	return AuthorizeEmployeeManagement(ctx, t.tx, t.workspace, employee, actor)
}

func (t *packageTx) AuthorizeEmployee(ctx context.Context, employee, actor string) error {
	return AuthorizeEmployeeManagement(ctx, t.model.tx, t.model.workspace, employee, actor)
}

func (t *agentTx) AuthorizeEmployee(ctx context.Context, employee, actor string) error {
	return AuthorizeEmployeeManagement(ctx, t.model.tx, t.model.workspace, employee, actor)
}

// LockEmployeeManagement serializes usage writes with membership and whitelist revocation.
// The caller must first acquire any channel configuration lock it needs.
func LockEmployeeManagement(ctx context.Context, tx *sql.Tx, w, employee, actor string) error {
	var member string
	err := tx.QueryRowContext(ctx, `SELECT user_id FROM platform_members WHERE workspace_id=$1 AND user_id=$2 AND status='active' FOR SHARE`, w, actor).Scan(&member)
	if errors.Is(err, sql.ErrNoRows) {
		return service.Fail("workspace_forbidden", 403, "没有此员工的管理权限")
	}
	if err != nil {
		return err
	}
	var id string
	err = tx.QueryRowContext(ctx, `SELECT id FROM platform_agents WHERE workspace_id=$1 AND id=$2 FOR UPDATE`, w, employee).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return service.Fail("agent_not_found", 404, "员工不存在")
	}
	if err != nil {
		return err
	}
	return AuthorizeEmployeeManagement(ctx, tx, w, employee, actor)
}

func (t *pluginTx) AuthorizeEmployee(ctx context.Context, employee, actor string) error {
	return AuthorizeEmployeeManagement(ctx, t.model.tx, t.model.workspace, employee, actor)
}
