package repository

import (
	"context"
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func (s *Store) CapabilityContext(ctx context.Context, v service.DelegationIdentity) (service.HarnessConfig, string, string, error) {
	var input string
	err := s.DB.QueryRowContext(ctx, `SELECT t.input_id FROM manager_sessions s JOIN manager_turns t ON t.id=s.active_turn_id WHERE s.id=$1 AND s.engine_id=$2 AND s.assignment_id=$3 AND s.generation=$4 AND s.mode='task' AND t.status='running' AND t.assignment_id=s.assignment_id AND t.generation=s.generation AND s.stop_revision=s.applied_stop_revision`, v.SessionID, v.EngineID, v.AssignmentID, v.Generation).Scan(&input)
	if err != nil {
		return service.HarnessConfig{}, "", "", visibleError(err)
	}
	cfg, actor, err := s.HarnessConfig(ctx, v.EngineID, v.SessionID, v.AssignmentID, v.Generation, input)
	return cfg, actor, input, err
}

func (s *Store) RecordCapabilityCall(ctx context.Context, v service.DelegationIdentity, c service.CapabilityCall) error {
	result, err := s.DB.ExecContext(ctx, `INSERT INTO manager_capability_calls(id,workspace_id,session_id,turn_id,native_tool_call_id,plugin_id,plugin_version_id,account_id,account_version,tool_name,schema_digest,status,error_code,finished_at,connection_source,actor_ref,agent_id)
 SELECT $5,s.workspace_id,s.id,t.id,$6,$7,$8,$9,$10,$11,$12,$13,$14,CASE WHEN $13='authorized' THEN NULL ELSE now() END,NULLIF($16,''),NULLIF($17,''),s.agent_id FROM manager_sessions s JOIN manager_turns t ON t.id=s.active_turn_id WHERE s.id=$1 AND s.engine_id=$2 AND s.assignment_id=$3 AND s.generation=$4 AND ($13<>'authorized' OR EXISTS (
 SELECT FROM platform_plugin_connections pc JOIN platform_plugins p ON p.id=pc.plugin_id
 LEFT JOIN platform_agent_plugin_connection_settings choice ON choice.workspace_id=pc.workspace_id AND choice.agent_id=s.agent_id AND choice.plugin_id=pc.plugin_id
 WHERE pc.workspace_id=s.workspace_id AND pc.id=$9 AND pc.version=$10 AND pc.plugin_id=$7 AND pc.plugin_version_id=$8 AND pc.enabled AND pc.validated AND p.enabled AND p.archived_at IS NULL
 AND pc.agent_id IS NOT DISTINCT FROM platform_plugin_connection_owner(s.workspace_id,s.agent_id,pc.plugin_id) AND COALESCE(choice.revision,0)=$18))
 AND t.status='running' AND t.input_id=$15 AND manager_input_actor_allowed(t.input_id) AND t.assignment_id=$3 AND t.generation=$4 AND s.stop_revision=s.applied_stop_revision AND s.lifecycle='active' AND (EXISTS(SELECT 1 FROM manager_conversations c WHERE c.id=s.conversation_id AND c.lifecycle='active') OR EXISTS(SELECT 1 FROM manager_task_spaces p WHERE p.id=s.task_space_id AND p.lifecycle='active'))`, v.SessionID, v.EngineID, v.AssignmentID, v.Generation, c.ID, c.NativeID, c.PluginID, c.VersionID, c.AccountID, c.AccountVersion, c.Tool, c.SchemaDigest, c.Status, c.ErrorCode, c.InputID, c.Source, c.Actor, c.SourceRevision)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return service.Fail("capability_forbidden", 403, "当前执行窗口已关闭")
	}
	return err
}

func (s *Store) FinishCapabilityCall(ctx context.Context, v service.DelegationIdentity, id, status string) error {
	return s.finishCapabilityCall(ctx, v, id, status, false)
}
func (s *Store) FinishRelayCall(ctx context.Context, v service.DelegationIdentity, id, status string) error {
	return s.finishCapabilityCall(ctx, v, id, status, true)
}
func (s *Store) finishCapabilityCall(ctx context.Context, v service.DelegationIdentity, id, status string, relay bool) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE manager_capability_calls c SET status=$6,finished_at=now() FROM manager_sessions s,manager_turns t WHERE c.id=$5 AND c.session_id=s.id AND c.turn_id=t.id AND s.id=$1 AND s.engine_id=$2 AND s.assignment_id=$3 AND s.generation=$4 AND t.assignment_id=$3 AND t.generation=$4 AND c.status='authorized' AND (c.native_tool_call_id LIKE 'relay:%')=$7`, v.SessionID, v.EngineID, v.AssignmentID, v.Generation, id, status, relay)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return service.Fail("capability_call_conflict", 409, "调用记录不存在或已结束")
	}
	return err
}
