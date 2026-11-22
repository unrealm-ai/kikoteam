package repository

import (
	"context"
	"database/sql"
	"errors"
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"time"
)

const taskSpaceColumns = `id,workspace_id,business_key,created_by,lifecycle,version,created_at`

func loadTaskSpace(ctx context.Context, tx *sql.Tx, w, actor, id string) (service.TaskSpace, error) {
	var v service.TaskSpace
	err := tx.QueryRowContext(ctx, `SELECT `+taskSpaceColumns+` FROM manager_task_spaces WHERE workspace_id=$1 AND id=$2 AND created_by=$3`, w, id, actor).Scan(&v.ID, &v.WorkspaceID, &v.BusinessKey, &v.CreatedBy, &v.Lifecycle, &v.Version, &v.CreatedAt)
	if err != nil {
		return v, visibleError(err)
	}
	v.Agents = []service.TaskSpaceAgent{}
	rows, err := tx.QueryContext(ctx, `SELECT agent_id,status,admission_epoch FROM manager_task_space_agents WHERE task_space_id=$1 ORDER BY agent_id`, id)
	if err != nil {
		return v, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Explicit close is checked below.
	for rows.Next() {
		var a service.TaskSpaceAgent
		if err = rows.Scan(&a.AgentID, &a.Status, &a.AdmissionEpoch); err != nil {
			return v, errors.Join(err, rows.Close())
		}
		v.Agents = append(v.Agents, a)
	}
	return v, errors.Join(rows.Err(), rows.Close())
}
func lockTaskSpaceMember(ctx context.Context, tx *sql.Tx, w, actor string) error {
	var id string
	return visibleError(tx.QueryRowContext(ctx, `SELECT w.id FROM platform_workspaces w JOIN platform_members m ON m.workspace_id=w.id AND m.user_id=$2 AND m.status='active' WHERE w.id=$1 FOR SHARE OF w,m`, w, actor).Scan(&id))
}
func lockTaskSpaceAgent(ctx context.Context, tx *sql.Tx, w, actor, agent string, requireAllowed bool) error {
	var allowed bool
	err := tx.QueryRowContext(ctx, `SELECT platform_agent_web_allowed($1,id,$3) FROM platform_agents WHERE workspace_id=$1 AND id=$2 FOR SHARE`, w, agent, actor).Scan(&allowed)
	if err != nil {
		return visibleError(err)
	}
	if requireAllowed && !allowed {
		return service.Fail("employee_access_denied", 403, "没有员工使用权限")
	}
	return nil
}
func (s *Store) CreateTaskSpace(ctx context.Context, w, actor, key, fp string, q service.TaskSpaceCreate) (service.TaskSpace, bool, error) {
	var out service.TaskSpace
	replayed := false
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, false, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	if err = lockTaskSpaceMember(ctx, tx, w, actor); err != nil {
		return out, false, err
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, w+":"+actor+":taskspace:"+key); err != nil {
		return out, false, err
	}
	if err = lockTaskSpaceAgent(ctx, tx, w, actor, q.AgentID, true); err != nil {
		return out, false, err
	}
	var id, prior string
	err = tx.QueryRowContext(ctx, `SELECT task_space_id,fingerprint FROM manager_task_space_requests WHERE workspace_id=$1 AND actor_id=$2 AND request_key=$3`, w, actor, key).Scan(&id, &prior)
	if err == nil {
		if prior != fp {
			return out, false, service.Fail("idempotency_conflict", 409, "幂等键已用于其他内容")
		}
		replayed = true
	} else if !errors.Is(err, sql.ErrNoRows) {
		return out, false, err
	}
	if !replayed {
		id = service.NewID()
		_, err = tx.ExecContext(ctx, `INSERT INTO manager_task_spaces(id,workspace_id,business_key,created_by) VALUES($1,$2,$3,$4) ON CONFLICT(workspace_id,business_key) DO NOTHING`, id, w, q.BusinessKey, actor)
		if err != nil {
			return out, false, err
		}
		if q.BusinessKey != nil {
			if err = tx.QueryRowContext(ctx, `SELECT id FROM manager_task_spaces WHERE workspace_id=$1 AND business_key=$2 AND created_by=$3 AND lifecycle='active' FOR UPDATE`, w, *q.BusinessKey, actor).Scan(&id); err != nil {
				return out, false, visibleError(err)
			}
		}
		// A same-key lookup never silently grants a second employee.
		var exists bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM manager_task_space_agents WHERE task_space_id=$1)`, id).Scan(&exists); err != nil {
			return out, false, err
		}
		if !exists {
			if _, err = tx.ExecContext(ctx, `INSERT INTO manager_task_space_agents(task_space_id,workspace_id,agent_id) VALUES($1,$2,$3)`, id, w, q.AgentID); err != nil {
				return out, false, err
			}
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO manager_task_space_requests(workspace_id,actor_id,request_key,fingerprint,task_space_id) VALUES($1,$2,$3,$4,$5)`, w, actor, key, fp, id); err != nil {
			return out, false, err
		}
	}
	var allowed bool
	if err = tx.QueryRowContext(ctx, `SELECT manager_task_space_actor_allowed($1,$2,$3)`, id, actor, q.AgentID).Scan(&allowed); err != nil {
		return out, false, err
	}
	if !allowed {
		return out, false, service.Fail("task_space_forbidden", 403, "任务空间未授权此员工")
	}
	out, err = loadTaskSpace(ctx, tx, w, actor, id)
	if err != nil {
		return out, false, err
	}
	return out, replayed, tx.Commit()
}
func (s *Store) TaskSpace(ctx context.Context, w, actor, id string) (service.TaskSpace, error) {
	var out service.TaskSpace
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	if err = lockTaskSpaceMember(ctx, tx, w, actor); err != nil {
		return out, err
	}
	out, err = loadTaskSpace(ctx, tx, w, actor, id)
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}
func (s *Store) GrantTaskSpaceAgent(ctx context.Context, w, actor, id, agent string, q service.TaskSpaceGrant) (service.TaskSpace, error) {
	var out service.TaskSpace
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	if err = lockTaskSpaceMember(ctx, tx, w, actor); err != nil {
		return out, err
	}
	if err = lockTaskSpaceAgent(ctx, tx, w, actor, agent, q.Enabled); err != nil {
		return out, err
	}
	var version int64
	if err = tx.QueryRowContext(ctx, `SELECT version FROM manager_task_spaces WHERE workspace_id=$1 AND id=$2 AND created_by=$3 AND lifecycle='active' FOR UPDATE`, w, id, actor).Scan(&version); err != nil {
		return out, visibleError(err)
	}
	if version != q.Version {
		return out, service.Fail("version_conflict", 409, "任务空间版本已变化")
	}
	status := "removed"
	if q.Enabled {
		status = "active"
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO manager_task_space_agents(task_space_id,workspace_id,agent_id,status) VALUES($1,$2,$3,$4) ON CONFLICT(task_space_id,agent_id) DO UPDATE SET status=EXCLUDED.status,admission_epoch=manager_task_space_agents.admission_epoch+CASE WHEN manager_task_space_agents.status<>EXCLUDED.status THEN 1 ELSE 0 END`, id, w, agent, status); err != nil {
		return out, err
	}
	if !q.Enabled {
		if err = stopTaskSpaceAgentSessions(ctx, tx, s, w, actor, id, agent); err != nil {
			return out, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE manager_task_spaces SET version=version+1 WHERE id=$1`, id); err != nil {
		return out, err
	}
	out, err = loadTaskSpace(ctx, tx, w, actor, id)
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}

func (s *Store) TaskSpaces(ctx context.Context, w, actor string, before time.Time, id string, limit int) ([]service.TaskSpace, error) {
	out := []service.TaskSpace{}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	if err = lockTaskSpaceMember(ctx, tx, w, actor); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+taskSpaceColumns+` FROM manager_task_spaces WHERE workspace_id=$1 AND created_by=$2 AND ($3='' OR (created_at,id)<($4::timestamptz,NULLIF($3,'')::uuid)) ORDER BY created_at DESC,id DESC LIMIT $5`, w, actor, id, before, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Explicit close checked below.
	for rows.Next() {
		var v service.TaskSpace
		if err = rows.Scan(&v.ID, &v.WorkspaceID, &v.BusinessKey, &v.CreatedBy, &v.Lifecycle, &v.Version, &v.CreatedAt); err != nil {
			return nil, errors.Join(err, rows.Close())
		}
		v.Agents = []service.TaskSpaceAgent{}
		out = append(out, v)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}
