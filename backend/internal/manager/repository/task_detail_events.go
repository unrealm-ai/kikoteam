package repository

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

// TaskDetailEvents uses the same event records as Web. The SQL target comes
// only from the grant, never from browser-supplied Session or Turn IDs.
func (s *Store) TaskDetailEvents(ctx context.Context, grant service.TaskDetailGrant, filter service.EventFilter) ([]json.RawMessage, int64, error) {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Preserve the read failure.
	g, err := s.readTaskDetailGrant(ctx, tx, grant.ID, false)
	if err != nil {
		return nil, 0, err
	}
	filter.Turn = ""
	if g.TriggerNotificationID != "" {
		err = tx.QueryRowContext(ctx, `SELECT t.id FROM manager_trigger_notifications n
 JOIN manager_turns t ON t.id=n.source_turn_id AND t.session_id=$2 WHERE n.id=$1`, g.TriggerNotificationID, g.SessionID).Scan(&filter.Turn)
		if err != nil {
			return nil, 0, visibleError(err)
		}
	}
	items, high, err := readSessionEvents(ctx, tx, g.WorkspaceID, g.SessionID, filter)
	if err != nil {
		return nil, 0, err
	}
	if err = tx.Commit(); err != nil {
		return nil, 0, err
	}
	_, err = s.RecheckTaskDetailGrant(ctx, g.ID, false)
	return items, high, err
}
