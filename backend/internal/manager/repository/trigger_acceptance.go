package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	platform "github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"slices"
	"time"
)

func (s *Store) AcceptTrigger(ctx context.Context, q service.TriggerAcceptance) (service.TriggerReceipt, error) {
	var out service.TriggerReceipt
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	b, err := lockTriggerAcceptance(ctx, tx, q.TriggerID)
	if err != nil {
		return out, err
	}
	out, err = s.acceptTriggerTx(ctx, tx, b, q)
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}
func lockTriggerAcceptance(ctx context.Context, tx *sql.Tx, id string) (service.Trigger, error) {
	var agent string
	var b service.Trigger
	if !service.ValidID(id) {
		return b, service.Fail("invalid_argument", 400, "触发器无效")
	}
	if err := tx.QueryRowContext(ctx, `SELECT a.id FROM platform_agents a JOIN manager_triggers t ON t.agent_id=a.id WHERE t.id=$1 FOR SHARE OF a`, id).Scan(&agent); err != nil {
		return b, visibleError(err)
	}
	return scanTrigger(tx.QueryRowContext(ctx, `SELECT `+triggerColumns+` FROM manager_triggers WHERE id=$1 FOR UPDATE`, id))
}
func (s *Store) acceptTriggerTx(ctx context.Context, tx *sql.Tx, b service.Trigger, q service.TriggerAcceptance) (service.TriggerReceipt, error) {
	var out service.TriggerReceipt
	if err := service.ValidateTriggerCommand(q.Command); err != nil {
		return out, err
	}
	if q.TriggerID != b.ID || q.BindingVersion != b.Version || !b.Enabled {
		return out, service.Fail("trigger_binding_changed", 403, "触发器已停用或配置已变化")
	}
	if q.Command.EventID != q.EventID || len(q.BodyHash) != 64 || q.Command.EntityID != "" && q.Command.EntityID != b.AgentID || !slices.Contains(b.AllowedActions, q.Command.Action) {
		return out, service.Fail("trigger_target_forbidden", 403, "触发目标或动作不可用")
	}
	var enabled bool
	if err := tx.QueryRowContext(ctx, `SELECT enabled FROM platform_agents WHERE workspace_id=$1 AND id=$2`, b.WorkspaceID, b.AgentID).Scan(&enabled); err != nil {
		return out, err
	}
	if !enabled {
		return out, service.Fail("agent_disabled", 409, "员工已停用")
	}
	var prior string
	var receipt []byte
	err := tx.QueryRowContext(ctx, `SELECT body_hash,receipt FROM manager_trigger_events WHERE trigger_id=$1 AND event_id=$2`, b.ID, q.EventID).Scan(&prior, &receipt)
	if err == nil {
		if prior != q.BodyHash {
			return out, service.Fail("trigger_event_conflict", 409, "事件编号已用于不同内容")
		}
		if err = json.Unmarshal(receipt, &out); err != nil {
			return out, err
		}
		out.Duplicate = true
		return out, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	eid := service.NewID()
	payload, err := json.Marshal(q.Command)
	if err != nil {
		return out, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO manager_trigger_events(id,trigger_id,workspace_id,agent_id,binding_version,event_id,body_hash,action,payload,occurred_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, eid, b.ID, b.WorkspaceID, b.AgentID, b.Version, q.EventID, q.BodyHash, q.Command.Action, payload, q.OccurredAt); err != nil {
		return out, err
	}
	ctx = platform.WithTriggerExecution(ctx, platform.TriggerExecution{TriggerID: b.ID, Version: b.Version, EventID: eid})
	if q.Command.Action == "create_task" {
		out, err = s.createTriggerTask(ctx, tx, b, q, eid)
	} else {
		out, err = s.updateTriggerTask(ctx, tx, b, q, eid)
	}
	if err != nil {
		return out, err
	}
	out.EventID = eid
	out.Status = "accepted"
	raw, err := json.Marshal(out)
	if err != nil {
		return out, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE manager_trigger_events SET status='accepted',task_id=NULLIF($2,'')::uuid,session_id=NULLIF($3,'')::uuid,input_id=NULLIF($4,'')::uuid,receipt=$5 WHERE id=$1`, eid, out.TaskID, out.SessionID, out.InputID, raw)
	return out, err
}
func (s *Store) createTriggerTask(ctx context.Context, tx *sql.Tx, b service.Trigger, q service.TriggerAcceptance, eid string) (service.TriggerReceipt, error) {
	out := service.TriggerReceipt{TaskSpaceID: service.NewID(), TaskID: service.NewID(), SessionID: service.NewID()}
	// The key only selects a workspace-local space. It does not grant its human owner access to trigger tasks.
	_, err := tx.ExecContext(ctx, `INSERT INTO manager_task_spaces(id,workspace_id,business_key,created_by) VALUES($1,$2,$3,$4) ON CONFLICT(workspace_id,business_key) DO NOTHING`, out.TaskSpaceID, b.WorkspaceID, q.Command.BusinessKey, b.CreatedBy)
	if err != nil {
		return out, err
	}
	if q.Command.BusinessKey != nil {
		if err = tx.QueryRowContext(ctx, `SELECT id FROM manager_task_spaces WHERE workspace_id=$1 AND business_key=$2 AND lifecycle='active' AND NOT EXISTS(SELECT 1 FROM manager_work_items w WHERE w.task_space_id=manager_task_spaces.id) FOR UPDATE`, b.WorkspaceID, *q.Command.BusinessKey).Scan(&out.TaskSpaceID); err != nil {
			return out, visibleError(err)
		}
	}
	t := &sessionTx{ctx: ctx, tx: tx, store: s, target: service.Target{Workspace: b.WorkspaceID, TaskSpaceID: out.TaskSpaceID, AgentID: b.AgentID}}
	t.state.AgentID = b.AgentID
	t.state.TaskSpaceID = &out.TaskSpaceID
	t.state.OwnerLifecycle = "active"
	if err = tx.QueryRowContext(ctx, `SELECT enabled,admission_epoch FROM platform_agents WHERE id=$1`, b.AgentID).Scan(&t.state.AgentEnabled, &t.state.AdmissionEpoch); err != nil {
		return out, err
	}
	cfg, err := t.Configuration("task")
	if err != nil {
		return out, err
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return out, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO manager_tasks(id,workspace_id,task_space_id,agent_id,trigger_id,trigger_version) VALUES($1,$2,$3,$4,$5,$6)`, out.TaskID, b.WorkspaceID, out.TaskSpaceID, b.AgentID, b.ID, b.Version); err != nil {
		return out, err
	}
	if err = SnapshotTriggerNotificationsTx(ctx, tx, b.ID, out.TaskID, b.Version, b.NotificationTargets); err != nil {
		return out, err
	}
	title := b.Name + " · " + q.OccurredAt.UTC().Format("2006-01-02 15:04:05 UTC")
	if _, err = tx.ExecContext(ctx, `INSERT INTO manager_sessions(id,workspace_id,agent_id,task_id,task_space_id,trigger_id,trigger_version,mode,title,configuration) VALUES($1,$2,$3,$4,$5,$6,$7,'task',$8,$9)`, out.SessionID, b.WorkspaceID, b.AgentID, out.TaskID, out.TaskSpaceID, b.ID, b.Version, title, raw); err != nil {
		return out, err
	}
	t.target.Session = out.SessionID
	if err = t.refresh(); err != nil {
		return out, err
	}
	text := q.Command.Description
	if p := q.Native; p != nil {
		if _, err = tx.ExecContext(ctx, `INSERT INTO manager_webhook_payloads(event_id,source,event_name,content_type,body,as_file) VALUES($1,$2,$3,$4,$5,$6)`, eid, p.Source, p.Name, p.ContentType, p.Body, p.AsFile()); err != nil {
			return out, err
		}
		text, err = p.Input(text, eid, q.OccurredAt)
		if err != nil {
			return out, err
		}
	}
	out.InputID, err = appendTriggerInput(t, eid, text)
	if err != nil {
		return out, err
	}
	if err = t.SetInitialInput(out.InputID); err != nil {
		return out, err
	}
	if err = t.Event("session.created", nil, nil, map[string]any{"session_id": out.SessionID, "task_id": out.TaskID, "trigger_id": b.ID}); err != nil {
		return out, err
	}
	return out, t.SnapshotEvent()
}
func (s *Store) updateTriggerTask(ctx context.Context, tx *sql.Tx, b service.Trigger, q service.TriggerAcceptance, eid string) (service.TriggerReceipt, error) {
	out := service.TriggerReceipt{TaskID: q.Command.TaskID}
	var space string
	err := tx.QueryRowContext(ctx, `SELECT task_space_id FROM manager_tasks WHERE id=$1 AND workspace_id=$2 AND agent_id=$3 AND trigger_id=$4 AND trigger_version=$5`, out.TaskID, b.WorkspaceID, b.AgentID, b.ID, b.Version).Scan(&space)
	if err != nil {
		return out, service.Fail("trigger_task_forbidden", 403, "任务不属于当前触发器")
	}
	var lifecycle string
	if err = tx.QueryRowContext(ctx, `SELECT lifecycle FROM manager_task_spaces WHERE id=$1 FOR UPDATE`, space).Scan(&lifecycle); err != nil {
		return out, err
	}
	if lifecycle != "active" {
		return out, service.Fail("task_space_archived", 409, "空间已归档")
	}
	if err = tx.QueryRowContext(ctx, `SELECT id FROM manager_sessions WHERE task_id=$1 AND trigger_id=$2 AND trigger_version=$3 AND lifecycle='active' FOR UPDATE`, out.TaskID, b.ID, b.Version).Scan(&out.SessionID); err != nil {
		return out, visibleError(err)
	}
	out.TaskSpaceID = space
	t := &sessionTx{ctx: ctx, tx: tx, store: s, target: service.Target{Workspace: b.WorkspaceID, TaskSpaceID: space, AgentID: b.AgentID, Session: out.SessionID}}
	if err = t.refresh(); err != nil {
		return out, err
	}
	if q.Command.Action == "supplement_task" {
		out.InputID, err = appendTriggerInput(t, eid, q.Command.Description)
	} else {
		err = cancelTriggerSession(t, "trigger_canceled")
	}
	if err != nil {
		return out, err
	}
	return out, t.SnapshotEvent()
}
func appendTriggerInput(t *sessionTx, eid, text string) (string, error) {
	if t.state.Session.Execution.QueuedInputCount >= 100 {
		return "", service.Fail("session_queue_full", 429, "待执行输入达到上限")
	}
	id := service.NewID()
	var seq int64
	if err := t.tx.QueryRowContext(t.ctx, `UPDATE manager_sessions SET last_input_seq=last_input_seq+1 WHERE id=$1 RETURNING last_input_seq`, t.target.Session).Scan(&seq); err != nil {
		return "", err
	}
	_, err := t.tx.ExecContext(t.ctx, `INSERT INTO manager_inputs(id,workspace_id,session_id,trigger_event_id,admission_epoch,seq,text,delivery_mode,source_kind,trigger_agent_loop) VALUES($1,$2,$3,$4,$5,$6,$7,'queue','system',true)`, id, t.target.Workspace, t.target.Session, eid, t.state.AdmissionEpoch, seq, "[系统消息]"+text)
	if err != nil {
		return "", err
	}
	v := service.Input{ID: id, SessionID: t.target.Session, PrincipalType: "trigger", Text: "[系统消息]" + text, Seq: seq, Status: "queued", DeliveryMode: "queue", SourceKind: "system", CreatedAt: time.Now().UTC()}
	if err = t.Event("user.message", &id, nil, service.InputFields(v)); err != nil {
		return "", err
	}
	return id, t.Event("input.updated", &id, nil, service.InputFields(v))
}
func cancelTriggerSession(t *sessionTx, reason string) error {
	items, err := t.CancelQueued(reason)
	if err != nil {
		return err
	}
	for _, i := range items {
		if err = t.Event("input.updated", &i.ID, nil, service.InputFields(i)); err != nil {
			return err
		}
	}
	return t.Stop(false)
}
func (s *Store) stopTriggerSessions(ctx context.Context, tx *sql.Tx, b service.Trigger) error {
	rows, err := tx.QueryContext(ctx, `SELECT id,task_space_id FROM manager_sessions WHERE trigger_id=$1 AND lifecycle='active' ORDER BY task_space_id,id`, b.ID)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Explicit close below.
	type target struct{ id, space string }
	var targets []target
	for rows.Next() {
		var v target
		if err = rows.Scan(&v.id, &v.space); err != nil {
			return errors.Join(err, rows.Close())
		}
		targets = append(targets, v)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, v := range targets {
		var ignored string
		if err = tx.QueryRowContext(ctx, `SELECT id FROM manager_task_spaces WHERE id=$1 FOR UPDATE`, v.space).Scan(&ignored); err != nil {
			return err
		}
		if err = tx.QueryRowContext(ctx, `SELECT id FROM manager_sessions WHERE id=$1 FOR UPDATE`, v.id).Scan(&ignored); err != nil {
			return err
		}
		t := &sessionTx{ctx: ctx, tx: tx, store: s, target: service.Target{Workspace: b.WorkspaceID, TaskSpaceID: v.space, AgentID: b.AgentID, Session: v.id}}
		if err = t.refresh(); err != nil {
			return err
		}
		if err = cancelTriggerSession(t, "trigger_binding_changed"); err != nil {
			return err
		}
		if err = t.SnapshotEvent(); err != nil {
			return err
		}
	}
	return nil
}
