package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

// Capture trusted channel activity without manufacturing an inbound event or
// creating a runnable session. Flush immediately if its chat session exists.
func enqueueHumanDMContext(ctx context.Context, tx *sql.Tx, conversation, principal string, generation int64, source, body string, system bool) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO manager_human_dm_context(source_id,conversation_id,principal_id,connection_generation,admission_epoch,text,is_system)
 SELECT $4,c.id,$2,$3,a.admission_epoch,$5,$6 FROM manager_conversations c JOIN platform_agents a ON a.id=c.agent_id
 WHERE c.id=$1 AND c.lifecycle='active' AND a.enabled AND manager_channel_actor_allowed($2,c.id,$3) ON CONFLICT DO NOTHING`,
		conversation, principal, generation, source, body, system)
	if err != nil {
		return err
	}
	return flushExistingHumanDMContext(ctx, tx, conversation)
}

func flushExistingHumanDMContext(ctx context.Context, tx *sql.Tx, conversation string) error {
	var session, workspace string
	err := tx.QueryRowContext(ctx, `SELECT id,workspace_id FROM manager_sessions WHERE conversation_id=$1 AND mode='chat' AND lifecycle='active' FOR UPDATE`, conversation).Scan(&session, &workspace)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return flushHumanDMContext(ctx, tx, workspace, session, conversation)
}

// A late receipt can complete the history of a legacy fixed reply that was
// authorized before retirement. Agent replies already have their chat history.
func captureLegacyHumanReplyContext(ctx context.Context, tx *sql.Tx, id string) error {
	var conversation, principal, body string
	var generation int64
	err := tx.QueryRowContext(ctx, `SELECT n.conversation_id,n.principal_id,n.connection_generation,d.text
 FROM manager_channel_deliveries d JOIN manager_channel_inbox n ON n.id=d.inbox_id
 JOIN manager_human_dm_context captured ON captured.source_id=n.id
 WHERE d.id=$1 AND d.kind='human_confirmation' AND d.status='sent'
 AND manager_human_channel_inbox_current(n.id,n.principal_id)`, id).Scan(&conversation, &principal, &generation, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return enqueueHumanDMContext(ctx, tx, conversation, principal, generation, id, body, true)
}

func flushHumanDMContext(ctx context.Context, tx *sql.Tx, workspace, session, conversation string) error {
	rows, err := tx.QueryContext(ctx, `SELECT d.source_id,d.principal_id,d.connection_generation,d.admission_epoch,d.text,d.is_system
 FROM manager_human_dm_context d JOIN platform_agents a ON a.id=(SELECT c.agent_id FROM manager_conversations c WHERE c.id=d.conversation_id)
 WHERE d.conversation_id=$1 AND d.input_id IS NULL AND d.admission_epoch=a.admission_epoch
 AND manager_channel_actor_allowed(d.principal_id,d.conversation_id,d.connection_generation)
 ORDER BY d.created_at,d.source_id FOR UPDATE OF d`, conversation)
	if err != nil {
		return err
	}
	type pending struct {
		source, principal, text string
		generation, epoch       int64
		system                  bool
	}
	var queue []pending
	for rows.Next() {
		var item pending
		if err = rows.Scan(&item.source, &item.principal, &item.generation, &item.epoch, &item.text, &item.system); err != nil {
			return errors.Join(err, rows.Close())
		}
		queue = append(queue, item)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	t := sessionTx{ctx: ctx, tx: tx, target: service.Target{Workspace: workspace, Session: session}}
	for _, item := range queue {
		sourceKind, body := "user", item.text
		if item.system {
			sourceKind, body = "system", "[系统消息]"+strings.TrimPrefix(body, "[系统消息]")
		}
		if err = insertHumanDMContext(&t, item.source, item.principal, item.generation, item.epoch, sourceKind, body); err != nil {
			return err
		}
	}
	return nil
}

func insertHumanDMContext(t *sessionTx, source, principal string, generation, epoch int64, kind, body string) error {
	id := service.NewID()
	var seq int64
	if err := t.tx.QueryRowContext(t.ctx, `UPDATE manager_sessions SET last_input_seq=last_input_seq+1 WHERE id=$1 RETURNING last_input_seq`, t.target.Session).Scan(&seq); err != nil {
		return err
	}
	if _, err := t.tx.ExecContext(t.ctx, `INSERT INTO manager_inputs(id,workspace_id,session_id,channel_principal_id,channel_generation,admission_epoch,seq,text,source_kind,delivery_mode,trigger_agent_loop,context_state,created_at)
 SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9,'queue',false,'pending',created_at FROM manager_human_dm_context WHERE source_id=$10`,
		id, t.target.Workspace, t.target.Session, principal, generation, epoch, seq, body, kind, source); err != nil {
		return err
	}
	if _, err := t.tx.ExecContext(t.ctx, `UPDATE manager_human_dm_context SET input_id=$2 WHERE source_id=$1`, source, id); err != nil {
		return err
	}
	input, err := t.Input(id)
	if err != nil {
		return err
	}
	return t.Event("input.updated", &id, nil, service.InputFields(input))
}
