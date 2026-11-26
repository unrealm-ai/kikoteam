// Package repository commits execution facts under the Backend assignment.
package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

var ErrStale = errors.New("execution assignment is no longer current")

type Assignment struct {
	SessionID    string `json:"session_id"`
	AssignmentID string `json:"assignment_id"`
	Generation   int64  `json:"generation,string"`
}
type Work struct {
	ReportToChat *string
	Assignment
	EngineID, WorkspaceID, ConversationID, AgentID, Mode string
	TriggerID                                            string
	TaskSpaceID, TaskID                                  string
	NativeSessionID                                      *string
	Lifecycle, ParentLifecycle                           string
	StopSeq, StopRevision, AppliedStopRevision           int64
	ActiveTurnID                                         *string
	BackgroundCount                                      *int
	BlockReason                                          *string
}
type Input struct {
	CreatedAt             time.Time
	WorkSubmissionID      *string
	HumanRequestID        *string
	ConfirmationRequestID *string
	NeedsContextReceipt   bool
	CorrectionOnly        bool
	ID, Text, SubmittedBy string
	ChannelPrincipalID    *string
	PrincipalType         string
	Seq                   int64
}
type Turn struct {
	ID     string
	Number int64
	Input  Input
}
type Store struct {
	DB       *sql.DB
	EngineID string
}

func (s *Store) Refresh(ctx context.Context, a Assignment) (Work, error) {
	v := Work{Assignment: a, EngineID: s.EngineID}
	err := s.DB.QueryRowContext(ctx, `SELECT s.workspace_id,COALESCE(s.conversation_id::text,''),COALESCE(s.task_space_id::text,''),COALESCE(s.task_id::text,''),COALESCE(s.trigger_id::text,''),s.agent_id,s.mode,s.native_session_id,s.lifecycle,COALESCE(c.lifecycle,sp.lifecycle),s.stop_input_seq,s.stop_revision,s.applied_stop_revision,s.active_turn_id,s.background_process_count,s.block_reason,(SELECT k.source_chat_session_id FROM manager_tasks k WHERE k.id=s.task_id) FROM manager_sessions s LEFT JOIN manager_conversations c ON c.id=s.conversation_id LEFT JOIN manager_task_spaces sp ON sp.id=s.task_space_id WHERE s.id=$1 AND s.engine_id=$2 AND s.assignment_id=$3 AND s.generation=$4`, a.SessionID, s.EngineID, a.AssignmentID, a.Generation).Scan(&v.WorkspaceID, &v.ConversationID, &v.TaskSpaceID, &v.TaskID, &v.TriggerID, &v.AgentID, &v.Mode, &v.NativeSessionID, &v.Lifecycle, &v.ParentLifecycle, &v.StopSeq, &v.StopRevision, &v.AppliedStopRevision, &v.ActiveTurnID, &v.BackgroundCount, &v.BlockReason, &v.ReportToChat)
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrStale
	}
	return v, err
}
func (s *Store) transaction(ctx context.Context, w Work, fn func(*sql.Tx) error) error {
	return s.scopedTransaction(ctx, w, false, false, fn)
}
func (s *Store) scopedTransaction(ctx context.Context, w Work, admission, lockConversation bool, fn func(*sql.Tx) error) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Preserve operation error; rollback after commit is harmless.
	var id string
	if admission {
		if err = tx.QueryRowContext(ctx, `SELECT id FROM platform_agents WHERE id=$1 FOR SHARE`, w.AgentID).Scan(&id); err != nil {
			return err
		}
		if err = lockTrigger(ctx, tx, w); err != nil {
			return err
		}
		if err = lockOwner(ctx, tx, w, false).Scan(&id); err != nil {
			return err
		}
	}
	// Manager writes lock the parent before the session. Keep the same order
	// for an assistant reply, which also updates the conversation row.
	if lockConversation {
		if err = tx.QueryRowContext(ctx, `SELECT id FROM manager_conversations WHERE id=$1 AND workspace_id=$2 FOR NO KEY UPDATE`, w.ConversationID, w.WorkspaceID).Scan(&id); err != nil {
			return err
		}
	}
	if err = tx.QueryRowContext(ctx, `SELECT id FROM manager_sessions WHERE id=$1 AND engine_id=$2 AND assignment_id=$3 AND generation=$4 FOR UPDATE`, w.SessionID, s.EngineID, w.AssignmentID, w.Generation).Scan(&id); errors.Is(err, sql.ErrNoRows) {
		return ErrStale
	}
	if err != nil {
		return err
	}
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Claim(ctx context.Context, w Work, newID string) (*Turn, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Preserve original failure.
	var enabled bool
	var epoch int64
	if err = tx.QueryRowContext(ctx, `SELECT enabled,admission_epoch FROM platform_agents WHERE id=$1 FOR SHARE`, w.AgentID).Scan(&enabled, &epoch); err != nil {
		return nil, err
	}
	if err = lockTrigger(ctx, tx, w); err != nil {
		return nil, err
	}
	var parent string
	if err = lockOwner(ctx, tx, w, true).Scan(&parent); err != nil {
		return nil, err
	}
	var lifecycle string
	var active *string
	var stop, applied int64
	err = tx.QueryRowContext(ctx, `SELECT lifecycle,active_turn_id,stop_revision,applied_stop_revision FROM manager_sessions WHERE id=$1 AND engine_id=$2 AND assignment_id=$3 AND generation=$4 FOR UPDATE`, w.SessionID, s.EngineID, w.AssignmentID, w.Generation).Scan(&lifecycle, &active, &stop, &applied)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrStale
	}
	if err != nil {
		return nil, err
	}
	if lifecycle != "active" || parent != "active" || active != nil || stop != applied {
		return nil, nil
	}
	// Serialize a channel turn admission with binding revocation/reconfiguration.
	// Web conversations have no binding and retain their existing behavior.
	if _, err = tx.ExecContext(ctx, `SELECT b.id FROM platform_channel_bindings b JOIN manager_conversations c ON c.channel_binding_id=b.id WHERE c.id=NULLIF($1,'')::uuid FOR SHARE OF b`, w.ConversationID); err != nil {
		return nil, err
	}
	// Reject stale accepted epochs even if the employee was quickly re-enabled.
	rows, err := tx.QueryContext(ctx, `UPDATE manager_inputs i SET status='rejected',context_state=CASE WHEN context_state IS NOT NULL THEN 'absent' ELSE NULL END,rejection_reason=CASE WHEN NOT (manager_input_admission_allowed(i.id) OR manager_human_chat_input_allowed(i.id)) THEN 'access_revoked' ELSE 'agent_disabled' END,finished_at=now() WHERE session_id=$1 AND status='queued' AND (NOT $2 OR admission_epoch<>$3 OR NOT (manager_input_admission_allowed(i.id) OR manager_human_chat_input_allowed(i.id))) RETURNING id,seq,rejection_reason`, w.SessionID, enabled, epoch)
	if err != nil {
		return nil, err
	}
	type rejected struct {
		id, reason string
		seq        int64
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Rows are explicitly closed before subsequent transaction statements.
	rejects := []rejected{}
	for rows.Next() {
		var v rejected
		if err = rows.Scan(&v.id, &v.seq, &v.reason); err != nil {
			if e := rows.Close(); e != nil {
				return nil, errors.Join(err, e)
			}
			return nil, err
		}
		rejects = append(rejects, v)
	}
	rowErr := rows.Err()
	if err = rows.Close(); err != nil {
		return nil, err
	}
	if rowErr != nil {
		return nil, rowErr
	}
	for _, v := range rejects {
		if err = inputEvent(ctx, tx, w, v.id, ""); err != nil {
			return nil, err
		}
	}
	v := &Turn{ID: newID}
	err = tx.QueryRowContext(ctx, `SELECT id,seq,text,COALESCE(submitted_by::text,''),channel_principal_id,CASE WHEN work_assignment_id IS NOT NULL THEN 'work_item' WHEN notification_id IS NOT NULL THEN 'notification' WHEN trigger_event_id IS NOT NULL THEN 'trigger' WHEN channel_principal_id IS NULL THEN 'web' ELSE 'channel' END,context_state IS NOT NULL,confirmation_request_id,human_request_id,work_submission_id,created_at FROM manager_inputs WHERE session_id=$1 AND status='queued' AND trigger_agent_loop AND (work_submission_id IS NULL OR manager_work_review_ready(session_id)) AND (human_request_id IS NOT NULL OR confirmation_request_id IS NOT NULL OR work_submission_id IS NOT NULL OR (SELECT required_actions='[]' FROM manager_sessions WHERE id=$1)) ORDER BY (human_request_id IS NULL),(confirmation_request_id IS NULL),(work_submission_id IS NULL),seq LIMIT 1`, w.SessionID).Scan(&v.Input.ID, &v.Input.Seq, &v.Input.Text, &v.Input.SubmittedBy, &v.Input.ChannelPrincipalID, &v.Input.PrincipalType, &v.Input.NeedsContextReceipt, &v.Input.ConfirmationRequestID, &v.Input.HumanRequestID, &v.Input.WorkSubmissionID, &v.Input.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		if len(rejects) > 0 {
			if err = snapshot(ctx, tx, w); err != nil {
				return nil, err
			}
		}
		return nil, tx.Commit()
	}
	if err != nil {
		return nil, err
	}
	if err = useHumanResume(ctx, tx, &v.Input); err != nil {
		if !errors.Is(err, errHumanResumeScopeChanged) {
			return nil, err
		}
		if err = rejectChangedHumanResume(ctx, tx, w, v.Input); err != nil {
			return nil, err
		}
		return nil, tx.Commit()
	}
	resumeText := v.Input.Text
	if err = tx.QueryRowContext(ctx, `UPDATE manager_sessions SET last_turn_no=last_turn_no+1,active_turn_id=$2 WHERE id=$1 RETURNING last_turn_no`, w.SessionID, v.ID).Scan(&v.Number); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO manager_turns(id,workspace_id,session_id,input_id,turn_no,assignment_id,generation,status) VALUES($1,$2,$3,$4,$5,$6,$7,'running')`, v.ID, w.WorkspaceID, w.SessionID, v.Input.ID, v.Number, w.AssignmentID, w.Generation); err != nil {
		return nil, err
	}
	if err = prepareBatch(ctx, tx, w, &v.Input, v.ID); err != nil {
		return nil, err
	}
	if v.Input.HumanRequestID != nil {
		v.Input.Text = resumeText
	}
	if err = turnEvent(ctx, tx, w, *v); err != nil {
		return nil, err
	}
	if err = snapshot(ctx, tx, w); err != nil {
		return nil, err
	}
	return v, tx.Commit()
}

func (s *Store) SetNative(ctx context.Context, w Work, id string) error {
	return s.transaction(ctx, w, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE manager_sessions SET native_session_id=$2 WHERE id=$1`, w.SessionID, id)
		return err
	})
}
func (s *Store) Event(ctx context.Context, w Work, t Turn, kind string, fields map[string]any) error {
	reply := w.Mode == "chat" && w.ConversationID != "" && kind == "agent.message" && fields["status"] == "completed"
	return s.scopedTransaction(ctx, w, false, reply, func(tx *sql.Tx) error {
		var active *string
		if err := tx.QueryRowContext(ctx, `SELECT active_turn_id FROM manager_sessions WHERE id=$1`, w.SessionID).Scan(&active); err != nil {
			return err
		}
		if active == nil || *active != t.ID {
			return ErrStale
		}
		if kind == "agent.tool_result" && fields["status"] == "unknown" {
			if _, err := tx.ExecContext(ctx, `UPDATE manager_turns SET has_unknown_tool_results=true WHERE id=$1`, t.ID); err != nil {
				return err
			}
		}
		if err := emit(ctx, tx, w, kind, &t.Input.ID, &t.ID, fields); err != nil || !reply {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE manager_conversations SET last_message_at=GREATEST(last_message_at,now()) WHERE id=$1 AND workspace_id=$2`, w.ConversationID, w.WorkspaceID)
		return err
	})
}

// InterruptPreviousTurn settles an abandoned Turn under the replacement
// assignment. Complete checks both the current assignment and active Turn in
// its transaction; the original Input never returns to the dispatch queue.
func (s *Store) InterruptPreviousTurn(ctx context.Context, w Work) (bool, error) {
	var t Turn
	err := s.DB.QueryRowContext(ctx, `SELECT t.id,t.input_id,i.seq FROM manager_turns t
		JOIN manager_inputs i ON i.id=t.input_id WHERE t.id=$1 AND t.session_id=$2
		AND t.generation<$3 AND t.assignment_id<>$4`, w.ActiveTurnID, w.SessionID, w.Generation, w.AssignmentID).Scan(&t.ID, &t.Input.ID, &t.Input.Seq)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, s.Complete(ctx, w, t, "interrupted", "engine_restarted", "")
}

func (s *Store) Complete(ctx context.Context, w Work, t Turn, status, code, summary string) error {
	return s.transaction(ctx, w, func(tx *sql.Tx) error {
		var active *string
		var stopSeq, stop, applied int64
		if err := tx.QueryRowContext(ctx, `SELECT active_turn_id,stop_input_seq,stop_revision,applied_stop_revision FROM manager_sessions WHERE id=$1`, w.SessionID).Scan(&active, &stopSeq, &stop, &applied); err != nil {
			return err
		}
		if active == nil || *active != t.ID {
			return ErrStale
		}
		if stop > applied && t.Input.Seq <= stopSeq && (status == "completed" || code == "configuration_unusable" || code == "awaiting_confirmation" || code == "awaiting_work_commit" || code == "work_boundary_unresolved") {
			status = "canceled"
			code = ""
		}
		if err := settleHumanRequest(ctx, tx, w, t.ID, status, code); err != nil {
			return err
		}
		if err := settleWorkBoundary(ctx, tx, w, t.ID, status, code); err != nil {
			return err
		}
		if code != "awaiting_confirmation" && code != "engine_shutdown" {
			if err := rejectClosedConfirmations(ctx, tx, w, t); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE manager_sessions SET required_actions=(SELECT COALESCE(jsonb_agg(q ORDER BY ord),'[]') FROM jsonb_array_elements(required_actions) WITH ORDINALITY AS x(q,ord) WHERE q->>'turn_id'<>$2 OR q->>'type' IN ('human_confirmation','work_item')) WHERE id=$1 AND required_actions<>'[]'`, w.SessionID, t.ID); err != nil {
				return err
			}
		}
		var failure any
		if code == "engine_restarted" {
			// A submitted native tool may have taken effect before the crash.
			if _, err := tx.ExecContext(ctx, `UPDATE manager_turns SET has_unknown_tool_results=true WHERE id=$1 AND EXISTS (
				SELECT 1 FROM manager_session_events u WHERE u.session_id=$2 AND u.event->>'turn_id'=$1::text
				AND u.event->>'type'='agent.tool_use' AND NOT EXISTS (
					SELECT 1 FROM manager_session_events r WHERE r.session_id=u.session_id AND r.event->>'turn_id'=u.event->>'turn_id'
					AND r.event->>'type'='agent.tool_result' AND r.event->>'tool_use_id'=u.event->>'tool_use_id'))`, t.ID, w.SessionID); err != nil {
				return err
			}
		}
		if code != "" {
			message := "执行未正常完成，请查看会话状态并按需提交新输入"
			if code == "human_confirmation_unresolved" {
				message = "人工确认停稳需要核对"
			}
			if code == "awaiting_confirmation" {
				message = "本轮已结束，等待用户确认后继续"
			}
			if code == "human_reapproval_required" {
				message = "修改后尚未发起重新确认，原任务仍等待批准"
			}
			if code == "work_boundary_unresolved" {
				message = "事项交接停稳需要核对"
			}
			if code == "awaiting_work_commit" {
				message = "本轮已结束，等待提交事项动作"
			}
			data, err := json.Marshal(map[string]string{"code": code, "message": message})
			if err != nil {
				return err
			}
			failure = data
		}
		if _, err := tx.ExecContext(ctx, `UPDATE manager_turns SET status=$2,error=$3,result_summary=NULLIF($4,''),finished_at=now(),has_unknown_tool_results=has_unknown_tool_results OR EXISTS(SELECT 1 FROM manager_capability_calls WHERE turn_id=$1 AND status='authorized') WHERE id=$1`, t.ID, status, failure, strings.ReplaceAll(summary, "\x00", "\uFFFD")); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE manager_inputs SET status='processed',finished_at=now(),context_state=CASE WHEN context_state='pending' THEN 'unknown' ELSE context_state END WHERE turn_id=$1 AND status='started'`, t.ID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE manager_sessions SET active_turn_id=NULL,block_reason=CASE WHEN EXISTS(SELECT 1 FROM manager_capability_calls WHERE session_id=$1 AND status='authorized') THEN 'remote_tool_outcome_unknown' ELSE block_reason END WHERE id=$1`, w.SessionID); err != nil {
			return err
		}
		if status != "canceled" {
			if err := returnAbsentContext(ctx, tx, w, t.ID); err != nil {
				return err
			}
		}
		if err := completedInputEvents(ctx, tx, w, t); err != nil {
			return err
		}
		if err := turnEvent(ctx, tx, w, t); err != nil {
			return err
		}
		return snapshot(ctx, tx, w)
	})
}

func inputEvent(ctx context.Context, tx *sql.Tx, w Work, input, turn string) error {
	var data []byte
	if err := tx.QueryRowContext(ctx, `SELECT jsonb_build_object('attachments',attachments,'confirmation_request_id',confirmation_request_id,'confirmed',confirmed,'trigger_agent_loop',trigger_agent_loop,'delivery_mode',delivery_mode,'source_kind',source_kind,'report_id',report_id,'message_id',id,'input_seq',seq::text,'submitted_by',submitted_by,'channel_principal_id',channel_principal_id,'principal_type',CASE WHEN work_assignment_id IS NOT NULL THEN 'work_item' WHEN notification_id IS NOT NULL THEN 'notification' WHEN trigger_event_id IS NOT NULL THEN 'trigger' WHEN channel_principal_id IS NULL THEN 'web' ELSE 'channel' END,'content',jsonb_build_array(jsonb_build_object('type','text','text',text)),'status',status,'cancel_reason',cancel_reason,'rejection',CASE WHEN rejection_reason IS NULL THEN NULL ELSE jsonb_build_object('code',rejection_reason,'message','输入未执行') END,'received_at',created_at,'started_at',started_at,'finished_at',finished_at) FROM manager_inputs WHERE id=$1`, input).Scan(&data); err != nil {
		return err
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	var turnID *string
	if turn != "" {
		turnID = &turn
	}
	return emit(ctx, tx, w, "input.updated", &input, turnID, fields)
}
func (s *Store) AppliedStop(ctx context.Context, w Work, revision int64) error {
	return s.transaction(ctx, w, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE manager_sessions SET applied_stop_revision=GREATEST(applied_stop_revision,$2) WHERE id=$1 AND active_turn_id IS NULL AND stop_revision>=$2`, w.SessionID, revision)
		if err != nil {
			return err
		}
		return snapshot(ctx, tx, w)
	})
}

// Activity records complete Runner evidence. Unknown work is never reported as
// zero, and only a changed observation emits a reliable Session snapshot.
func (s *Store) Activity(ctx context.Context, w Work, count *int, reason *string) error {
	return s.transaction(ctx, w, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE manager_sessions SET background_process_count=$2,block_reason=CASE WHEN block_reason='remote_tool_outcome_unknown' THEN block_reason ELSE $3 END WHERE id=$1 AND (block_reason IS NULL OR block_reason='sandbox_activity_unknown' OR $3::text IS NOT NULL) AND (background_process_count IS DISTINCT FROM $2::integer OR block_reason IS DISTINCT FROM $3::text)`, w.SessionID, count, reason)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil || n == 0 {
			return err
		}
		return snapshot(ctx, tx, w)
	})
}

func emit(ctx context.Context, tx *sql.Tx, w Work, kind string, input, turn *string, fields map[string]any) error {
	var seq int64
	if err := tx.QueryRowContext(ctx, `UPDATE manager_sessions SET last_event_seq=last_event_seq+1 WHERE id=$1 RETURNING last_event_seq`, w.SessionID).Scan(&seq); err != nil {
		return err
	}
	fields["type"] = kind
	fields["schema_version"] = 1
	fields["seq"] = strconv.FormatInt(seq, 10)
	fields["session_id"] = w.SessionID
	fields["input_id"] = input
	fields["turn_id"] = turn
	fields["occurred_at"] = time.Now().UTC()
	data, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	replaceJSONNUL(data)
	_, err = tx.ExecContext(ctx, `INSERT INTO manager_session_events(workspace_id,session_id,seq,event) VALUES($1,$2,$3,$4)`, w.WorkspaceID, w.SessionID, seq, data)
	return err
}
func turnEvent(ctx context.Context, tx *sql.Tx, w Work, t Turn) error {
	var fields []byte
	err := tx.QueryRowContext(ctx, `SELECT jsonb_build_object('turn_no',turn_no::text,'status',status,'result_summary',result_summary,'error',error,'usage',usage,'has_unknown_tool_results',has_unknown_tool_results,'started_at',started_at,'finished_at',finished_at) FROM manager_turns WHERE id=$1`, t.ID).Scan(&fields)
	if err != nil {
		return err
	}
	var v map[string]any
	if err = json.Unmarshal(fields, &v); err != nil {
		return err
	}
	return emit(ctx, tx, w, "turn.updated", &t.Input.ID, &t.ID, v)
}
func snapshot(ctx context.Context, tx *sql.Tx, w Work) error {
	var fields []byte
	err := tx.QueryRowContext(ctx, `SELECT jsonb_build_object('title',s.title,'lifecycle',s.lifecycle,'revision',s.revision::text,'archived_at',s.archived_at,'execution',jsonb_build_object('required_actions',s.required_actions,'state',CASE WHEN s.block_reason IS NOT NULL OR s.background_process_count IS NULL THEN 'blocked' WHEN s.stop_revision>s.applied_stop_revision THEN 'stopping' WHEN s.active_turn_id IS NOT NULL THEN 'running' WHEN EXISTS(SELECT 1 FROM manager_inputs i WHERE i.session_id=s.id AND i.status='queued') THEN 'queued' ELSE 'idle' END,'active_turn_id',s.active_turn_id,'queued_input_count',(SELECT count(*) FROM manager_inputs i WHERE i.session_id=s.id AND i.status='queued'),'background_process_count',s.background_process_count,'block_reason',s.block_reason)) FROM manager_sessions s WHERE s.id=$1`, w.SessionID).Scan(&fields)
	if err != nil {
		return err
	}
	var v map[string]any
	if err = json.Unmarshal(fields, &v); err != nil {
		return err
	}
	return emit(ctx, tx, w, "session.updated", nil, nil, v)
}

// Owner rows serialize lifecycle and TaskSpace grants with execution admission.
func lockOwner(ctx context.Context, tx *sql.Tx, w Work, lifecycle bool) *sql.Row {
	column := "id::text"
	if lifecycle {
		column = "lifecycle"
	}
	if w.TaskSpaceID != "" {
		return tx.QueryRowContext(ctx, "SELECT "+column+" FROM manager_task_spaces WHERE id=$1 AND workspace_id=$2 FOR SHARE", w.TaskSpaceID, w.WorkspaceID)
	}
	return tx.QueryRowContext(ctx, "SELECT "+column+" FROM manager_conversations WHERE id=$1 AND workspace_id=$2 FOR SHARE", w.ConversationID, w.WorkspaceID)
}

func lockTrigger(ctx context.Context, tx *sql.Tx, w Work) error {
	if w.TriggerID == "" {
		return nil
	}
	var id string
	return tx.QueryRowContext(ctx, `SELECT id FROM manager_triggers WHERE id=$1 AND workspace_id=$2 AND agent_id=$3 FOR SHARE`, w.TriggerID, w.WorkspaceID, w.AgentID).Scan(&id)
}
