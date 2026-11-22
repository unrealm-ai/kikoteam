package repository

import (
	"context"
	"errors"
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

// ResolveWebConversation serializes by the business key, independent of HTTP
// request identity. Generic/channel conversations are never adopted implicitly.
func (s *Store) ResolveWebConversation(ctx context.Context, v service.Conversation) (service.Conversation, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return v, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Preserve operation error.
	var id string
	if err = tx.QueryRowContext(ctx, `SELECT w.id FROM platform_workspaces w JOIN platform_members m ON m.workspace_id=w.id AND m.user_id=$2 AND m.status='active' WHERE w.id=$1 FOR SHARE OF w`, v.WorkspaceID, v.CreatedBy).Scan(&id); err != nil {
		return v, visibleError(err)
	}
	var enabled bool
	if err = tx.QueryRowContext(ctx, `SELECT platform_agent_web_allowed(workspace_id,id,$3) FROM platform_agents WHERE workspace_id=$1 AND id=$2 FOR SHARE`, v.WorkspaceID, v.AgentID, v.CreatedBy).Scan(&enabled); err != nil {
		return v, visibleError(err)
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "web-direct:"+v.WorkspaceID+":"+v.AgentID+":"+v.CreatedBy); err != nil {
		return v, err
	}
	// Existing history remains readable when an employee is disabled.
	existing, e := scanConversation(tx.QueryRowContext(ctx, `SELECT `+conversationColumns+` FROM manager_conversations c WHERE c.workspace_id=$1 AND c.agent_id=$2 AND c.web_user_id=$3`, v.WorkspaceID, v.AgentID, v.CreatedBy))
	if e == nil {
		return existing, tx.Commit()
	}
	if !isMissing(e) {
		return v, e
	}
	if !enabled {
		return v, service.Fail("agent_disabled", 409, "员工已停用")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO manager_conversations(id,workspace_id,agent_id,created_by,web_user_id,title,created_at) VALUES($1,$2,$3,$4,$4,$5,$6)`, v.ID, v.WorkspaceID, v.AgentID, v.CreatedBy, v.Title, v.CreatedAt)
	if err != nil {
		return v, err
	}
	return v, tx.Commit()
}
func isMissing(err error) bool {
	var e *service.Error
	return errors.As(err, &e) && e.Code == "resource_not_found"
}
