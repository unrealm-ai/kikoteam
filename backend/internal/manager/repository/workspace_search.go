package repository

import (
	"context"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

// Search only the reader's own Web private chats with an existing chat session.
// Channel observations and group metadata are not a source of authorization.
func (s *Store) SearchWebConversations(ctx context.Context, workspace, actor, query string, limit int) ([]service.ConversationSearchHit, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT c.id,c.agent_id,COALESCE(NULLIF(c.title,''),a.name),a.name
 FROM manager_conversations c JOIN platform_agents a ON (a.workspace_id,a.id)=(c.workspace_id,c.agent_id)
 JOIN platform_members m ON m.workspace_id=c.workspace_id AND m.user_id=$2 AND m.status='active'
 WHERE c.workspace_id=$1 AND c.web_user_id=$2 AND c.web_group_id IS NULL AND c.channel_binding_id IS NULL
 AND EXISTS(SELECT 1 FROM manager_sessions s WHERE s.conversation_id=c.id AND s.mode='chat')
 AND (position(lower($3) in lower(COALESCE(c.title,'')))>0 OR position(lower($3) in lower(a.name))>0 OR c.id::text=lower($3))
 ORDER BY COALESCE(c.last_message_at,c.created_at) DESC,c.id DESC LIMIT $4`, workspace, actor, query, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Read cursor; iteration checked below.
	items := []service.ConversationSearchHit{}
	for rows.Next() {
		var item service.ConversationSearchHit
		if err = rows.Scan(&item.ID, &item.AgentID, &item.Title, &item.AgentName); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
