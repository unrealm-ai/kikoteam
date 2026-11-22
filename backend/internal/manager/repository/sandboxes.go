package repository

import (
	"context"
	"encoding/json"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

// The membership predicate is shared by enumeration, reclaim and restart. A
// standalone task must never acquire its sibling task's runtime or sessions.
const sandboxMembers = `(s.conversation_id=b.conversation_id OR s.task_id=b.task_id)`
const sandboxUnsettled = `(s.engine_id IS NOT NULL OR s.active_turn_id IS NOT NULL OR COALESCE(s.background_process_count,1)<>0 OR s.block_reason IS NOT NULL OR s.stop_revision<>s.applied_stop_revision)`
const sandboxLifecycle = `CASE WHEN b.task_id IS NULL THEN c.lifecycle WHEN p.lifecycle='active' THEN owner.lifecycle ELSE 'archived' END`

func (s *Store) Sandboxes(ctx context.Context, engine string) ([]service.Sandbox, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT b.id,b.workspace_id,COALESCE(c.agent_id,owner.agent_id),COALESCE(b.conversation_id::text,''),COALESCE(owner.task_space_id::text,''),COALESCE(b.task_id::text,''),b.manager_engine_id,`+sandboxLifecycle+`,b.state,
 (`+sandboxLifecycle+`)='archived' AND NOT EXISTS(SELECT 1 FROM manager_sessions s WHERE `+sandboxMembers+` AND `+sandboxUnsettled+`),
 COALESCE((SELECT jsonb_agg(jsonb_build_object('session_id',s.id,'assignment_id',s.assignment_id,'generation',s.generation::text,'engine_id',s.engine_id,'lifecycle',s.lifecycle,'conversation_id',COALESCE(s.conversation_id::text,''),'task_space_id',COALESCE(s.task_space_id::text,''),'task_id',COALESCE(s.task_id::text,'')) ORDER BY s.created_at) FROM manager_sessions s WHERE `+sandboxMembers+` AND s.mode='task'),'[]'::jsonb)
 FROM manager_conversation_sandboxes b LEFT JOIN manager_conversations c ON c.id=b.conversation_id LEFT JOIN manager_sessions owner ON owner.task_id=b.task_id LEFT JOIN manager_task_spaces p ON p.id=owner.task_space_id WHERE b.manager_engine_id=$1 ORDER BY b.created_at,b.id`, engine)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Iteration failures are returned below.
	out := []service.Sandbox{}
	for rows.Next() {
		var v service.Sandbox
		var data []byte
		if err = rows.Scan(&v.ID, &v.WorkspaceID, &v.AgentID, &v.ConversationID, &v.TaskSpaceID, &v.TaskID, &v.EngineID, &v.Lifecycle, &v.State, &v.ReclaimAllowed, &data); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(data, &v.Sessions); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) SandboxState(ctx context.Context, engine, id, state string) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE manager_conversation_sandboxes b SET state=$3 WHERE b.id=$1 AND b.manager_engine_id=$2 AND ($3<>'stopped' OR NOT EXISTS(SELECT 1 FROM manager_sessions s WHERE `+sandboxMembers+` AND `+sandboxUnsettled+`))`, id, engine, state)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return service.Fail("sandbox_unresolved", 409, "环境尚未收尾或管理身份已失效")
	}
	return err
}
