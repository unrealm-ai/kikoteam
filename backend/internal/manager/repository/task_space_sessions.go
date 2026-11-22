package repository

import (
	"context"
	"database/sql"
	"errors"
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	platform "github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

// Uses the same agent -> owner -> session lock order as Engine admission.
func (s *Store) withTaskSpaceSession(ctx context.Context, target service.Target, fn func(service.SessionTx) error, dispatch bool) error {
	if _, ok := service.ChannelActorFrom(ctx); ok {
		return service.Fail("task_space_forbidden", 403, "渠道主体不可直接访问任务空间")
	}
	if _, ok := service.WebGroupExecutionFrom(ctx); ok {
		return service.Fail("task_space_forbidden", 403, "群执行不可直接访问任务空间")
	}
	if target.Source != nil {
		return service.Fail("task_space_forbidden", 403, "任务空间尚不支持会话委派")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	if !dispatch {
		if err = lockTaskSpaceMember(ctx, tx, target.Workspace, target.Actor); err != nil {
			return err
		}
	}
	if target.DedupLock != "" {
		if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, target.Workspace+":"+target.Actor+":"+target.DedupLock); err != nil {
			return err
		}
	}
	t := &sessionTx{ctx: ctx, tx: tx, target: target, store: s}
	if err = tx.QueryRowContext(ctx, `SELECT enabled,admission_epoch FROM platform_agents WHERE workspace_id=$1 AND id=$2 FOR SHARE`, target.Workspace, target.AgentID).Scan(&t.state.AgentEnabled, &t.state.AdmissionEpoch); err != nil {
		return visibleError(err)
	}

	var triggerID string
	if target.Session != "" {
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE(trigger_id::text,'') FROM manager_sessions WHERE id=$1 AND workspace_id=$2`, target.Session, target.Workspace).Scan(&triggerID); err != nil {
			return visibleError(err)
		}
	}
	if triggerID != "" {
		var allowed bool
		if err = tx.QueryRowContext(ctx, `SELECT manager_trigger_admin_allowed(id,$2) FROM manager_triggers WHERE id=$1 FOR SHARE`, triggerID, target.Actor).Scan(&allowed); err != nil {
			return visibleError(err)
		}
		if !dispatch && (!target.Control || !allowed) {
			return service.Fail("trigger_task_forbidden", 403, "触发器任务仅接受可信事件输入")
		}
	}
	var owner string
	if err = tx.QueryRowContext(ctx, `SELECT created_by,lifecycle FROM manager_task_spaces WHERE workspace_id=$1 AND id=$2 FOR UPDATE`, target.Workspace, target.TaskSpaceID).Scan(&owner, &t.state.OwnerLifecycle); err != nil {
		return visibleError(err)
	}
	t.state.AgentID = target.AgentID
	t.state.TaskSpaceID = &target.TaskSpaceID
	if !dispatch {
		var systemSpace bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM manager_work_items WHERE task_space_id=$1)`, target.TaskSpaceID).Scan(&systemSpace); err != nil {
			return err
		}
		if systemSpace && !target.Control {
			return service.Fail("task_space_forbidden", 403, "事项空间由当前事项派发")
		}
	}
	if !dispatch && triggerID == "" {
		var allowed bool
		if err = tx.QueryRowContext(ctx, `SELECT manager_task_space_actor_allowed($1,$2,$3)`, target.TaskSpaceID, target.Actor, target.AgentID).Scan(&allowed); err != nil {
			return err
		}
		if owner != target.Actor || (!target.Control && !allowed) {
			return service.Fail("task_space_forbidden", 403, "任务空间或员工权限不可用")
		}
	}
	t.ctx = platform.WithTaskSpaceExecution(ctx, platform.TaskSpaceExecution{TaskSpaceID: target.TaskSpaceID})
	if target.Session != "" {
		var id string
		if err = tx.QueryRowContext(ctx, `SELECT s.id FROM manager_sessions s LEFT JOIN manager_task_space_agents g ON g.task_space_id=s.task_space_id AND g.agent_id=s.agent_id WHERE s.id=$1 AND s.workspace_id=$2 AND s.task_space_id=$3 AND s.agent_id=$4 AND ($5 OR s.task_space_agent_epoch=g.admission_epoch) FOR UPDATE OF s`, target.Session, target.Workspace, target.TaskSpaceID, target.AgentID, dispatch || target.Control).Scan(&id); err != nil {
			return visibleError(err)
		}
		if err = t.refresh(); err != nil {
			return err
		}
	}
	if err = fn(t); err != nil {
		return err
	}
	return tx.Commit()
}

func stopTaskSpaceAgentSessions(ctx context.Context, tx *sql.Tx, store *Store, w, actor, space, agent string) error {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM manager_sessions WHERE task_space_id=$1 AND agent_id=$2 AND lifecycle='active' ORDER BY id FOR UPDATE`, space, agent)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Explicit close is checked below.
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return errors.Join(err, rows.Close())
		}
		ids = append(ids, id)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, id := range ids {
		t := &sessionTx{ctx: ctx, tx: tx, store: store, target: service.Target{Workspace: w, Actor: actor, Session: id, TaskSpaceID: space, AgentID: agent}}
		if err = t.refresh(); err != nil {
			return err
		}
		items, e := t.CancelQueued("access_revoked")
		if e != nil {
			return e
		}
		for _, i := range items {
			if e = t.Event("input.updated", &i.ID, nil, service.InputFields(i)); e != nil {
				return e
			}
		}
		if e = t.Stop(false); e != nil {
			return e
		}
		if e = t.SnapshotEvent(); e != nil {
			return e
		}
	}
	return nil
}

func (s *Store) resolveTaskSpaceTarget(ctx context.Context, target service.Target) (service.Target, error) {
	if target.TaskSpaceID != "" || target.Session == "" {
		return target, nil
	}
	err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(task_space_id::text,''),agent_id FROM manager_sessions WHERE workspace_id=$1 AND id=$2`, target.Workspace, target.Session).Scan(&target.TaskSpaceID, &target.AgentID)
	return target, visibleError(err)
}
