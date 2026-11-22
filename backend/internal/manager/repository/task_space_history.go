package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

// Authorization and target projection share one database snapshot. The actual
// TaskSpace is inferred from the running input, not supplied by a tool caller.
const taskSpaceHistoryTarget = ` WHERE s.workspace_id=$1 AND s.task_space_id=$2 AND s.mode='task'`

type taskSpaceHistorySource struct {
	workspace, space, turn string
}

const taskSpaceHistorySourceSQL = `SELECT s.workspace_id,s.task_space_id,t.id
 FROM manager_sessions s JOIN manager_task_spaces p ON p.id=s.task_space_id AND p.workspace_id=s.workspace_id
 JOIN platform_agents a ON a.id=s.agent_id AND a.workspace_id=s.workspace_id
 JOIN manager_turns t ON t.id=s.active_turn_id AND t.session_id=s.id
 JOIN manager_inputs i ON i.id=t.input_id AND i.session_id=s.id
 LEFT JOIN manager_human_requests h ON h.id=i.human_request_id
 WHERE s.id=$1 AND s.engine_id=$2 AND s.assignment_id=$3 AND s.generation=$4 AND i.id=$5
 AND t.assignment_id=$3 AND t.generation=$4 AND t.status='running' AND i.status='started' AND i.turn_id=t.id
 AND s.mode='task' AND s.task_id IS NOT NULL AND s.lifecycle='active' AND p.lifecycle='active'
 AND s.stop_revision=s.applied_stop_revision AND a.enabled AND i.admission_epoch=a.admission_epoch
 AND manager_input_actor_allowed(i.id)
 AND (s.trigger_id IS NULL OR EXISTS (
   SELECT 1 FROM manager_triggers b JOIN manager_trigger_events e ON e.trigger_id=b.id
   WHERE b.id=s.trigger_id AND b.enabled AND b.workspace_id=s.workspace_id AND b.agent_id=s.agent_id
   AND b.version=s.trigger_version AND e.binding_version=b.version AND e.status='accepted'
   AND e.id=i.trigger_event_id AND e.workspace_id=s.workspace_id AND e.task_id=s.task_id AND e.session_id=s.id
   AND e.input_id=COALESCE(h.authority_input_id,i.id)
 ))`

func taskSpaceHistoryAuthority(ctx context.Context, tx *sql.Tx, id service.TaskSpaceHistoryIdentity, write bool) (taskSpaceHistorySource, error) {
	query := taskSpaceHistorySourceSQL
	if write {
		query += " FOR SHARE OF a,p,s,t,i"
	}
	var src taskSpaceHistorySource
	err := tx.QueryRowContext(ctx, query, id.SessionID, id.EngineID, id.AssignmentID, id.Generation, id.InputID).
		Scan(&src.workspace, &src.space, &src.turn)
	if errors.Is(err, sql.ErrNoRows) {
		return src, service.Fail("task_space_history_forbidden", 403, "当前执行没有任务空间摘要权限")
	}
	return src, err
}

func (s *Store) withTaskSpaceHistory(ctx context.Context, id service.TaskSpaceHistoryIdentity, write bool, action func(*sql.Tx, taskSpaceHistorySource) error) error {
	options := &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}
	if write {
		// Authorization locks the current execution rows; the subsequent upsert
		// can serialize concurrent partial notes without a stale write snapshot.
		options = &sql.TxOptions{Isolation: sql.LevelReadCommitted}
	}
	tx, err := s.DB.BeginTx(ctx, options)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit releases a successful snapshot.
	src, err := taskSpaceHistoryAuthority(ctx, tx, id, write)
	if err != nil {
		return err
	}
	if err = action(tx, src); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) TaskSpaceHistoryTasks(ctx context.Context, id service.TaskSpaceHistoryIdentity, f service.TaskFilter) ([]service.Task, error) {
	var items []service.Task
	err := s.withTaskSpaceHistory(ctx, id, false, func(tx *sql.Tx, src taskSpaceHistorySource) error {
		rows, err := tx.QueryContext(ctx, `SELECT `+taskColumns+`,NULL::uuid`+taskFrom+taskSpaceHistoryTarget+`
 AND ($3='' OR (k.created_at,k.id)<($4::timestamptz,NULLIF($3,'')::uuid)) ORDER BY k.created_at DESC,k.id DESC LIMIT $5`,
			src.workspace, src.space, f.BeforeID, f.BeforeTime, f.Limit)
		if err == nil {
			items, err = scanTasks(rows)
		}
		return err
	})
	return items, err
}

func (s *Store) TaskSpaceHistoryDetail(ctx context.Context, id service.TaskSpaceHistoryIdentity, task string) (service.TaskSpaceHistoryDetail, error) {
	var out service.TaskSpaceHistoryDetail
	err := s.withTaskSpaceHistory(ctx, id, false, func(tx *sql.Tx, src taskSpaceHistorySource) error {
		v, err := scanTask(tx.QueryRowContext(ctx, `SELECT `+taskColumns+`,NULL::uuid`+taskFrom+taskSpaceHistoryTarget+` AND k.id=$3`, src.workspace, src.space, task))
		if err != nil {
			return err
		}
		out.Task = v.TaskSpaceHistorySummary()
		out.ProgressSummary, err = loadTaskSpaceNote(ctx, tx, v.SessionID, false)
		if err != nil {
			return err
		}
		out.ResultSummary, err = loadTaskSpaceNote(ctx, tx, v.SessionID, true)
		return err
	})
	return out, err
}

func loadTaskSpaceNote(ctx context.Context, tx *sql.Tx, session string, result bool) (*service.TaskSpaceHistoryNote, error) {
	var note service.TaskSpaceHistoryNote
	err := tx.QueryRowContext(ctx, `SELECT t.id,CASE WHEN $2 THEN n.result_text ELSE n.progress_text END,
 CASE WHEN $2 THEN n.result_updated_at ELSE n.progress_updated_at END
 FROM manager_turns t JOIN manager_task_space_summaries n ON n.turn_id=t.id WHERE t.session_id=$1
 AND (CASE WHEN $2 THEN n.result_text IS NOT NULL AND t.status='completed' AND NOT t.has_unknown_tool_results
 ELSE n.progress_text IS NOT NULL END) ORDER BY t.turn_no DESC LIMIT 1`, session, result).
		Scan(&note.TurnID, &note.Text, &note.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &note, err
}

func (s *Store) SaveTaskSpaceSummary(ctx context.Context, id service.TaskSpaceHistoryIdentity, args service.TaskSpaceHistorySaveArgs) error {
	return s.withTaskSpaceHistory(ctx, id, true, func(tx *sql.Tx, src taskSpaceHistorySource) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO manager_task_space_summaries(turn_id,progress_text,progress_updated_at,result_text,result_updated_at)
 VALUES($1,NULLIF($2,''),CASE WHEN $2<>'' THEN now() END,NULLIF($3,''),CASE WHEN $3<>'' THEN now() END)
 ON CONFLICT(turn_id) DO UPDATE SET
 progress_text=COALESCE(EXCLUDED.progress_text,manager_task_space_summaries.progress_text),
 progress_updated_at=COALESCE(EXCLUDED.progress_updated_at,manager_task_space_summaries.progress_updated_at),
 result_text=COALESCE(EXCLUDED.result_text,manager_task_space_summaries.result_text),
 result_updated_at=COALESCE(EXCLUDED.result_updated_at,manager_task_space_summaries.result_updated_at)`, src.turn, args.Progress, args.Result)
		return err
	})
}
