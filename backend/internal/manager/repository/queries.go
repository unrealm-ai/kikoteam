package repository

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func (s *Store) Sessions(ctx context.Context, w, actor string, f service.SessionFilter) ([]service.Session, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+publicSessionColumns+sessionFrom+` WHERE s.workspace_id=$1 AND `+sessionRead(ctx, actor)+` AND ($3='' OR s.conversation_id::text=$3) AND ($4='' OR s.agent_id::text=$4) AND ($5='' OR s.mode=$5) AND ($6='' OR s.lifecycle=$6) AND ($7='' OR (s.created_at,s.id)<($8::timestamptz,NULLIF($7,'')::uuid)) AND ($10='' OR EXISTS(SELECT 1 FROM manager_tasks t WHERE t.id=s.task_id AND t.source_chat_session_id::text=$10)) AND ($11='' OR s.task_space_id::text=$11) AND ($12='' OR EXISTS(SELECT 1 FROM manager_tasks k WHERE k.id=s.task_id AND k.planning_state=$12)) AND ($13='' OR (`+taskExecutionSQL+`)=$13) AND ($14='' OR (SELECT status FROM manager_turns WHERE session_id=s.id ORDER BY turn_no DESC LIMIT 1)=$14) ORDER BY s.created_at DESC,s.id DESC LIMIT $9`, w, actor, f.Conversation, f.Agent, f.Mode, f.Lifecycle, f.BeforeID, f.BeforeTime, f.Limit, f.SourceChat, f.TaskSpace, f.PlanningState, f.ExecutionState, f.LatestTurnStatus)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Iteration errors are checked below.
	items := []service.Session{}
	for rows.Next() {
		st, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, service.PublicSession(st.Session))
	}
	return items, rows.Err()
}
func (s *Store) Input(ctx context.Context, w, actor, session, id string) (service.Input, error) {
	if _, err := s.Session(ctx, w, actor, session); err != nil {
		return service.Input{}, err
	}
	return scanInput(s.DB.QueryRowContext(ctx, `SELECT `+inputColumns+` FROM manager_inputs WHERE workspace_id=$1 AND session_id=$2 AND id=$3`, w, session, id))
}
func (s *Store) Inputs(ctx context.Context, w, actor, session, status string, after int64, limit int) ([]service.Input, error) {
	if _, err := s.Session(ctx, w, actor, session); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+inputColumns+` FROM manager_inputs WHERE workspace_id=$1 AND session_id=$2 AND seq>$3 AND ($4='' OR status=$4) ORDER BY seq LIMIT $5`, w, session, after, status, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Iteration errors are checked below.
	items := []service.Input{}
	for rows.Next() {
		v, err := scanInput(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, v)
	}
	return items, rows.Err()
}

const turnColumns = `id,session_id,input_id,turn_no,status,result_summary,error,usage,has_unknown_tool_results,started_at,finished_at`

func scanTurn(row scanner) (service.Turn, error) {
	var v service.Turn
	var failure, usage []byte
	err := row.Scan(&v.ID, &v.SessionID, &v.InputID, &v.TurnNo, &v.Status, &v.ResultSummary, &failure, &usage, &v.HasUnknownToolResults, &v.StartedAt, &v.FinishedAt)
	v.Error = failure
	v.Usage = usage
	return v, visibleError(err)
}
func (s *Store) Turn(ctx context.Context, w, actor, session, id string) (service.Turn, error) {
	if _, err := s.Session(ctx, w, actor, session); err != nil {
		return service.Turn{}, err
	}
	return scanTurn(s.DB.QueryRowContext(ctx, `SELECT `+turnColumns+` FROM manager_turns WHERE workspace_id=$1 AND session_id=$2 AND id=$3`, w, session, id))
}
func (s *Store) Turns(ctx context.Context, w, actor, session, status string, after int64, limit int) ([]service.Turn, error) {
	if _, err := s.Session(ctx, w, actor, session); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+turnColumns+` FROM manager_turns WHERE workspace_id=$1 AND session_id=$2 AND turn_no>$3 AND ($4='' OR status=$4) ORDER BY turn_no LIMIT $5`, w, session, after, status, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Iteration errors are checked below.
	items := []service.Turn{}
	for rows.Next() {
		v, err := scanTurn(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, v)
	}
	return items, rows.Err()
}
func (s *Store) Events(ctx context.Context, w, actor, session string, f service.EventFilter) ([]json.RawMessage, int64, error) {
	if _, err := s.Session(ctx, w, actor, session); err != nil {
		return nil, 0, err
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Read-only transaction; rollback releases the snapshot.
	high := f.High
	if high < 0 {
		if err = tx.QueryRowContext(ctx, `SELECT last_event_seq FROM manager_sessions WHERE id=$1`, session).Scan(&high); err != nil {
			return nil, 0, err
		}
	}
	query := `SELECT event FROM manager_session_events WHERE workspace_id=$1 AND session_id=$2 AND seq>$3 AND seq<=$4 AND ($5='' OR event->>'turn_id'=$5) AND ($6='' OR event->>'type'=ANY(string_to_array($6,','))) AND ($8=0 OR seq<$8) ORDER BY seq`
	if f.Backward {
		query += " DESC"
	}
	query += " LIMIT $7"
	rows, err := tx.QueryContext(ctx, query, w, session, f.After, high, f.Turn, f.Type, f.Limit, f.Before)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Iteration errors are checked below.
	items := []json.RawMessage{}
	for rows.Next() {
		var v json.RawMessage
		if err = rows.Scan(&v); err != nil {
			return nil, 0, err
		}
		items = append(items, v)
	}
	return items, high, rows.Err()
}
