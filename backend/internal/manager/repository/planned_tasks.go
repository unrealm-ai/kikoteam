package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func (s *Store) PlannedTaskCommand(ctx context.Context, scope service.DelegationScope, q service.PlannedTaskRequest) (service.PlannedTask, error) {
	var out service.PlannedTask
	target := service.Target{Workspace: scope.WorkspaceID, Actor: scope.ActorID, Conversation: scope.ConversationID, Session: q.SessionID, Source: &scope.Identity, DedupLock: "task_plan:" + scope.Identity.SessionID + ":" + q.RequestKey}
	target.SourceInputID = scope.InputID
	err := s.WithSession(ctx, target, func(base service.SessionTx) error {
		out = service.PlannedTask{}
		t := base.(*sessionTx)
		if t.state.OwnerLifecycle != "active" || !t.state.AgentEnabled || q.SessionID != "" && t.state.Session.Lifecycle != "active" {
			return service.Fail("task_unavailable", 409, "待办所属会话不可用")
		}
		if q.SessionID != "" && scope.ActorID != t.state.Session.CreatedBy && (t.state.Session.ChannelCreatedBy == nil || scope.ActorID != *t.state.Session.ChannelCreatedBy) {
			return service.Fail("resource_not_found", 404, "待办不存在")
		}
		var hash, task string
		e := t.tx.QueryRowContext(ctx, `SELECT fingerprint,task_id FROM manager_task_plan_operations WHERE workspace_id=$1 AND source_session_id=$2 AND actor_id=$3 AND request_key=$4`, scope.WorkspaceID, scope.Identity.SessionID, scope.ActorID, q.RequestKey).Scan(&hash, &task)
		if e == nil {
			if hash != q.Fingerprint {
				return service.Fail("idempotency_conflict", 409, "请求键已用于其他操作")
			}
			var err error
			out, err = readPlannedTask(ctx, t.tx, task)
			out.Duplicate = true
			return err
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		files, e := q.ResolveAttachments(t.state.Conversation, scope.ActorID, q.AttachmentPaths)
		if e != nil {
			return e
		}
		if len(q.AttachmentIDs) > 0 {
			var refs []service.Attachment
			refs, e = t.ResolveTaskAttachmentIDs(q.AttachmentIDs)
			if e != nil {
				return e
			}
			files = append(files, refs...)
		}
		if e = t.checkAttachmentSource(files); e != nil {
			return e
		}
		if q.Action == "create_todo" {
			e = t.createPlanned(scope, q, files)
		} else {
			e = t.mutatePlanned(scope, q, files)
		}
		if e != nil {
			return e
		}
		out, e = readPlannedTask(ctx, t.tx, *t.state.Session.TaskID)
		if e != nil {
			return e
		}
		_, e = t.tx.ExecContext(ctx, `INSERT INTO manager_task_plan_operations(workspace_id,source_session_id,actor_id,request_key,fingerprint,task_id,action)VALUES($1,$2,$3,$4,$5,$6,$7)`, scope.WorkspaceID, scope.Identity.SessionID, scope.ActorID, q.RequestKey, q.Fingerprint, out.TaskID, q.Action)
		if e != nil {
			return e
		}
		return t.SnapshotEvent()
	})
	return out, err
}
func readPlannedTask(ctx context.Context, tx *sql.Tx, task string) (service.PlannedTask, error) {
	var v service.PlannedTask
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT k.id,s.id,k.planning_state,k.planning_version,COALESCE(k.planned_instruction,''),s.initial_input_id,s.lifecycle,k.planned_attachments FROM manager_tasks k JOIN manager_sessions s ON s.task_id=k.id WHERE k.id=$1`, task).Scan(&v.TaskID, &v.SessionID, &v.State, &v.Version, &v.Instruction, &v.InitialInputID, &v.Lifecycle, &raw)
	if err == nil {
		err = json.Unmarshal(raw, &v.Attachments)
	}
	v.Application = "not_applicable"
	ready := v.State == "planned" && v.Lifecycle == "active"
	if ready {
		v.Application = "on_start"
	}
	v.AvailableActions = map[string]bool{"start": ready, "cancel_todo": ready, "supplement": ready, "query": true}
	return v, visibleError(err)
}
func (t *sessionTx) createPlanned(scope service.DelegationScope, q service.PlannedTaskRequest, files []service.Attachment) error {
	task := service.NewID()
	if err := t.InsertTask(task, &scope.Identity.SessionID); err != nil {
		return err
	}
	attachments, err := json.Marshal(files)
	if err != nil {
		return err
	}
	if _, err = t.tx.ExecContext(t.ctx, `UPDATE manager_tasks SET planning_state='planned',planned_instruction=$2,planned_attachments=$3 WHERE id=$1`, task, q.Instruction, attachments); err != nil {
		return err
	}
	return t.InsertSession(service.Session{ID: service.NewID(), WorkspaceID: scope.WorkspaceID, AgentID: scope.AgentID, ConversationID: scope.ConversationID, TaskID: &task, CreatedBy: scope.ActorID, Mode: "task", Title: q.Title, Lifecycle: "active", Revision: 1, CreatedAt: time.Now().UTC()})
}
func (t *sessionTx) mutatePlanned(scope service.DelegationScope, q service.PlannedTaskRequest, files []service.Attachment) error {
	var locked string
	if err := t.tx.QueryRowContext(t.ctx, `SELECT id FROM manager_tasks WHERE id=$1 FOR UPDATE`, *t.state.Session.TaskID).Scan(&locked); err != nil {
		return err
	}
	v, err := readPlannedTask(t.ctx, t.tx, *t.state.Session.TaskID)
	if err != nil {
		return err
	}
	if v.Version != q.ExpectedVersion {
		return service.Fail("version_conflict", 409, "待办已变更，请重新查询")
	}
	if v.State == "started" {
		return service.Fail("already_started", 409, "任务已启动；取消待办不会停止执行")
	}
	if v.State != "planned" {
		return service.Fail("task_already_started_or_canceled", 409, "仅未启动待办允许此操作；不会停止已运行任务")
	}
	next, body := "planned", v.Instruction
	switch q.Action {
	case "cancel_todo":
		next = "canceled"
	case "supplement_todo":
		body += "\n\n" + q.Instruction
		seen := make(map[string]bool, len(v.Attachments))
		for _, old := range v.Attachments {
			seen[plannedAttachmentKey(old)] = true
		}
		for _, file := range files {
			if key := plannedAttachmentKey(file); !seen[key] {
				seen[key] = true
				v.Attachments = append(v.Attachments, file)
			}
		}
		if len(v.Attachments) > 32 {
			return service.Fail("invalid_argument", 400, "待办附件最多 32 项")
		}
		if len([]rune(body)) > 65536 {
			return service.Fail("invalid_argument", 400, "待办正文过长")
		}
	case "start_todo":
		next = "started"
	default:
		return service.Fail("invalid_argument", 400, "待办动作无效")
	}
	attachments, err := json.Marshal(v.Attachments)
	if err != nil {
		return err
	}
	if len(v.Attachments) == 0 {
		attachments = []byte("[]")
	}
	if _, err = t.tx.ExecContext(t.ctx, `UPDATE manager_tasks SET planning_state=$2,planned_instruction=$3,planned_attachments=$4,planning_version=planning_version+1 WHERE id=$1`, v.TaskID, next, body, attachments); err != nil {
		return err
	}
	if next != "started" {
		return nil
	}
	cfg, err := t.Configuration("task")
	if err != nil {
		return err
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	if _, err = t.tx.ExecContext(t.ctx, `UPDATE manager_sessions SET configuration=$2 WHERE id=$1`, v.SessionID, raw); err != nil {
		return err
	}
	input, err := t.InsertInput(service.Input{ID: service.NewID(), SessionID: v.SessionID, SubmittedBy: scope.ActorID, Attachments: v.Attachments, Text: body, DeliveryMode: "queue", SourceKind: "user", Status: "queued", CreatedAt: time.Now().UTC()})
	if err != nil {
		return err
	}
	if err = t.SetInitialInput(input.ID); err != nil {
		return err
	}
	if err = t.Event("user.message", &input.ID, nil, service.InputFields(input)); err != nil {
		return err
	}
	return t.Event("input.updated", &input.ID, nil, service.InputFields(input))
}

func (s *Store) PlannedTaskRead(ctx context.Context, scope service.DelegationScope, id string) (service.PlannedTask, error) {
	v, err := s.DelegatedSession(ctx, scope, id)
	if err != nil {
		return service.PlannedTask{}, err
	}
	if v.CreatedBy != scope.ActorID && (v.ChannelCreatedBy == nil || *v.ChannelCreatedBy != scope.ActorID) {
		return service.PlannedTask{}, service.Fail("resource_not_found", 404, "待办不存在")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return service.PlannedTask{}, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Read-only transaction.
	return readPlannedTask(ctx, tx, *v.TaskID)
}

func plannedAttachmentKey(a service.Attachment) string {
	if a.ID != "" {
		return "id:" + a.ID
	}
	return "path:" + a.Path
}
