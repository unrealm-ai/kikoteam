package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"time"
)

func (s *Store) TriggerEvents(ctx context.Context, w, actor, agent, id string, before time.Time, beforeID string, n int) ([]service.TriggerEvent, error) {
	out := []service.TriggerEvent{}
	err := s.readTriggerAdmin(ctx, w, actor, agent, func(tx *sql.Tx) error {
		if _, e := scanTrigger(tx.QueryRowContext(ctx, `SELECT `+triggerColumns+` FROM manager_triggers WHERE workspace_id=$1 AND agent_id=$2 AND id=$3`, w, agent, id)); e != nil {
			return e
		}
		rows, e := tx.QueryContext(ctx, `SELECT e.id,e.event_id,e.action,e.binding_version,e.status,e.received_at,e.receipt,COALESCE(p.event_name,''),COALESCE(t.status,i.status,''),
 CASE WHEN b.kind='schedule' THEN e.occurred_at END,
 CASE WHEN b.kind='schedule' AND e.status='rejected' THEN COALESCE(e.payload->>'schedule_skip_reason','') ELSE '' END
 FROM manager_trigger_events e JOIN manager_triggers b ON b.id=e.trigger_id LEFT JOIN manager_webhook_payloads p ON p.event_id=e.id
 LEFT JOIN manager_inputs i ON i.id=e.input_id LEFT JOIN manager_turns t ON t.id=i.turn_id
 WHERE e.trigger_id=$1 AND ($2::uuid IS NULL OR (e.received_at,e.id)<($3,$2)) ORDER BY e.received_at DESC,e.id DESC LIMIT $4`, id, nullID(beforeID), before, n)
		if e != nil {
			return e
		}
		defer func() { _ = rows.Close() }() //nolint:errcheck // Explicit close below.
		for rows.Next() {
			var v service.TriggerEvent
			var raw []byte
			if e = rows.Scan(&v.ID, &v.EventID, &v.Action, &v.BindingVersion, &v.Status, &v.ReceivedAt, &raw, &v.EventName, &v.ExecutionStatus, &v.ScheduledFor, &v.Reason); e != nil {
				return errors.Join(e, rows.Close())
			}
			if raw != nil {
				if e = json.Unmarshal(raw, &v.Receipt); e != nil {
					return errors.Join(e, rows.Close())
				}
			}
			out = append(out, v)
		}
		return errors.Join(rows.Err(), rows.Close())
	})
	return out, err
}
