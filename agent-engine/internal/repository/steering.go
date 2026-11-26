package repository

import (
	"context"
	"database/sql"
	"errors"
)

// ClaimSteering joins one input to the active Turn. Task reports always get
// their own Turn, including reports accepted with the legacy steer mode.
func (s *Store) ClaimSteering(ctx context.Context, w Work, t Turn) (*Input, error) {
	var out *Input
	err := s.scopedTransaction(ctx, w, true, false, func(tx *sql.Tx) error {
		// Serialize the batch admission with channel binding reconfiguration,
		// matching the ordinary Claim path's lock order.
		if _, err := tx.ExecContext(ctx, `SELECT b.id FROM platform_channel_bindings b JOIN manager_conversations c ON c.channel_binding_id=b.id WHERE c.id=NULLIF($1,'')::uuid FOR SHARE OF b`, w.ConversationID); err != nil {
			return err
		}
		var valid bool
		err := tx.QueryRowContext(ctx, `SELECT s.required_actions='[]' AND NOT EXISTS(SELECT FROM manager_inputs answer WHERE answer.session_id=s.id AND answer.status='queued' AND (answer.confirmation_request_id IS NOT NULL OR answer.human_request_id IS NOT NULL OR answer.work_submission_id IS NOT NULL)) AND s.active_turn_id=$2 AND s.stop_revision=s.applied_stop_revision AND s.lifecycle='active' AND COALESCE(c.lifecycle,sp.lifecycle)='active' AND a.enabled AND root.work_submission_id IS NULL AND NOT EXISTS(
 SELECT FROM manager_human_requests h JOIN manager_inputs origin ON origin.id=h.authority_input_id
 WHERE h.id=root.human_request_id AND origin.work_submission_id IS NOT NULL)
 AND root.source_kind<>'task_report' AND root.admission_epoch=a.admission_epoch AND manager_input_actor_allowed(root.id) FROM manager_sessions s LEFT JOIN manager_conversations c ON c.id=s.conversation_id LEFT JOIN manager_task_spaces sp ON sp.id=s.task_space_id JOIN platform_agents a ON a.id=s.agent_id JOIN manager_inputs root ON root.id=$3 WHERE s.id=$1`, w.SessionID, t.ID, t.Input.ID).Scan(&valid)
		if err != nil || !valid {
			return err
		}
		v := Input{}
		err = tx.QueryRowContext(ctx, `SELECT i.id,i.seq,i.text,COALESCE(i.submitted_by::text,''),i.channel_principal_id,CASE WHEN i.trigger_event_id IS NOT NULL THEN 'trigger' WHEN i.channel_principal_id IS NULL THEN 'web' ELSE 'channel' END,i.context_state IS NOT NULL,i.created_at
 FROM manager_inputs i JOIN manager_inputs root ON root.id=$2 JOIN platform_agents a ON a.id=$3
 WHERE i.session_id=$1 AND i.status='queued' AND i.delivery_mode='steer' AND i.trigger_agent_loop AND i.source_kind<>'task_report'
 AND NOT EXISTS (SELECT 1 FROM manager_inputs held WHERE held.session_id=i.session_id AND held.status='queued' AND held.trigger_agent_loop AND held.seq<i.seq)
 AND NOT EXISTS (SELECT 1 FROM manager_inputs i WHERE i.session_id=$1 AND i.status='queued' AND (i.admission_epoch<>a.admission_epoch OR NOT manager_input_admission_allowed(i.id)))
 AND i.trigger_event_id IS NOT DISTINCT FROM root.trigger_event_id AND i.submitted_by IS NOT DISTINCT FROM root.submitted_by
 AND i.channel_principal_id IS NOT DISTINCT FROM root.channel_principal_id AND i.channel_generation IS NOT DISTINCT FROM root.channel_generation
 AND i.admission_epoch=a.admission_epoch AND manager_input_actor_allowed(i.id) ORDER BY i.seq LIMIT 1`, w.SessionID, t.Input.ID, w.AgentID).Scan(&v.ID, &v.Seq, &v.Text, &v.SubmittedBy, &v.ChannelPrincipalID, &v.PrincipalType, &v.NeedsContextReceipt, &v.CreatedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err = prepareBatch(ctx, tx, w, &v, t.ID); err != nil {
			return err
		}
		out = &v
		return nil
	})
	return out, err
}

// ReturnSteering is allowed only with positive native clear_queue evidence.
func (s *Store) ReturnSteering(ctx context.Context, w Work, t Turn, input string) error {
	return s.transaction(ctx, w, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `UPDATE manager_inputs SET status='queued',turn_id=NULL,started_at=NULL,context_state='pending',batch_input_id=NULL WHERE (id=$1 OR batch_input_id=$1) AND turn_id=$2 AND status='started' AND context_state IS DISTINCT FROM 'committed' RETURNING id`, input, t.ID)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }() //nolint:errcheck // Explicit close below.
		var ids []string
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		if err = errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		for _, id := range ids {
			if err = inputEvent(ctx, tx, w, id, ""); err != nil {
				return err
			}
		}
		return nil
	})
}
