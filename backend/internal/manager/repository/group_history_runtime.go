package repository

import (
	"context"
	"database/sql"
	"errors"
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func (s *Store) SharedHistoryInput(ctx context.Context, id service.DelegationIdentity) (string, error) {
	cfg, _, input, err := s.CapabilityContext(ctx, id)
	if err != nil {
		return "", err
	}
	if cfg.Mode != "task" || cfg.ConversationID == "" || cfg.TaskSpaceID != "" || cfg.TriggerID != "" {
		return "", service.Fail("history_not_granted", 403, "当前任务没有群共享受众")
	}
	var allowed bool
	err = s.DB.QueryRowContext(ctx, `SELECT source_kind='user' FROM manager_inputs WHERE id=$1`, input).Scan(&allowed)
	if err != nil {
		return "", err
	}
	if !allowed {
		return "", service.Fail("history_not_granted", 403, "系统结果不是群共享授权")
	}
	return input, nil
}
func (s *Store) ListSharedHistoryContexts(ctx context.Context, id service.DelegationIdentity) ([]service.SharedHistoryContext, error) {
	out := []service.SharedHistoryContext{}
	input, err := s.SharedHistoryInput(ctx, id)
	if err != nil {
		return out, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	var inserted string
	err = tx.QueryRowContext(ctx, `INSERT INTO manager_group_history_grant_sets(input_id,session_id,engine_id,assignment_id,generation)
 SELECT i.id,s.id,s.engine_id,s.assignment_id,s.generation FROM manager_sessions s JOIN manager_inputs i ON i.id=$5 AND i.session_id=s.id
 WHERE s.id=$1 AND s.engine_id=$2 AND s.assignment_id=$3 AND s.generation=$4 AND i.status='started' AND i.turn_id=s.active_turn_id
 AND s.stop_revision=s.applied_stop_revision AND manager_input_actor_allowed(i.id)
 ON CONFLICT(input_id) DO NOTHING RETURNING input_id`, id.SessionID, id.EngineID, id.AssignmentID, id.Generation, input).Scan(&inserted)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	// The same frozen grant set survives reassignment; old Engine credentials cannot use it.
	var found string
	err = tx.QueryRowContext(ctx, `UPDATE manager_group_history_grant_sets g SET engine_id=s.engine_id,assignment_id=s.assignment_id,generation=s.generation FROM manager_sessions s,manager_inputs i
 WHERE g.input_id=$5 AND g.session_id=s.id AND i.id=g.input_id AND i.session_id=s.id AND s.id=$1 AND s.engine_id=$2 AND s.assignment_id=$3 AND s.generation=$4
 AND i.status='started' AND i.turn_id=s.active_turn_id AND s.stop_revision=s.applied_stop_revision AND manager_input_actor_allowed(i.id) RETURNING g.input_id`, id.SessionID, id.EngineID, id.AssignmentID, id.Generation, input).Scan(&found)
	if err != nil {
		return out, service.Fail("history_run_inactive", 403, "执行已失效")
	}
	if inserted != "" {
		_, err = tx.ExecContext(ctx, `INSERT INTO manager_group_history_grants(id,input_id,workspace_id,agent_id,session_id,grant_kind,policy_id,policy_version,edge_ordinal,source_ref,target_ref)
 SELECT gen_random_uuid(),$1,s.workspace_id,s.agent_id,s.id,'self',NULL,0,-1,r.ref,r.ref
 FROM manager_sessions s JOIN manager_conversations c ON c.id=s.conversation_id
 LEFT JOIN platform_channel_bindings b ON b.id=c.channel_binding_id
 CROSS JOIN LATERAL (SELECT CASE WHEN c.web_group_id IS NOT NULL THEN jsonb_build_object('kind','group','provider','web','workspace_id',s.workspace_id,'agent_id',s.agent_id,'identity_namespace','web_group_id','external_id',c.web_group_id,'group_epoch',c.web_group_epoch,'agent_epoch',c.web_group_agent_epoch)
 ELSE jsonb_build_object('kind','group','provider',b.provider,'workspace_id',s.workspace_id,'agent_id',s.agent_id,'binding_id',b.id,'tenant_id',b.external_tenant_id,'identity_namespace','chat_id','external_id',c.external_chat_id,'identity_epoch',b.identity_epoch,'connection_generation',b.connection_generation,'config_revision',b.config_revision) END ref) r
 WHERE s.id=$2 AND (c.web_group_id IS NOT NULL OR (c.channel_chat_type='group' AND c.channel_thread_id='' AND b.provider IN ('feishu','wecom')))
 AND manager_sharing_ref_current(r.ref,s.workspace_id,s.agent_id) AND manager_sharing_target_matches($1,r.ref)`, input, id.SessionID)
		if err != nil {
			return out, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO manager_group_history_grants(id,input_id,workspace_id,agent_id,session_id,policy_id,policy_version,edge_ordinal,source_ref,target_ref)
 SELECT gen_random_uuid(),i.id,s.workspace_id,s.agent_id,s.id,p.id,p.version,e.ordinal,e.source_ref,e.target_ref
 FROM manager_inputs i JOIN manager_sessions s ON s.id=i.session_id JOIN platform_group_sharing_policies p ON p.workspace_id=s.workspace_id AND p.agent_id=s.agent_id
 JOIN platform_group_sharing_edges e ON e.policy_id=p.id WHERE i.id=$1 AND p.enabled
 AND e.source_ref->>'provider' IN ('web','feishu','wecom') AND manager_sharing_ref_current(e.source_ref,s.workspace_id,s.agent_id) AND manager_sharing_target_matches(i.id,e.target_ref) ORDER BY p.id,e.ordinal LIMIT 100`, input)
		if err != nil {
			return out, err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT g.id,COALESCE(p.name,'Current group') || ': ' || COALESCE((SELECT title FROM platform_web_groups WHERE id::text=g.source_ref->>'external_id' AND workspace_id=g.workspace_id),(SELECT display_name FROM platform_channel_group_candidates WHERE id::text=g.source_ref->>'id'),g.source_ref->>'external_id'),g.source_ref->>'provider' FROM manager_group_history_grants g LEFT JOIN platform_group_sharing_policies p ON p.id=g.policy_id WHERE g.input_id=$1 AND manager_shared_history_grant_allowed(g.id) ORDER BY p.name,g.id`, input)
	if err != nil {
		return out, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Explicit close below.
	for rows.Next() {
		var v service.SharedHistoryContext
		if err = rows.Scan(&v.Ref, &v.Name, &v.Provider); err != nil {
			return out, err
		}
		v.Coverage = "bounded_text_history"
		out = append(out, v)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return out, err
	}
	return out, tx.Commit()
}
func (s *Store) SharedHistoryGrant(ctx context.Context, id service.DelegationIdentity, grant string) (service.SharedHistoryScope, error) {
	var out service.SharedHistoryScope
	input, err := s.SharedHistoryInput(ctx, id)
	if err != nil {
		return out, err
	}
	err = s.DB.QueryRowContext(ctx, `SELECT workspace_id,agent_id,session_id,input_id,COALESCE(policy_id::text,''),policy_version,edge_ordinal,source_ref,target_ref FROM manager_group_history_grants WHERE id=$1 AND input_id=$2 AND session_id=$3 AND manager_shared_history_grant_allowed(id)`, grant, input, id.SessionID).Scan(&out.WorkspaceID, &out.AgentID, &out.SessionID, &out.InputID, &out.PolicyID, &out.PolicyVersion, &out.EdgeOrdinal, &out.SourceRef, &out.TargetRef)
	if errors.Is(err, sql.ErrNoRows) {
		return out, service.Fail("history_source_forbidden", 403, "共享来源未获授权或已变化")
	}
	return out, err
}
