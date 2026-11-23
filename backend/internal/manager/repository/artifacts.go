package repository

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"path"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func (s *Store) publishArtifact(ctx context.Context, tx *sql.Tx, src workSource, p string) (service.Artifact, error) {
	var a service.Artifact
	var conv, space, task string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(conversation_id::text,''),COALESCE(task_space_id::text,''),task_id FROM manager_sessions WHERE id=$1`, src.session).Scan(&conv, &space, &task); err != nil {
		return a, err
	}
	data, err := service.ReadWorkFile(s.RuntimeRoot, src.workspace, src.agent, conv, space, task, p)
	if err != nil {
		return a, err
	}
	if s.WorkAssets == nil {
		return a, service.Fail("artifacts_unavailable", 503, "产物存储不可用")
	}
	a.Version, err = s.WorkAssets.PutWorkFile(ctx, src.workspace, data)
	if err != nil {
		return a, err
	}
	a.Name = path.Base(p)
	a.Size = int64(len(data))
	a.MediaType = http.DetectContentType(data)
	err = tx.QueryRowContext(ctx, `INSERT INTO manager_task_artifacts(id,turn_id,path,name,media_type,version,size_bytes) VALUES(gen_random_uuid(),$1,$2,$3,$4,$5,$6)
 ON CONFLICT(turn_id,path,version) DO UPDATE SET path=EXCLUDED.path RETURNING id`, src.turn, p, a.Name, a.MediaType, a.Version, a.Size).Scan(&a.ID)
	return a, err
}

func turnArtifacts(ctx context.Context, tx *sql.Tx, turn string) ([]service.Artifact, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,version,name,media_type,size_bytes FROM manager_task_artifacts WHERE turn_id=$1 ORDER BY created_at,id`, turn)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Iteration errors are returned.
	out := []service.Artifact{}
	for rows.Next() {
		var a service.Artifact
		if err := rows.Scan(&a.ID, &a.Version, &a.Name, &a.MediaType, &a.Size); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Only a query from a live channel user can offer a prior result for resending.
func (s *Store) OfferArtifacts(ctx context.Context, scope service.DelegationScope, turn string) ([]service.Artifact, error) {
	if scope.ReportOnly || scope.Channel == nil {
		return nil, nil
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit or the operational error owns the result.
	var chatTurn string
	err = tx.QueryRowContext(ctx, `SELECT t.id`+delegationFrom+` AND EXISTS (SELECT FROM manager_turns src JOIN manager_sessions ss ON ss.id=src.session_id
 JOIN manager_tasks k ON k.id=ss.task_id JOIN manager_inputs auth ON auth.id=src.input_id
 WHERE src.id=$5 AND src.status IN ('completed','failed','interrupted','canceled') AND k.source_chat_session_id=s.id AND ss.conversation_id=s.conversation_id
 AND manager_input_actor_allowed(auth.id) AND auth.channel_principal_id=$6 AND auth.channel_generation=$7)
 AND EXISTS(SELECT FROM manager_channel_inbox n WHERE n.input_id=$8 AND n.conversation_id=s.conversation_id AND n.principal_id=$6)`,
		scope.Identity.SessionID, scope.Identity.EngineID, scope.Identity.AssignmentID, scope.Identity.Generation, turn, scope.ActorID, scope.Channel.Generation, scope.InputID).Scan(&chatTurn)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	a, err := turnArtifacts(ctx, tx, turn)
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO manager_artifact_offers SELECT $1,$2,id FROM manager_task_artifacts WHERE turn_id=$3 ON CONFLICT DO NOTHING`, chatTurn, scope.InputID, turn)
	if err != nil {
		return nil, err
	}
	return a, tx.Commit()
}

const artifactSelectionTurn = `SELECT t.id` + delegationFrom + ` AND i.channel_principal_id=$5 AND i.channel_generation=$6 AND ` + effectiveDelegatingInput + `=$7`

// Use the same eligible set for selection and its recovery list. The active
// source is resolved again in the same query snapshot as the candidate rows.
const artifactSelectionScope = `WITH active AS (` + artifactSelectionTurn + ` AND manager_input_actor_allowed($7)),
 eligible AS (
 SELECT ra.artifact_id,n.id AS inbox_id,r.id AS report_id
 FROM active JOIN manager_inputs received ON received.turn_id=active.id
 JOIN manager_task_reports r ON r.target_input_id=received.id JOIN manager_report_artifacts ra ON ra.report_id=r.id
 JOIN manager_task_artifacts f ON f.id=ra.artifact_id AND f.turn_id=r.source_turn_id
 JOIN manager_sessions chat ON chat.id=received.session_id AND r.target_session_id=chat.id
 JOIN manager_inputs origin ON origin.id=r.source_input_id JOIN manager_sessions task ON task.id=origin.session_id
 JOIN manager_tasks k ON k.id=task.task_id JOIN manager_channel_inbox n ON n.input_id=k.source_chat_input_id
 WHERE (received.id=(SELECT input_id FROM manager_turns WHERE id=active.id) OR received.context_state='committed')
 AND chat.id=$1 AND task.conversation_id=chat.conversation_id AND n.conversation_id=chat.conversation_id AND n.principal_id=$5 AND n.connection_generation=$6
 AND manager_input_actor_allowed(origin.id) AND manager_channel_actor_allowed(n.principal_id,n.conversation_id,n.connection_generation)
 UNION ALL SELECT o.artifact_id,n.id,NULL::uuid
 FROM active JOIN manager_artifact_offers o ON o.chat_turn_id=active.id
 JOIN manager_channel_inbox n ON n.input_id=o.input_id JOIN manager_inputs u ON u.id=o.input_id
 JOIN manager_task_artifacts f ON f.id=o.artifact_id JOIN manager_turns src ON src.id=f.turn_id
 JOIN manager_inputs auth ON auth.id=src.input_id JOIN manager_sessions task ON task.id=src.session_id
 JOIN manager_tasks k ON k.id=task.task_id
 WHERE o.input_id=$7 AND n.principal_id=$5 AND n.connection_generation=$6 AND u.source_kind='user' AND manager_input_actor_allowed(u.id)
 AND src.status IN ('completed','failed','interrupted','canceled') AND k.source_chat_session_id=$1
 AND task.conversation_id=n.conversation_id AND auth.channel_principal_id=$5 AND auth.channel_generation=$6
 AND manager_input_actor_allowed(auth.id))
 `

func artifactSelectionScopeArgs(scope service.DelegationScope) []any {
	id := scope.Identity
	return []any{id.SessionID, id.EngineID, id.AssignmentID, id.Generation, scope.ActorID, scope.Channel.Generation, scope.InputID}
}

func artifactSelectionRecovery(ctx context.Context, tx *sql.Tx, args []any, original error) error {
	rows, err := tx.QueryContext(ctx, artifactSelectionScope+`
 SELECT a.id,a.name FROM manager_task_artifacts a JOIN (SELECT DISTINCT artifact_id FROM eligible) x ON x.artifact_id=a.id
 ORDER BY a.created_at,a.id LIMIT 32`, args...)
	if err != nil {
		return original
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Recovery is optional; the original failure is authoritative.
	var candidates []service.ArtifactSelectionCandidate
	for rows.Next() {
		var candidate service.ArtifactSelectionCandidate
		if err := rows.Scan(&candidate.ID, &candidate.Name); err != nil {
			return original
		}
		candidates = append(candidates, candidate)
	}
	if rows.Err() != nil || len(candidates) == 0 {
		return original
	}
	return &service.ArtifactSelectionError{Err: original, Candidates: candidates}
}

func (s *Store) SelectArtifacts(ctx context.Context, scope service.DelegationScope, ids []string) (any, error) {
	if scope.Channel == nil || len(ids) == 0 || len(ids) > 32 {
		return nil, service.Fail("invalid_artifact_selection", 400, "需要渠道回复和候选产物")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit or the operational error owns the result.
	args := artifactSelectionScopeArgs(scope)
	var chatTurn string
	err = tx.QueryRowContext(ctx, artifactSelectionTurn+` FOR SHARE OF s,t`, args...).Scan(&chatTurn)
	if err != nil {
		return nil, visibleError(err)
	}
	seen := map[string]bool{}
	for _, a := range ids {
		if seen[a] {
			return nil, service.Fail("invalid_artifact_selection", 400, "候选 ID 无效或重复")
		}
		if !service.ValidID(a) {
			err = service.Fail("invalid_artifact_selection", 400, "候选 ID 无效或重复")
			return nil, artifactSelectionRecovery(ctx, tx, args, err)
		}
		seen[a] = true
		var inbox string
		var report sql.NullString
		// Each report retains its own original inbox even when multiple reports steer the same chat Turn.
		err = tx.QueryRowContext(ctx, artifactSelectionScope+`
 SELECT inbox_id,report_id FROM eligible WHERE artifact_id=$8 ORDER BY report_id NULLS FIRST LIMIT 1`,
			append(args, a)...).Scan(&inbox, &report)
		if errors.Is(err, sql.ErrNoRows) {
			err = service.Fail("artifact_forbidden", 403, "产物不属于这次回复的候选清单")
			return nil, artifactSelectionRecovery(ctx, tx, args, err)
		}
		if err != nil {
			return nil, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO manager_artifact_selections(chat_turn_id,artifact_id,inbox_id,report_id) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, chatTurn, a, inbox, report)
		if err != nil {
			return nil, err
		}
	}
	return map[string]any{"status": "selected", "artifact_ids": ids}, tx.Commit()
}

func (s *Store) materializeArtifactReplies(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO manager_channel_deliveries(id,inbox_id,binding_id,connection_generation,kind,text,content_format,artifact_id)
 SELECT gen_random_uuid(),n.id,n.binding_id,n.connection_generation,'artifact:'||sel.chat_turn_id||':'||a.id,'',
 CASE WHEN a.media_type IN ('image/png','image/jpeg','image/gif','image/webp') THEN 'image' ELSE 'file' END,a.id
 FROM manager_artifact_selections sel JOIN manager_task_artifacts a ON a.id=sel.artifact_id JOIN manager_turns reply ON reply.id=sel.chat_turn_id
 JOIN manager_channel_inbox n ON n.id=sel.inbox_id WHERE reply.status='completed'
 AND NOT EXISTS(SELECT FROM manager_channel_deliveries d WHERE d.inbox_id=n.id AND d.kind='artifact:'||sel.chat_turn_id||':'||a.id)
 ORDER BY sel.position ON CONFLICT(inbox_id,kind) DO NOTHING`)
	return err
}

func (s *Store) ReadDeliveryArtifact(ctx context.Context, id, lease string, content bool) (a service.Artifact, data []byte, err error) {
	if !service.ValidID(id) || !service.ValidID(lease) {
		return a, nil, service.Fail("lease_lost", 409, "投递身份无效")
	}
	var workspace string
	err = s.DB.QueryRowContext(ctx, `SELECT f.id,f.version,f.name,f.media_type,f.size_bytes,b.workspace_id FROM manager_channel_deliveries d JOIN manager_channel_inbox n ON n.id=d.inbox_id
 JOIN platform_channel_bindings b ON b.id=d.binding_id JOIN platform_agents a ON a.id=b.agent_id JOIN manager_task_artifacts f ON f.id=d.artifact_id
 JOIN manager_turns src ON src.id=f.turn_id JOIN manager_artifact_selections sel ON sel.artifact_id=f.id AND sel.inbox_id=n.id AND d.kind='artifact:'||sel.chat_turn_id||':'||f.id
 WHERE d.id=$1 AND d.lease_token=$2 AND d.status='sending' AND d.lease_until>now() AND manager_input_actor_allowed(src.input_id) AND `+deliveryAllowedExpression, id, lease).Scan(&a.ID, &a.Version, &a.Name, &a.MediaType, &a.Size, &workspace)
	if err != nil {
		return a, nil, visibleError(err)
	}
	if content {
		if s.WorkAssets == nil {
			return a, nil, service.Fail("artifacts_unavailable", 503, "产物暂不可读")
		}
		data, err = s.WorkAssets.GetWorkFile(ctx, workspace, a.Version, a.Size)
	}
	return
}
