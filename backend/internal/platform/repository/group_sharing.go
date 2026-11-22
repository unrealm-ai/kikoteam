package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type GroupSharingStore struct{ DB *sql.DB }

const sharingColumns = `id,workspace_id,agent_id,name,mode,enabled,version,sources,targets,created_at,updated_at`

func scanSharingPolicy(row scanner) (service.GroupSharingPolicy, error) {
	var p service.GroupSharingPolicy
	var sources, targets []byte
	err := row.Scan(&p.ID, &p.WorkspaceID, &p.AgentID, &p.Name, &p.Mode, &p.Enabled, &p.Version, &sources, &targets, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return p, service.Fail("sharing_policy_not_found", 404, "共享策略不存在")
	}
	if err != nil {
		return p, err
	}
	if err = json.Unmarshal(sources, &p.Sources); err != nil {
		return p, err
	}
	err = json.Unmarshal(targets, &p.Targets)
	return p, err
}
func sharingAdmin(ctx context.Context, tx *sql.Tx, w, actor, a string) error {
	if err := usageAdmin(ctx, tx, w, actor); err != nil {
		return err
	}
	var id string
	err := tx.QueryRowContext(ctx, `SELECT id FROM platform_agents WHERE workspace_id=$1 AND id=$2 FOR SHARE`, w, a).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return service.Fail("agent_not_found", 404, "员工不存在")
	}
	return err
}
func (s *GroupSharingStore) ListSharingPolicies(ctx context.Context, w, actor, a, q string, page, size int) (service.GroupSharingPage, error) {
	out := service.GroupSharingPage{Items: []service.GroupSharingPolicy{}}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	if err = sharingAdmin(ctx, tx, w, actor, a); err != nil {
		return out, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM platform_group_sharing_policies WHERE workspace_id=$1 AND agent_id=$2 AND ($3='' OR strpos(lower(name),lower($3))>0)`, w, a, q).Scan(&out.Total); err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+sharingColumns+` FROM platform_group_sharing_policies WHERE workspace_id=$1 AND agent_id=$2 AND ($3='' OR strpos(lower(name),lower($3))>0) ORDER BY id LIMIT $4 OFFSET $5`, w, a, q, size, (page-1)*size)
	if err != nil {
		return out, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Explicit close below.
	for rows.Next() {
		v, e := scanSharingPolicy(rows)
		if e != nil {
			return out, errors.Join(e, rows.Close())
		}
		out.Items = append(out.Items, v)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return out, err
	}
	return out, tx.Commit()
}
func (s *GroupSharingStore) GetSharingPolicy(ctx context.Context, w, actor, a, id string) (service.GroupSharingPolicy, error) {
	var out service.GroupSharingPolicy
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	if err = sharingAdmin(ctx, tx, w, actor, a); err != nil {
		return out, err
	}
	out, err = scanSharingPolicy(tx.QueryRowContext(ctx, `SELECT `+sharingColumns+` FROM platform_group_sharing_policies WHERE workspace_id=$1 AND agent_id=$2 AND id=$3`, w, a, id))
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}
func (s *GroupSharingStore) SaveSharingPolicy(ctx context.Context, w, actor, a, id string, q service.GroupSharingPut, edges []service.GroupSharingEdge) (service.GroupSharingPolicy, error) {
	var out service.GroupSharingPolicy
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	// Match channel configuration lock order, then employee and policy.
	if _, err = tx.ExecContext(ctx, `SELECT id FROM platform_channel_config_clock WHERE id=true FOR UPDATE`); err != nil {
		return out, err
	}
	if err = sharingAdmin(ctx, tx, w, actor, a); err != nil {
		return out, err
	}
	var old service.GroupSharingPolicy
	action := "created"
	if id != "" {
		old, err = scanSharingPolicy(tx.QueryRowContext(ctx, `SELECT `+sharingColumns+` FROM platform_group_sharing_policies WHERE workspace_id=$1 AND agent_id=$2 AND id=$3 FOR UPDATE`, w, a, id))
		if err != nil {
			return out, err
		}
		if old.Version != q.Version {
			return out, service.Fail("sharing_version_conflict", 409, "共享策略已更新，请刷新")
		}
		action = "updated"
	} else {
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM platform_group_sharing_policies WHERE workspace_id=$1 AND agent_id=$2`, w, a).Scan(&count); err != nil {
			return out, err
		}
		if count >= 100 {
			return out, service.Fail("sharing_policy_limit", 409, "每个员工最多配置100条共享策略")
		}
	}
	if err = validateSharingChanges(ctx, tx, w, a, old, q, edges); err != nil {
		return out, err
	}
	sources, err := json.Marshal(q.Sources)
	if err != nil {
		return out, err
	}
	targets, err := json.Marshal(q.Targets)
	if err != nil {
		return out, err
	}
	if id == "" {
		id = service.NewID()
		out, err = scanSharingPolicy(tx.QueryRowContext(ctx, `INSERT INTO platform_group_sharing_policies(id,workspace_id,agent_id,name,mode,enabled,sources,targets,created_by,updated_by)VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$9)RETURNING `+sharingColumns, id, w, a, q.Name, q.Mode, q.Enabled, sources, targets, actor))
	} else {
		out, err = scanSharingPolicy(tx.QueryRowContext(ctx, `UPDATE platform_group_sharing_policies SET name=$4,mode=$5,enabled=$6,sources=$7,targets=$8,version=version+1,updated_by=$9,updated_at=now() WHERE workspace_id=$1 AND agent_id=$2 AND id=$3 RETURNING `+sharingColumns, w, a, id, q.Name, q.Mode, q.Enabled, sources, targets, actor))
	}
	if err != nil {
		return out, err
	}
	if err = replaceSharingEdges(ctx, tx, id, edges); err != nil {
		return out, err
	}
	if err = recordSharingPolicy(ctx, tx, actor, action, out); err != nil {
		return out, err
	}
	return out, tx.Commit()
}
func replaceSharingEdges(ctx context.Context, tx *sql.Tx, id string, edges []service.GroupSharingEdge) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM platform_group_sharing_edges WHERE policy_id=$1`, id); err != nil {
		return err
	}
	for index, edge := range edges {
		source, err := json.Marshal(edge.Source)
		if err != nil {
			return err
		}
		target, err := json.Marshal(edge.Target)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO platform_group_sharing_edges(policy_id,ordinal,source_ref,target_ref)VALUES($1,$2,$3,$4)`, id, index, source, target); err != nil {
			return err
		}
	}
	return nil
}
func recordSharingPolicy(ctx context.Context, tx *sql.Tx, actor, action string, p service.GroupSharingPolicy) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO platform_group_sharing_policy_events(id,workspace_id,agent_id,policy_id,actor_id,action,version,snapshot)VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, service.NewID(), p.WorkspaceID, p.AgentID, p.ID, actor, action, p.Version, raw)
	return err
}
func (s *GroupSharingStore) DeleteSharingPolicy(ctx context.Context, w, actor, a, id string, version int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	if err = sharingAdmin(ctx, tx, w, actor, a); err != nil {
		return err
	}
	old, err := scanSharingPolicy(tx.QueryRowContext(ctx, `SELECT `+sharingColumns+` FROM platform_group_sharing_policies WHERE workspace_id=$1 AND agent_id=$2 AND id=$3 FOR UPDATE`, w, a, id))
	if err != nil {
		return err
	}
	if old.Version != version {
		return service.Fail("sharing_version_conflict", 409, "共享策略已更新，请刷新")
	}
	if err = recordSharingPolicy(ctx, tx, actor, "deleted", old); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM platform_group_sharing_policies WHERE id=$1`, id); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *GroupSharingStore) PreviewSharingPolicy(ctx context.Context, w, actor, a string, q service.GroupSharingPut, edges []service.GroupSharingEdge) (service.GroupSharingPreview, error) {
	out := service.GroupSharingPreview{Edges: []service.GroupSharingEdge{}}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	if err = sharingAdmin(ctx, tx, w, actor, a); err != nil {
		return out, err
	}
	if err = validateSharingChanges(ctx, tx, w, a, service.GroupSharingPolicy{}, q, edges); err != nil {
		return out, err
	}
	out.Edges = edges
	return out, tx.Commit()
}
