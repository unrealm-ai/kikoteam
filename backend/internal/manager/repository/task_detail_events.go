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
	filter.SharedDetail = true
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
