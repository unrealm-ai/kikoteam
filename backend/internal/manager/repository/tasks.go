package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

// Keep execution filtering identical to scanSession, including unknown local
// activity and outstanding stop acknowledgements.
const taskExecutionSQL = `CASE WHEN ` + publicBlockReason + ` IS NOT NULL OR s.background_process_count IS NULL THEN 'blocked' WHEN s.stop_revision>s.applied_stop_revision THEN 'stopping' WHEN s.active_turn_id IS NOT NULL THEN 'running' WHEN EXISTS(SELECT 1 FROM manager_inputs q WHERE q.session_id=s.id AND q.status='queued') THEN 'queued' ELSE 'idle' END`
const taskFrom = sessionFrom + ` JOIN manager_tasks k ON k.id=s.task_id
 LEFT JOIN LATERAL (SELECT to_jsonb(i)-'text' AS value FROM manager_inputs i WHERE i.session_id=s.id ORDER BY seq DESC LIMIT 1) li ON true
 LEFT JOIN LATERAL (SELECT (to_jsonb(t)-'result_summary'-'error'-'usage')||jsonb_build_object('turn_no',t.turn_no::text) AS value FROM manager_turns t WHERE t.session_id=s.id ORDER BY turn_no DESC LIMIT 1) lt ON true `
const taskColumns = publicSessionColumns + `,k.id,k.created_at,a.name,a.avatar,k.source_chat_session_id,k.source_chat_input_id,li.value,lt.value`

// scanSession remains the single implementation of execution/admission reads.
type appendedScanner struct {
	scanner
	extra []any
}

func (r appendedScanner) Scan(dest ...any) error { return r.scanner.Scan(append(dest, r.extra...)...) }
func scanTask(row scanner) (service.Task, error) {
	var out service.Task
	var input, turn []byte
	r := appendedScanner{scanner: row, extra: []any{&out.TaskID, &out.CreatedAt, &out.Agent.Name, &out.Agent.Avatar, &out.Source.ChatSessionID, &out.Source.ChatInputID, &input, &turn, &out.WorkItemID}}
	st, err := scanSession(r)
	if err != nil {
		return out, err
	}
	v := st.Session
	out.PlanningState = v.PlanningState
	out.PlanningVersion = v.PlanningVersion
	out.SessionID = v.ID
	out.ConversationID = v.ConversationID
	out.TaskSpaceID = v.TaskSpaceID
	out.Title = v.Title
	out.Agent.ID = v.AgentID
	out.Lifecycle = v.Lifecycle
	out.Execution = v.Execution
	out.Revision = v.Revision
	out.AcceptsInput = v.AcceptsInput
	out.InputBlockReason = v.InputBlockReason
	out.Source.Kind = "unspecified"
	if out.Source.ChatSessionID != nil {
		out.Source.Kind = "chat_delegation"
	}
	if len(input) > 0 {
		if err = json.Unmarshal(input, &out.LatestInput); err != nil {
			return out, err
		}
	}
	if len(turn) > 0 {
		if err = json.Unmarshal(turn, &out.LatestTurn); err != nil {
			return out, err
		}
	}
	mutable := v.Lifecycle == "active"
	archive := mutable && v.Execution.ActiveTurnID == nil && v.Execution.QueuedInputCount == 0 && v.Execution.BackgroundProcessCount != nil && *v.Execution.BackgroundProcessCount == 0 && (v.Execution.BlockReason == nil || *v.Execution.BlockReason == "remote_tool_outcome_unknown") && st.StopRevision == st.AppliedStopRevision
	archiveReason := "session_busy"
	if !mutable {
		archiveReason = "session_archived"
	}
	rename := mutable
	renameReason := "session_archived"
	if v.TaskSpaceID != nil && !v.AcceptsInput {
		rename = false
		renameReason = "task_space_access_revoked"
	}
	stopReason := "session_archived"
	canStop := mutable && v.PlanningState == "started"
	if v.PlanningState != "started" {
		stopReason = "task_not_started"
	}
	if mutable && service.TaskDetailWaiting(st) {
		canStop, archive = false, false
		stopReason, archiveReason = "task_waiting_for_action", "task_waiting_for_action"
	}
	out.Actions = map[string]service.TaskAction{"rename": taskAction(rename, renameReason), "stop": taskAction(canStop, stopReason), "archive": taskAction(archive, archiveReason)}
	return out, nil
}
func taskAction(allowed bool, reason string) service.TaskAction {
	v := service.TaskAction{Allowed: allowed}
	if !allowed {
		v.Reason = &reason
	}
	return v
}

// A task can participate in several handoffs. Navigation exposes only items whose root is readable.
const relatedWork = ` FROM manager_work_items w WHERE w.workspace_id=s.workspace_id
 AND (w.root_session_id=s.id OR EXISTS(SELECT 1 FROM manager_work_assignments wa WHERE wa.work_item_id=w.id AND wa.session_id=s.id))
 AND ` + boardVisible
const taskReadColumns = taskColumns + `,(SELECT w.id` + relatedWork + ` ORDER BY w.created_at DESC,w.id DESC LIMIT 1)`

func taskSelection(ctx context.Context, actor string) string {
	return taskFrom + ` WHERE s.workspace_id=$1 AND ` + sessionRead(ctx, actor) + `
 AND ($3='' OR position(lower($3) in lower(COALESCE(s.title,'')))>0 OR k.id::text=lower($3))
 AND ($4='' OR s.agent_id::text=$4) AND ($5='' OR s.conversation_id::text=$5)
 AND ($6='' OR lt.value->>'status'=$6) AND ($7='' OR s.lifecycle=$7) AND ($8='' OR s.task_space_id::text=$8)
 AND ($9='' OR EXISTS(SELECT 1` + relatedWork + ` AND w.id=NULLIF($9,'')::uuid))`
}
func taskArgs(w, actor string, f service.TaskFilter) []any {
	return []any{w, actor, f.Q, f.Agent, f.Conversation, f.LatestTurnStatus, f.Lifecycle, f.TaskSpace, f.WorkItem}
}
func (s *Store) TaskCounts(ctx context.Context, w, actor string, f service.TaskFilter) (map[string]int, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+taskExecutionSQL+`, count(*)`+taskSelection(ctx, actor)+` GROUP BY 1`, taskArgs(w, actor, f)...)
	return readStatusCounts(rows, err)
}
func readStatusCounts(rows *sql.Rows, err error) (map[string]int, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // Iteration reports errors.
	counts := map[string]int{}
	for rows.Next() {
		var state string
		var n int
		if err := rows.Scan(&state, &n); err != nil {
			return nil, err
		}
		counts[state] = n
	}
	return counts, rows.Err()
}
func (s *Store) Tasks(ctx context.Context, w, actor string, f service.TaskFilter) ([]service.Task, error) {
	args := append(taskArgs(w, actor, f), f.ExecutionState, f.BeforeID, f.BeforeTime, f.Limit)
	rows, err := s.DB.QueryContext(ctx, `SELECT `+taskReadColumns+taskSelection(ctx, actor)+`
 AND ($10='' OR (`+taskExecutionSQL+`)=$10) AND ($11='' OR (k.created_at,k.id)<($12::timestamptz,NULLIF($11,'')::uuid))
 ORDER BY k.created_at DESC,k.id DESC LIMIT $13`, args...)
	if err != nil {
		return nil, err
	}
	return scanTasks(rows)
}

func scanTasks(rows *sql.Rows) ([]service.Task, error) {
	defer func() { _ = rows.Close() }() //nolint:errcheck // Iteration errors checked below.
	items := []service.Task{}
	for rows.Next() {
		v, e := scanTask(rows)
		if e != nil {
			return nil, e
		}
		items = append(items, v)
	}
	return items, rows.Err()
}

func (s *Store) Task(ctx context.Context, w, actor, id string) (service.TaskDetail, error) {
	var out service.TaskDetail
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	var workSpace, preparing, stopped bool
	row := tx.QueryRowContext(ctx, `SELECT `+taskReadColumns+`,EXISTS(SELECT 1 FROM manager_work_items wi WHERE wi.task_space_id=s.task_space_id),
 `+taskPreparingEnvironment+`,COALESCE(`+taskSettledStop+`,false)`+taskFrom+` WHERE s.workspace_id=$1 AND `+sessionRead(ctx, actor)+` AND k.id=$3`, w, actor, id)
	v, err := scanTask(appendedScanner{scanner: row, extra: []any{&workSpace, &preparing, &stopped}})
	if err != nil {
		return out, err
	}
	if out.Progress, err = authenticatedTaskProgress(ctx, tx, w, actor, v, preparing, stopped); err != nil {
		return out, err
	}
	artifacts, err := taskDetailArtifactRows(ctx, tx, service.TaskDetailGrant{SessionID: v.SessionID}, "")
	if err != nil {
		return out, err
	}
	out.Artifacts = make([]service.TaskDetailArtifact, 0, len(artifacts))
	for _, a := range artifacts {
		out.Artifacts = append(out.Artifacts, service.TaskDetailArtifact{ID: a.ID, Name: a.Name, Size: a.Size})
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	out.Task = v
	out.Actions["supplement"] = service.TaskAction{Allowed: v.AcceptsInput, Reason: v.InputBlockReason}
	if v.AcceptsInput && workSpace {
		out.Actions["supplement"] = taskAction(false, "system_managed_input")
	}
	// All subsequent reads recheck the same Session visibility.
	session, err := s.Session(ctx, w, actor, v.SessionID)
	if err != nil {
		return out, err
	}
	if session.InitialInputID != nil {
		i, e := s.Input(ctx, w, actor, v.SessionID, *session.InitialInputID)
		if e != nil {
			return out, e
		}
		out.InitialInput = &i
	}
	if v.LatestTurn != nil {
		t, e := s.Turn(ctx, w, actor, v.SessionID, v.LatestTurn.ID)
		if e != nil {
			return out, e
		}
		text := ""
		if t.ResultSummary != nil {
			text = *t.ResultSummary
		}
		out.LatestResult = taskResultPreview(t.ID, text)
	}
	return out, nil
}

func taskResultPreview(turn, text string) *service.TaskResultPreview {
	runes := []rune(text)
	truncated := len(runes) > 1600
	if truncated {
		text = string(runes[:1600])
	}
	return &service.TaskResultPreview{TurnID: turn, Text: text, Truncated: truncated}
}

func (s *Store) TaskReports(ctx context.Context, w, actor, id string, f service.ReportFilter) ([]service.TaskReportEntry, error) {
	// An empty list must still distinguish an inaccessible Task from no reports.
	var session string
	err := s.DB.QueryRowContext(ctx, `SELECT s.id`+sessionFrom+` JOIN manager_tasks k ON k.id=s.task_id WHERE s.workspace_id=$1 AND `+sessionRead(ctx, actor)+` AND k.id=$3`, w, actor, id).Scan(&session)
	if err != nil {
		return nil, visibleError(err)
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT r.id,i.id,i.seq,t.id,CASE WHEN t.error->>'code'='awaiting_confirmation' THEN 'awaiting_confirmation' ELSE COALESCE(t.status,i.status) END,
 CASE WHEN r.id IS NOT NULL THEN r.status WHEN k.source_chat_session_id IS NULL THEN 'not_applicable' WHEN NOT i.report_enabled THEN 'not_enabled' ELSE 'pending' END,
 r.reason,
 CASE WHEN target.id IS NOT NULL AND EXISTS(SELECT 1 `+sessionFrom+` WHERE s.id=r.target_session_id AND s.workspace_id=$1 AND `+sessionRead(ctx, actor)+`) THEN jsonb_build_object('session_id',r.target_session_id,'input_id',target.id,'input_status',target.status,'context_state',target.context_state,'turn_id',target.turn_id,'turn_status',target_turn.status) ELSE NULL END,
 r.created_at, `+turnSupplementJSON+`
 FROM manager_inputs i JOIN manager_sessions origin ON origin.id=i.session_id JOIN manager_tasks k ON k.id=origin.task_id
 LEFT JOIN manager_turns t ON t.input_id=i.id LEFT JOIN manager_task_reports r ON r.source_input_id=i.id
 LEFT JOIN manager_inputs target ON target.id=r.target_input_id LEFT JOIN manager_turns target_turn ON target_turn.id=target.turn_id
 WHERE i.session_id=$3 AND i.workspace_id=$1
 AND EXISTS(SELECT 1 `+sessionFrom+` WHERE s.id=i.session_id AND s.workspace_id=i.workspace_id AND `+sessionRead(ctx, actor)+`)
 AND ((t.status IN ('completed','failed','interrupted','canceled')) OR (i.turn_id IS NULL AND i.status IN ('canceled','rejected')))
 AND ($4='' OR t.id::text=$4) AND ($5='' OR i.id::text=$5) AND ($6::bigint=0 OR i.seq<$6)
 ORDER BY i.seq DESC LIMIT $7`, w, actor, session, f.Turn, f.Input, f.Before, f.Limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Iteration errors checked below.
	items := []service.TaskReportEntry{}
	for rows.Next() {
		var v service.TaskReportEntry
		var target, supplements []byte
		var created *time.Time
		if err = rows.Scan(&v.ReportID, &v.SourceInputID, &v.SourceInputSeq, &v.SourceTurnID, &v.Outcome, &v.Status, &v.Reason, &target, &created, &supplements); err != nil {
			return nil, err
		}
		v.TurnSupplements, err = parseTurnSupplements(supplements)
		if err != nil {
			return nil, err
		}
		v.Target = target
		v.CreatedAt = created
		items = append(items, v)
	}
	return items, rows.Err()
}

func (s *Store) TaskArtifactDownload(ctx context.Context, w, actor, taskID, artifactID string) (service.Artifact, []byte, error) {
	readSession := func() (string, error) {
		var sessionID string
		err := s.DB.QueryRowContext(ctx, `SELECT s.id`+sessionFrom+` JOIN manager_tasks k ON k.id=s.task_id WHERE s.workspace_id=$1 AND `+sessionRead(ctx, actor)+` AND k.id=$3`, w, actor, taskID).Scan(&sessionID)
		if err != nil {
			return "", visibleError(err)
		}
		return sessionID, nil
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return service.Artifact{}, nil, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Preserve the operational error.
	var sessionID string
	err = tx.QueryRowContext(ctx, `SELECT s.id`+sessionFrom+` JOIN manager_tasks k ON k.id=s.task_id WHERE s.workspace_id=$1 AND `+sessionRead(ctx, actor)+` AND k.id=$3`, w, actor, taskID).Scan(&sessionID)
	if err != nil {
		return service.Artifact{}, nil, visibleError(err)
	}
	rows, err := taskDetailArtifactRows(ctx, tx, service.TaskDetailGrant{SessionID: sessionID}, artifactID)
	if err != nil || len(rows) != 1 {
		if err == nil {
			err = service.Fail("resource_not_found", 404, "产物不存在")
		}
		return service.Artifact{}, nil, err
	}
	if err = tx.Commit(); err != nil {
		return service.Artifact{}, nil, err
	}
	if s.WorkAssets == nil {
		return service.Artifact{}, nil, service.Fail("artifacts_unavailable", 503, "产物暂不可读")
	}
	a := rows[0]
	data, err := s.WorkAssets.GetWorkFile(ctx, w, a.Version, a.Size)
	if err != nil {
		return service.Artifact{}, nil, err
	}
	if _, err = readSession(); err != nil {
		return service.Artifact{}, nil, err
	}
	return a, data, nil
}
