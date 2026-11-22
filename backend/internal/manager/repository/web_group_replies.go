package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func (s *Store) MaterializeWebGroupReplies(ctx context.Context) error {
	rows, err := s.DB.QueryContext(ctx, `SELECT i.id,c.web_group_id FROM manager_inputs i JOIN manager_sessions s ON s.id=i.session_id JOIN manager_conversations c ON c.id=s.conversation_id LEFT JOIN manager_turns t ON t.id=i.turn_id WHERE c.web_group_id IS NOT NULL AND s.mode='chat' AND i.trigger_agent_loop AND NOT i.web_group_reply_done AND (i.status IN ('rejected','canceled') OR t.status IN ('completed','failed','canceled','interrupted')) ORDER BY i.created_at,i.id LIMIT 20`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Explicit closes report errors.
	type item struct{ id, group string }
	items := []item{}
	for rows.Next() {
		var v item
		if err = rows.Scan(&v.id, &v.group); err != nil {
			return errors.Join(err, rows.Close())
		}
		items = append(items, v)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, item := range items {
		if err = s.materializeWebGroupReply(ctx, item.id, item.group); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) materializeWebGroupReply(ctx context.Context, id, group string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	var locked string
	if err = tx.QueryRowContext(ctx, `SELECT id FROM platform_web_groups WHERE id=$1 FOR SHARE`, group).Scan(&locked); err != nil {
		return err
	}
	var w, agent, input, state, turn, name string
	var allowed bool
	err = tx.QueryRowContext(ctx, `SELECT i.workspace_id,s.agent_id,i.id,i.status,COALESCE(t.status,''),a.name,manager_input_actor_allowed(i.id) AND i.admission_epoch=a.admission_epoch AND a.enabled FROM manager_inputs i JOIN manager_sessions s ON s.id=i.session_id JOIN manager_conversations c ON c.id=s.conversation_id JOIN platform_agents a ON a.id=s.agent_id LEFT JOIN manager_turns t ON t.id=i.turn_id WHERE i.id=$1 AND c.web_group_id=$2 AND s.mode='chat' AND i.trigger_agent_loop AND NOT i.web_group_reply_done AND (i.status IN ('rejected','canceled') OR t.status IN ('completed','failed','canceled','interrupted')) FOR UPDATE OF i FOR SHARE OF a`, id, group).Scan(&w, &agent, &input, &state, &turn, &name, &allowed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if allowed {
		texts, e := webGroupOutputTexts(ctx, tx, input)
		if e != nil {
			return e
		}
		seq, e := nextWebGroupSeq(ctx, tx, group)
		if e != nil {
			return e
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO manager_web_group_messages(id,workspace_id,group_id,seq,author_kind,agent_id,display_name,text,origin_input_id) VALUES($1,$2,$3,$4,'assistant',$5,$6,$7,$8) ON CONFLICT(origin_input_id) DO NOTHING`, service.NewID(), w, group, seq, agent, channelLabel(name), channelReplyText(state, turn, texts), input)
		if err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE manager_inputs SET web_group_reply_done=true WHERE id=$1`, id); err != nil {
		return err
	}
	return tx.Commit()
}
func webGroupOutputTexts(ctx context.Context, tx *sql.Tx, input string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT event FROM manager_session_events WHERE event->>'input_id'=$1 AND event->>'type'='agent.message' ORDER BY seq`, input)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Explicit close below.
	out := []string{}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var event struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if err = json.Unmarshal(raw, &event); err != nil {
			return nil, err
		}
		for _, v := range event.Content {
			if v.Type == "text" && v.Text != "" {
				out = append(out, v.Text)
			}
		}
	}
	return out, errors.Join(rows.Err(), rows.Close())
}
