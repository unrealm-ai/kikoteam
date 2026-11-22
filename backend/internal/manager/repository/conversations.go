// Package repository persists Manager facts in the shared PostgreSQL database.
package repository

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sync/atomic"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	platform "github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type Store struct {
	WorkAssets           service.WorkAssets
	ChannelLog           *slog.Logger
	RuntimeRoot          string
	dispatchCursor       atomic.Pointer[dispatchPosition]
	TriggerSecret        []byte
	ConsumeWecomControl  func(context.Context, *sql.Tx, service.ChannelInbound, int64) (service.WecomControlResult, error)
	DB                   *sql.DB
	SnapshotCapabilities func(context.Context, *sql.Tx, string, string, string) (platform.ExecutionCapabilities, error)
}
type scanner interface{ Scan(...any) error }

const conversationColumns = `c.id,c.workspace_id,c.agent_id,COALESCE(c.created_by::text,''),c.title,c.lifecycle,
 (SELECT s.id FROM manager_sessions s WHERE s.conversation_id=c.id AND s.mode='chat'),
 (c.lifecycle='archived' AND (EXISTS(SELECT 1 FROM manager_sessions s WHERE s.conversation_id=c.id AND (s.active_turn_id IS NOT NULL OR s.applied_stop_revision<s.stop_revision OR s.background_process_count IS NULL OR s.background_process_count>0 OR s.block_reason IS NOT NULL)) OR EXISTS(SELECT 1 FROM manager_conversation_sandboxes e WHERE e.conversation_id=c.id AND e.state<>'stopped'))),c.created_at,c.archived_at,c.web_user_id,c.channel_created_by,c.channel_binding_id`

func scanConversation(row scanner) (service.Conversation, error) {
	var v service.Conversation
	err := row.Scan(&v.ID, &v.WorkspaceID, &v.AgentID, &v.CreatedBy, &v.Title, &v.Lifecycle, &v.ChatSessionID, &v.CleanupPending, &v.CreatedAt, &v.ArchivedAt, &v.WebUserID, &v.ChannelCreatedBy, &v.ChannelBindingID)
	return v, visibleError(err)
}
func visibleError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return service.Fail("resource_not_found", 404, "资源不存在")
	}
	return err
}

func (s *Store) InsertConversation(ctx context.Context, v service.Conversation) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Rollback after commit or failure is best effort.
	var workspace string
	// Membership changes serialize through the workspace row in Platform.
	if err = tx.QueryRowContext(ctx, `SELECT w.id FROM platform_workspaces w JOIN platform_members m ON m.workspace_id=w.id AND m.user_id=$2 AND m.status='active' WHERE w.id=$1 FOR SHARE OF w`, v.WorkspaceID, v.CreatedBy).Scan(&workspace); err != nil {
		return visibleError(err)
	}
	var enabled bool
	if err = tx.QueryRowContext(ctx, `SELECT platform_agent_web_allowed(workspace_id,id,$3) FROM platform_agents WHERE workspace_id=$1 AND id=$2 FOR SHARE`, v.WorkspaceID, v.AgentID, v.CreatedBy).Scan(&enabled); err != nil {
		return visibleError(err)
	}
	if !enabled {
		return service.Fail("agent_disabled", 409, "员工已停用")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO manager_conversations(id,workspace_id,agent_id,created_by,title,created_at) VALUES($1,$2,$3,$4,$5,$6)`, v.ID, v.WorkspaceID, v.AgentID, v.CreatedBy, v.Title, v.CreatedAt)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Conversation(ctx context.Context, workspace, actor, id string) (service.Conversation, error) {
	return scanConversation(s.DB.QueryRowContext(ctx, `SELECT `+conversationColumns+` FROM manager_conversations c WHERE c.workspace_id=$1 AND c.web_group_id IS NULL AND c.channel_binding_id IS NULL AND (c.web_user_id IS NULL OR c.web_user_id=$2) AND c.id=$3 AND EXISTS(SELECT 1 FROM platform_members m WHERE m.workspace_id=c.workspace_id AND m.user_id=$2 AND m.status='active')`, workspace, actor, id))
}

func (s *Store) Conversations(ctx context.Context, workspace, actor string, f service.ConversationFilter) ([]service.Conversation, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+conversationColumns+` FROM manager_conversations c WHERE c.workspace_id=$1 AND c.web_group_id IS NULL AND c.channel_binding_id IS NULL AND (c.web_user_id IS NULL OR c.web_user_id=$2) AND EXISTS(SELECT 1 FROM platform_members m WHERE m.workspace_id=c.workspace_id AND m.user_id=$2 AND m.status='active') AND ($3='' OR c.agent_id::text=$3) AND ($4='' OR c.lifecycle=$4) AND ($5='' OR (c.created_at,c.id)<($6::timestamptz,NULLIF($5,'')::uuid)) ORDER BY c.created_at DESC,c.id DESC LIMIT $7`, workspace, actor, f.AgentID, f.Lifecycle, f.BeforeID, f.BeforeTime, f.Limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Read-only rows; iteration failures are checked below.
	items := []service.Conversation{}
	for rows.Next() {
		v, err := scanConversation(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, v)
	}
	return items, rows.Err()
}
