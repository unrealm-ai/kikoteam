package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

const chatScheduleColumns = "id,status,instruction,title,rule,next_due"

func scanChatSchedule(row interface{ Scan(...any) error }) (service.ChatSchedule, error) {
	var v service.ChatSchedule
	var raw []byte
	err := row.Scan(&v.ID, &v.Status, &v.Instruction, &v.Title, &raw, &v.NextDue)
	if err == nil {
		err = json.Unmarshal(raw, &v.Rule)
	}
	return v, visibleError(err)
}

func chatScheduleSource(scope service.DelegationScope) service.Target {
	return service.Target{Workspace: scope.WorkspaceID, Actor: scope.ActorID, Conversation: scope.ConversationID, Source: &scope.Identity, SourceInputID: scope.InputID}
}

func (s *Store) MutateChatSchedule(ctx context.Context, scope service.DelegationScope, q service.ChatScheduleMutation) (service.ChatSchedule, error) {
	var out service.ChatSchedule
	err := s.WithSession(ctx, chatScheduleSource(scope), func(base service.SessionTx) error {
		t := base.(*sessionTx)
		var id string
		err := t.tx.QueryRowContext(ctx, `SELECT schedule_id FROM manager_chat_schedule_operations WHERE source_session_id=$1 AND actor_id=$2 AND request_hash=$3`, scope.Identity.SessionID, scope.ActorID, q.Key).Scan(&id)
		if err == nil {
			out, err = readChatSchedule(ctx, t.tx, scope, id)
			return err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var now time.Time
		if err := t.tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
			return err
		}
		if q.Action == "create_scheduled_task" {
			id, err = s.insertChatSchedule(ctx, t, scope, q, now)
		} else {
			id = q.ID
			err = updateChatSchedule(ctx, t.tx, scope, q, now)
		}
		if err != nil {
			return err
		}
		if _, err = t.tx.ExecContext(ctx, `INSERT INTO manager_chat_schedule_operations(schedule_id,source_session_id,actor_id,request_hash) VALUES($1,$2,$3,$4)`, id, scope.Identity.SessionID, scope.ActorID, q.Key); err != nil {
			return err
		}
		out, err = readChatSchedule(ctx, t.tx, scope, id)
		return err
	})
	return out, err
}

func readChatSchedule(ctx context.Context, tx *sql.Tx, scope service.DelegationScope, id string) (service.ChatSchedule, error) {
	return scanChatSchedule(tx.QueryRowContext(ctx, `SELECT `+chatScheduleColumns+` FROM manager_chat_schedules WHERE id=$1 AND workspace_id=$2 AND source_session_id=$3 AND actor_id=$4`, id, scope.WorkspaceID, scope.Identity.SessionID, scope.ActorID))
}

func (s *Store) insertChatSchedule(ctx context.Context, t *sessionTx, scope service.DelegationScope, q service.ChatScheduleMutation, now time.Time) (string, error) {
	requested := *q.Rule
	if requested.Timezone == "" {
		if err := t.tx.QueryRowContext(ctx, `SELECT timezone FROM platform_timezone_settings WHERE singleton`).Scan(&requested.Timezone); err != nil {
			return "", err
		}
	}
	rule, due, err := normalizedChatSchedule(&requested, now)
	if err != nil {
		return "", err
	}
	if err = validateChatScheduleFiles(ctx, t, q); err != nil {
		return "", err
	}
	ids, err := json.Marshal(q.AttachmentIDs)
	if err != nil {
		return "", err
	}
	paths, err := json.Marshal(q.AttachmentPaths)
	if err != nil {
		return "", err
	}
	id := service.NewID()
	_, err = t.tx.ExecContext(ctx, `INSERT INTO manager_chat_schedules(id,workspace_id,source_session_id,source_input_id,actor_id,title,instruction,rule,attachment_ids,attachment_paths,status,next_due) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'active',$11)`, id, scope.WorkspaceID, scope.Identity.SessionID, scope.InputID, scope.ActorID, q.Title, q.Instruction, rule, ids, paths, due)
	return id, err
}

func validateChatScheduleFiles(ctx context.Context, t *sessionTx, q service.ChatScheduleMutation) error {
	var hasFiles bool
	if err := t.tx.QueryRowContext(ctx, `SELECT attachments <> '[]'::jsonb FROM manager_inputs WHERE id=$1`, t.target.SourceInputID).Scan(&hasFiles); err != nil {
		return err
	}
	if hasFiles && len(q.AttachmentIDs)+len(q.AttachmentPaths) == 0 && !q.SourceFilesUnused {
		return service.Fail("attachment_selection_required", 409, "原消息包含附件，请选择本计划所需的文件，或明确本计划无需附件")
	}
	if _, err := t.ResolveTaskAttachmentIDs(q.AttachmentIDs); err != nil {
		return err
	}
	files, err := q.ValidatePaths(ctx, t.state.Conversation, t.target.Actor, q.AttachmentPaths)
	if err != nil {
		return err
	}
	return t.checkAttachmentSource(files)
}

func normalizedChatSchedule(rule *service.ChatScheduleRule, now time.Time) ([]byte, time.Time, error) {
	r, err := rule.Normalize(now)
	if err != nil {
		return nil, time.Time{}, err
	}
	due, err := r.Next(now)
	if err != nil {
		return nil, time.Time{}, err
	}
	raw, err := json.Marshal(r)
	return raw, due, err
}

func updateChatSchedule(ctx context.Context, tx *sql.Tx, scope service.DelegationScope, q service.ChatScheduleMutation, now time.Time) error {
	v, err := scanChatSchedule(tx.QueryRowContext(ctx, `SELECT `+chatScheduleColumns+` FROM manager_chat_schedules WHERE id=$1 AND workspace_id=$2 AND source_session_id=$3 AND actor_id=$4 FOR UPDATE`, q.ID, scope.WorkspaceID, scope.Identity.SessionID, scope.ActorID))
	if err != nil {
		return err
	}
	if v.Status != "active" && v.Status != "paused" {
		return service.Fail("chat_schedule_inactive", 409, "定时计划已取消或结束")
	}
	if q.Action == "cancel_scheduled_task" {
		_, err = tx.ExecContext(ctx, `UPDATE manager_chat_schedules SET status='canceled',next_due=NULL,version=version+1 WHERE id=$1`, q.ID)
		return err
	}
	raw, next, status, err := chatScheduleChange(v.Status, v.Rule, v.NextDue, q.Rule, q.Enabled, now)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE manager_chat_schedules SET instruction=COALESCE(NULLIF($2,''),instruction),title=COALESCE($3,title),rule=$4,next_due=$5,status=$6,version=version+1 WHERE id=$1`, q.ID, q.Instruction, q.Title, raw, next, status)
	return err
}

func (s *Store) ListChatSchedules(ctx context.Context, scope service.DelegationScope, id string) ([]service.ChatSchedule, error) {
	out := []service.ChatSchedule{}
	err := s.WithSession(ctx, chatScheduleSource(scope), func(base service.SessionTx) error {
		t := base.(*sessionTx)
		if id != "" {
			v, err := readChatSchedule(ctx, t.tx, scope, id)
			if err != nil {
				return err
			}
			v.Runs, err = listChatScheduleRuns(ctx, t.tx, id)
			out = []service.ChatSchedule{v}
			return err
		}
		rows, err := t.tx.QueryContext(ctx, `SELECT `+chatScheduleColumns+` FROM manager_chat_schedules WHERE workspace_id=$1 AND source_session_id=$2 AND actor_id=$3 ORDER BY (status='active') DESC,created_at DESC,id DESC LIMIT 50`, scope.WorkspaceID, scope.Identity.SessionID, scope.ActorID)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }() //nolint:errcheck // Read-only cursor cleanup.
		out = out[:0]
		for rows.Next() {
			v, e := scanChatSchedule(rows)
			if e != nil {
				return e
			}
			out = append(out, v)
		}
		return rows.Err()
	})
	return out, err
}

// Report dispatch acceptance separately from the first task input's execution.
const chatScheduleRunsProjection = `SELECT r.due,r.status,r.task_id,r.reason,
 NULLIF(COALESCE(t.status,i.status,''),'') AS execution_status
 FROM manager_chat_schedule_runs r
 LEFT JOIN manager_sessions s ON s.task_id=r.task_id
 LEFT JOIN manager_inputs i ON i.id=s.initial_input_id
 LEFT JOIN manager_turns t ON t.id=i.turn_id
 WHERE r.schedule_id=`

func listChatScheduleRuns(ctx context.Context, tx *sql.Tx, id string) ([]service.ChatScheduleOccurrence, error) {
	rows, err := tx.QueryContext(ctx, chatScheduleRunsProjection+`$1 ORDER BY r.due DESC LIMIT 20`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Read-only cursor cleanup.
	out := []service.ChatScheduleOccurrence{}
	for rows.Next() {
		var v service.ChatScheduleOccurrence
		var status sql.NullString
		if err = rows.Scan(&v.Due, &v.Status, &v.TaskID, &v.Reason, &status); err != nil {
			return nil, err
		}
		v.ExecutionStatus = status.String
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) PollChatSchedules(ctx context.Context, limit int) ([]service.TriggerScheduleItem, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,version,next_due FROM manager_chat_schedules WHERE status='active' ORDER BY next_due,id LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Read-only cursor cleanup.
	out := []service.TriggerScheduleItem{}
	for rows.Next() {
		v := service.TriggerScheduleItem{Kind: "chat"}
		if err = rows.Scan(&v.ID, &v.Version, &v.NextDue); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) LoadChatScheduleDispatch(ctx context.Context, id string, tick service.TriggerScheduleTick) (*service.ChatScheduleDispatch, error) {
	var p service.ChatScheduleDispatch
	var principal *string
	var generation int64
	var rule, ids, paths []byte
	var group bool
	err := s.DB.QueryRowContext(ctx, `SELECT p.workspace_id,p.actor_id,p.source_session_id,p.source_input_id,c.id,c.agent_id,p.title,p.instruction,p.rule,p.attachment_ids,p.attachment_paths,i.channel_principal_id,COALESCE(i.channel_generation,0),c.web_group_id IS NOT NULL,clock_timestamp()
 FROM manager_chat_schedules p JOIN manager_sessions s ON s.id=p.source_session_id JOIN manager_conversations c ON c.id=s.conversation_id JOIN manager_inputs i ON i.id=p.source_input_id AND i.session_id=s.id
 WHERE p.id=$1 AND p.status='active' AND p.version=$2 AND p.next_due=$3`, id, tick.Version, tick.ExpectedDue).
		Scan(&p.Scope.WorkspaceID, &p.Scope.ActorID, &p.Scope.Identity.SessionID, &p.Scope.InputID, &p.Scope.ConversationID, &p.Scope.AgentID, &p.Title, &p.Instruction, &rule, &ids, &paths, &principal, &generation, &group, &p.Now)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if principal != nil {
		p.Scope.Channel = &service.ChannelActor{ID: *principal, Generation: generation}
	}
	if group {
		p.Scope.WebGroup = &service.WebGroupExecution{ConversationID: p.Scope.ConversationID}
	}
	for _, pair := range []struct {
		raw []byte
		out any
	}{{rule, &p.Rule}, {ids, &p.AttachmentIDs}, {paths, &p.AttachmentPaths}} {
		if err = json.Unmarshal(pair.raw, pair.out); err != nil {
			return nil, err
		}
	}
	return &p, nil
}

func (s *Store) lockChatScheduleRun(ctx context.Context, tx *sql.Tx, t service.Target) error {
	run := t.ChatSchedule
	var valid bool
	err := tx.QueryRowContext(ctx, `SELECT s.lifecycle='active' AND s.mode='chat' AND s.conversation_id=$8 AND s.workspace_id=$4
 AND i.source_kind='user' AND i.trigger_agent_loop AND i.status NOT IN ('rejected','canceled') AND manager_input_actor_allowed(i.id)
 AND COALESCE(i.submitted_by,i.channel_principal_id)=$5 AND a.admission_epoch=i.admission_epoch AND p.next_due<=clock_timestamp()
 FROM manager_chat_schedules p JOIN manager_sessions s ON s.id=p.source_session_id JOIN manager_inputs i ON i.id=p.source_input_id AND i.session_id=s.id
 JOIN platform_agents a ON a.id=s.agent_id
 WHERE p.id=$1 AND p.version=$2 AND p.next_due=$3 AND p.status='active' AND p.workspace_id=$4 AND p.actor_id=$5 AND p.source_session_id=$6 AND p.source_input_id=$7
 FOR UPDATE OF p FOR SHARE OF s`, run.ID, run.Tick.Version, run.Tick.ExpectedDue, t.Workspace, t.Actor, t.Source.SessionID, t.SourceInputID, t.Conversation).Scan(&valid)
	if errors.Is(err, sql.ErrNoRows) {
		return service.Fail("chat_schedule_stale", 409, "定时计划已变更")
	}
	if err == nil && !valid {
		return service.Fail("access_revoked", 403, "原对话已不可用或原授权已失效")
	}
	return err
}

func (t *sessionTx) FinishChatSchedule(run *service.ChatScheduleRun, task string) error {
	_, err := finishChatSchedule(t.ctx, t.tx, *run, "accepted", "", &task)
	return err
}

func (s *Store) SkipChatSchedule(ctx context.Context, run service.ChatScheduleRun, reason string) (bool, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // The transaction commits explicitly below.
	ok, err := finishChatSchedule(ctx, tx, run, "skipped", reason, nil)
	if err != nil {
		return false, err
	}
	return ok, tx.Commit()
}

func finishChatSchedule(ctx context.Context, tx *sql.Tx, run service.ChatScheduleRun, status, reason string, task *string) (bool, error) {
	var id string
	err := tx.QueryRowContext(ctx, `UPDATE manager_chat_schedules SET next_due=$4,status=CASE WHEN $4::timestamptz IS NULL THEN 'completed' ELSE 'active' END
 WHERE id=$1 AND version=$2 AND next_due=$3 AND status='active' AND next_due<=clock_timestamp() RETURNING id`, run.ID, run.Tick.Version, run.Tick.ExpectedDue, optionalScheduleTime(run.Next)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		if task != nil {
			return false, service.Fail("chat_schedule_stale", 409, "定时计划已变更")
		}
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO manager_chat_schedule_runs(schedule_id,due,task_id,status,reason) VALUES($1,$2,$3,$4,$5)`, id, run.Tick.ExpectedDue, task, status, reason)
	return err == nil, err
}
func optionalScheduleTime(v time.Time) *time.Time {
	if v.IsZero() {
		return nil
	}
	return &v
}
