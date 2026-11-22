package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"strings"
)

func (s *Store) TriggerNotificationCandidates(ctx context.Context, w, actor, agent string, limit int) ([]service.TriggerNotificationCandidate, bool, error) {
	out := []service.TriggerNotificationCandidate{}
	if limit < 1 || limit > 100 {
		return nil, false, service.Fail("invalid_argument", 400, "limit无效")
	}
	err := s.readTriggerAdmin(ctx, w, actor, agent, func(tx *sql.Tx) error {
		// Scope qualification uses the same trusted snapshot query as save/send.
		query := strings.Replace(noticeTargetSQL, "c.id=$1 AND", "c.workspace_id=$1 AND", 1) + ` AND c.agent_id=$3 ORDER BY c.created_at DESC,c.id LIMIT $4`
		rows, e := tx.QueryContext(ctx, query, w, actor, agent, limit+1)
		if e != nil {
			return e
		}
		defer func() { _ = rows.Close() }() //nolint:errcheck // Explicit close checked.
		for rows.Next() {
			var raw []byte
			if e = rows.Scan(&raw); e != nil {
				return e
			}
			var v struct {
				ConversationID string `json:"conversation_id"`
				Provider       string `json:"provider"`
				Chat           string `json:"chat_id"`
				Web            string `json:"web_group_id"`
			}
			if e = json.Unmarshal(raw, &v); e != nil {
				return e
			}
			label := v.Provider + "群 " + v.Chat
			if v.Provider == "web" {
				label = "Web群 " + v.Web
			}
			out = append(out, service.TriggerNotificationCandidate{ConversationID: v.ConversationID, Provider: v.Provider, Label: label})
		}
		return errors.Join(rows.Err(), rows.Close())
	})
	if err != nil {
		return nil, false, err
	}
	more := len(out) > limit
	if more {
		out = out[:limit]
	}
	return out, more, nil
}
func (s *Store) TriggerNotificationViews(ctx context.Context, w, actor, agent, trigger string, limit int) ([]service.TriggerNotificationView, bool, error) {
	out := []service.TriggerNotificationView{}
	if limit < 1 || limit > 100 || !service.ValidID(trigger) {
		return nil, false, service.Fail("invalid_argument", 400, "查询无效")
	}
	err := s.readTriggerAdmin(ctx, w, actor, agent, func(tx *sql.Tx) error {
		var id string
		if e := tx.QueryRowContext(ctx, `SELECT id FROM manager_triggers WHERE workspace_id=$1 AND agent_id=$2 AND id=$3`, w, agent, trigger).Scan(&id); e != nil {
			return visibleError(e)
		}
		rows, e := tx.QueryContext(ctx, `SELECT id,reminder_event_id,task_id,source_input_id,source_turn_id,conversation_id,status,context_status,context_error,error_code,created_at FROM manager_trigger_notifications WHERE trigger_id=$1 ORDER BY created_at DESC,id DESC LIMIT $2`, trigger, limit+1)
		if e != nil {
			return e
		}
		defer func() { _ = rows.Close() }() //nolint:errcheck // Explicit close checked.
		for rows.Next() {
			var v service.TriggerNotificationView
			if e = rows.Scan(&v.ID, &v.ReminderEventID, &v.TaskID, &v.SourceInputID, &v.SourceTurnID, &v.ConversationID, &v.Status, &v.ContextStatus, &v.ContextError, &v.ErrorCode, &v.CreatedAt); e != nil {
				return e
			}
			out = append(out, v)
		}
		return errors.Join(rows.Err(), rows.Close())
	})
	if err != nil {
		return nil, false, err
	}
	more := len(out) > limit
	if more {
		out = out[:limit]
	}
	return out, more, nil
}
