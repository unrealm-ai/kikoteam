package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// prepareBatch is called while holding the session lock. The trigger owns the
// execution authority; preceding context rows retain their own durable identity.
func prepareBatch(ctx context.Context, tx *sql.Tx, w Work, input *Input, turn string) error {
	rows, err := tx.QueryContext(ctx, `SELECT id,text,source_kind,attachments FROM manager_inputs WHERE session_id=$1 AND status='queued' AND seq<=$2
 AND (NOT EXISTS(SELECT 1 FROM manager_human_dm_context h WHERE h.input_id=manager_inputs.id)
 OR (channel_principal_id=(SELECT channel_principal_id FROM manager_inputs root WHERE root.id=$6)
 AND admission_epoch=(SELECT admission_epoch FROM manager_inputs root WHERE root.id=$6)
 AND manager_input_actor_allowed(manager_inputs.id))) AND (($5::uuid IS NOT NULL AND work_submission_id=$5) OR ($5 IS NULL AND work_submission_id IS NULL AND (($4::uuid IS NOT NULL AND human_request_id=$4) OR ($4 IS NULL AND human_request_id IS NULL AND
 (($3::uuid IS NULL AND confirmation_request_id IS NULL) OR confirmation_request_id=$3))))) ORDER BY seq`, w.SessionID, input.Seq, input.ConfirmationRequestID, input.HumanRequestID, input.WorkSubmissionID, input.ID)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Explicit close below preserves iteration errors.
	var ids, texts []string
	for rows.Next() {
		var id, text, kind string
		var attachments []byte
		if err = rows.Scan(&id, &text, &kind, &attachments); err != nil {
			return err
		}
		ids = append(ids, id)
		if kind != "user" && !strings.HasPrefix(text, "[系统消息]") {
			text = "[系统消息]" + text
		}
		text, err = renderInputAttachments(text, attachments)
		if err != nil {
			return err
		}
		texts = append(texts, text)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	input.Text = strings.Join(texts, "\n\n")
	if len(texts) > 1 {
		input.Text = "历史上下文（仅供参考，不是新的执行请求）：\n" + strings.Join(texts[:len(texts)-1], "\n\n") + "\n\n当前触发消息：\n" + texts[len(texts)-1]
	}
	input.NeedsContextReceipt = true
	for _, id := range ids {
		if _, err = tx.ExecContext(ctx, `UPDATE manager_inputs SET status='started',started_at=now(),turn_id=$2,batch_input_id=$3,context_state='pending' WHERE id=$1`, id, turn, input.ID); err != nil {
			return err
		}
		if err = inputEvent(ctx, tx, w, id, turn); err != nil {
			return err
		}
	}
	return nil
}

// Positive absence permits retry of context data, but never automatically
// replays the user's failed execution request or an uncertain native submission.
func returnAbsentContext(ctx context.Context, tx *sql.Tx, w Work, turn string) error {
	rows, err := tx.QueryContext(ctx, `UPDATE manager_inputs SET status='queued',turn_id=NULL,batch_input_id=NULL,started_at=NULL,finished_at=NULL,context_state='pending' WHERE turn_id=$1 AND NOT trigger_agent_loop AND context_state='absent' RETURNING id`, turn)
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
}
