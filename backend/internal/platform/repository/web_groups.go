package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type WebGroupStore struct{ DB *sql.DB }
type webGroupReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

const webGroupColumns = `g.id,g.workspace_id,g.title,g.status,g.version,g.created_at`

func scanWebGroup(row scanner) (service.WebGroup, error) {
	var g service.WebGroup
	err := row.Scan(&g.ID, &g.WorkspaceID, &g.Title, &g.Status, &g.Version, &g.CreatedAt)
	g.Members = []service.WebGroupMember{}
	g.Agents = []service.WebGroupAgent{}
	if errors.Is(err, sql.ErrNoRows) {
		err = service.Fail("web_group_not_found", 404, "群不存在或不可见")
	}
	return g, err
}
func webGroupAdmin(ctx context.Context, r webGroupReader, w, actor, id string) (bool, error) {
	var yes bool
	err := r.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM platform_members m WHERE m.workspace_id=$1 AND m.user_id=$2 AND m.status='active' AND ($3='' OR EXISTS(SELECT 1 FROM platform_web_group_members gm WHERE gm.workspace_id=m.workspace_id AND gm.group_id=NULLIF($3,'')::uuid AND gm.user_id=m.user_id AND gm.status='active' AND gm.role IN ('owner','admin'))))`, w, actor, id).Scan(&yes)
	return yes, err
}
func loadWebGroup(ctx context.Context, r webGroupReader, w, id string) (service.WebGroup, error) {
	g, err := scanWebGroup(r.QueryRowContext(ctx, `SELECT `+webGroupColumns+` FROM platform_web_groups g WHERE g.workspace_id=$1 AND g.id=$2`, w, id))
	if err != nil {
		return g, err
	}
	rows, err := r.QueryContext(ctx, `SELECT m.user_id,m.role,a.name FROM platform_web_group_members m JOIN platform_accounts a ON a.id=m.user_id WHERE m.workspace_id=$1 AND m.group_id=$2 AND m.status='active' ORDER BY m.user_id`, w, id)
	if err != nil {
		return g, err
	}
	defer rows.Close() //nolint:errcheck // Explicit close and iteration errors are checked.
	for rows.Next() {
		var m service.WebGroupMember
		if err = rows.Scan(&m.UserID, &m.Role, &m.Name); err != nil {
			return g, errors.Join(err, rows.Close())
		}
		g.Members = append(g.Members, m)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return g, err
	}
	rows, err = r.QueryContext(ctx, `SELECT ga.agent_id,a.name FROM platform_web_group_agents ga JOIN platform_agents a ON a.id=ga.agent_id WHERE ga.workspace_id=$1 AND ga.group_id=$2 AND ga.status='active' ORDER BY ga.agent_id`, w, id)
	if err != nil {
		return g, err
	}
	defer rows.Close() //nolint:errcheck // Explicit close and iteration errors are checked.
	for rows.Next() {
		var a service.WebGroupAgent
		if err = rows.Scan(&a.AgentID, &a.Name); err != nil {
			return g, errors.Join(err, rows.Close())
		}
		g.Agents = append(g.Agents, a)
	}
	return g, errors.Join(rows.Err(), rows.Close())
}
func (s *WebGroupStore) GetWebGroup(ctx context.Context, w, actor, id string) (service.WebGroup, error) {
	var result service.WebGroup
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	var locked string
	if err = tx.QueryRowContext(ctx, `SELECT id FROM platform_web_groups WHERE workspace_id=$1 AND id=$2 FOR SHARE`, w, id).Scan(&locked); errors.Is(err, sql.ErrNoRows) {
		return result, service.Fail("web_group_not_found", 404, "群不存在或不可见")
	}
	if err != nil {
		return result, err
	}
	if err = lockWebWorkspaceMember(ctx, tx, w, actor); err != nil {
		return result, err
	}
	var visible bool
	if err = tx.QueryRowContext(ctx, `SELECT platform_web_group_member_allowed($1,$2,$3)`, w, id, actor).Scan(&visible); err != nil {
		return result, err
	}
	if !visible {
		return result, service.Fail("web_group_not_found", 404, "群不存在或不可见")
	}
	result, err = loadWebGroup(ctx, tx, w, id)
	if err != nil {
		return result, err
	}
	return result, tx.Commit()
}
func (s *WebGroupStore) ListWebGroups(ctx context.Context, w, actor, q string, page, size int) (service.WebGroupPage, error) {
	result := service.WebGroupPage{Items: []service.WebGroup{}}
	var active bool
	if err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM platform_members WHERE workspace_id=$1 AND user_id=$2 AND status='active')`, w, actor).Scan(&active); err != nil {
		return result, err
	}
	if !active {
		return result, service.Fail("workspace_forbidden", 403, "没有工作区权限")
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+webGroupColumns+`,count(*) OVER() FROM platform_web_groups g WHERE g.workspace_id=$1 AND platform_web_group_member_allowed($1,g.id,$2) AND ($3='' OR strpos(lower(g.title),lower($3))>0) ORDER BY g.created_at DESC,g.id LIMIT $4 OFFSET $5`, w, actor, q, size, (page-1)*size)
	if err != nil {
		return result, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Explicit close and iteration are checked.
	for rows.Next() {
		var g service.WebGroup
		if err = rows.Scan(&g.ID, &g.WorkspaceID, &g.Title, &g.Status, &g.Version, &g.CreatedAt, &result.Total); err != nil {
			return result, errors.Join(err, rows.Close())
		}
		g.Members = []service.WebGroupMember{}
		g.Agents = []service.WebGroupAgent{}
		result.Items = append(result.Items, g)
	}
	return result, errors.Join(rows.Err(), rows.Close())
}
func (s *WebGroupStore) SaveWebGroup(ctx context.Context, w, actor, id string, q service.WebGroupPut) (service.WebGroup, error) {
	var result service.WebGroup
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	create := id == ""
	if create {
		id = service.NewID()
	}
	if !create {
		var locked string
		if err = tx.QueryRowContext(ctx, `SELECT id FROM platform_web_groups WHERE workspace_id=$1 AND id=$2 FOR UPDATE`, w, id).Scan(&locked); errors.Is(err, sql.ErrNoRows) {
			return result, service.Fail("web_group_not_found", 404, "群不存在")
		}
		if err != nil {
			return result, err
		}
	}
	if err = lockWebWorkspaceMember(ctx, tx, w, actor); err != nil {
		return result, err
	}
	scopeID := id
	if create {
		scopeID = ""
	}
	admin, err := webGroupAdmin(ctx, tx, w, actor, scopeID)
	if err != nil {
		return result, err
	}
	if !admin {
		return result, service.Fail("web_group_forbidden", 403, "没有群管理权限")
	}
	var previous service.WebGroup
	if create {
		if q.Version != 0 || q.Status != "active" {
			return result, service.Fail("web_group_version_conflict", 409, "新群版本或状态无效")
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO platform_web_groups(id,workspace_id,title,created_by) VALUES($1,$2,$3,$4)`, id, w, q.Title, actor); err != nil {
			return result, err
		}
	} else {
		previous, err = loadWebGroup(ctx, tx, w, id)
		if err != nil {
			return result, err
		}
		if previous.Version != q.Version || previous.Status != "active" {
			return result, service.Fail("web_group_version_conflict", 409, "群版本已变化或已归档")
		}
	}
	if !create {
		if err = validateWebGroupOwnership(actor, previous, q); err != nil {
			return result, err
		}
	}
	if err = validateWebGroupRoster(ctx, tx, w, actor, previous, q); err != nil {
		return result, err
	}
	if err = saveWebGroupRoster(ctx, tx, w, id, q); err != nil {
		return result, err
	}
	if !create {
		_, err = tx.ExecContext(ctx, `UPDATE platform_web_groups SET title=$2,status=$3,version=version+1,admission_epoch=admission_epoch+CASE WHEN $3='archived' THEN 1 ELSE 0 END WHERE id=$1`, id, q.Title, q.Status)
		if err != nil {
			return result, err
		}
	}
	result, err = loadWebGroup(ctx, tx, w, id)
	if err != nil {
		return result, err
	}
	return result, tx.Commit()
}
func validateWebGroupRoster(ctx context.Context, tx *sql.Tx, w, actor string, old service.WebGroup, q service.WebGroupPut) error {
	for _, m := range q.Members {
		var id string
		if err := tx.QueryRowContext(ctx, `SELECT user_id FROM platform_members WHERE workspace_id=$1 AND user_id=$2 AND status='active' FOR SHARE`, w, m.UserID).Scan(&id); errors.Is(err, sql.ErrNoRows) {
			return service.Fail("web_group_member_unavailable", 409, "群成员不属于当前有效工作区")
		} else if err != nil {
			return err
		}
	}
	retained := map[string]bool{}
	for _, a := range old.Agents {
		retained[a.AgentID] = true
	}
	for _, a := range q.Agents {
		var id string
		err := tx.QueryRowContext(ctx, `SELECT id FROM platform_agents WHERE workspace_id=$1 AND id=$2 AND ($4 OR (enabled AND (platform_agent_web_allowed($1,id,$3) OR (COALESCE((usage_config#>>'{access,group_members_enabled}')::boolean,true) AND NOT ((platform_agent_usage_projection(usage_config,'web',NULL,0)->'access'->'deny_ids') ? $3::text))))) FOR SHARE`, w, a.AgentID, actor, retained[a.AgentID]).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return service.Fail("web_group_agent_unavailable", 409, "员工不可用或没有使用权限")
		}
		if err != nil {
			return err
		}
	}
	return nil
}
func saveWebGroupRoster(ctx context.Context, tx *sql.Tx, w, id string, q service.WebGroupPut) error {
	var nextOwner string
	for _, m := range q.Members {
		if m.Role == "owner" {
			nextOwner = m.UserID
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE platform_web_group_members SET role='member' WHERE group_id=$1 AND role='owner' AND user_id<>$2`, id, nextOwner); err != nil {
		return err
	}
	memberIDs := []string{}
	agentIDs := []string{}
	for _, m := range q.Members {
		memberIDs = append(memberIDs, m.UserID)
		_, err := tx.ExecContext(ctx, `INSERT INTO platform_web_group_members(group_id,workspace_id,user_id,role,status) VALUES($1,$2,$3,$4,'active') ON CONFLICT(group_id,user_id) DO UPDATE SET role=EXCLUDED.role,status='active',admission_epoch=platform_web_group_members.admission_epoch+CASE WHEN platform_web_group_members.status='removed' THEN 1 ELSE 0 END`, id, w, m.UserID, m.Role)
		if err != nil {
			return err
		}
	}
	for _, a := range q.Agents {
		agentIDs = append(agentIDs, a.AgentID)
		_, err := tx.ExecContext(ctx, `INSERT INTO platform_web_group_agents(group_id,workspace_id,agent_id,status) VALUES($1,$2,$3,'active') ON CONFLICT(group_id,agent_id) DO UPDATE SET status='active',admission_epoch=platform_web_group_agents.admission_epoch+CASE WHEN platform_web_group_agents.status='removed' THEN 1 ELSE 0 END`, id, w, a.AgentID)
		if err != nil {
			return err
		}
	}
	members, err := json.Marshal(memberIDs)
	if err != nil {
		return err
	}
	agents, err := json.Marshal(agentIDs)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE platform_web_group_members SET status='removed',admission_epoch=admission_epoch+1 WHERE group_id=$1 AND status='active' AND NOT $2::jsonb ? user_id::text`, id, members); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE platform_web_group_agents SET status='removed',admission_epoch=admission_epoch+1 WHERE group_id=$1 AND status='active' AND NOT $2::jsonb ? agent_id::text`, id, agents)
	return err
}

func validateWebGroupOwnership(actor string, old service.WebGroup, q service.WebGroupPut) error {
	var previousOwner, nextOwner string
	for _, m := range old.Members {
		if m.Role == "owner" {
			previousOwner = m.UserID
		}
	}
	for _, m := range q.Members {
		if m.Role == "owner" {
			nextOwner = m.UserID
		}
	}
	if actor != previousOwner && (previousOwner != nextOwner || q.Status == "archived") {
		return service.Fail("web_group_owner_required", 403, "只有群主可以转让或解散群")
	}
	return nil
}

func (s *WebGroupStore) LeaveWebGroup(ctx context.Context, w, actor, id string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	var locked string
	if err = tx.QueryRowContext(ctx, `SELECT id FROM platform_web_groups WHERE workspace_id=$1 AND id=$2 FOR UPDATE`, w, id).Scan(&locked); errors.Is(err, sql.ErrNoRows) {
		return service.Fail("web_group_not_found", 404, "群不存在或不可见")
	}
	if err != nil {
		return err
	}
	if err = lockWebWorkspaceMember(ctx, tx, w, actor); err != nil {
		return err
	}
	var role, status string
	err = tx.QueryRowContext(ctx, `SELECT role,status FROM platform_web_group_members WHERE group_id=$1 AND workspace_id=$2 AND user_id=$3 FOR UPDATE`, id, w, actor).Scan(&role, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return service.Fail("web_group_not_found", 404, "群不存在或不可见")
	}
	if err != nil {
		return err
	}
	if status == "removed" {
		return tx.Commit()
	}
	if role == "owner" {
		return service.Fail("web_group_owner_transfer_required", 409, "群主请先转让群主或解散群")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE platform_web_group_members SET status='removed',admission_epoch=admission_epoch+1 WHERE group_id=$1 AND user_id=$2`, id, actor); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE platform_web_groups SET version=version+1 WHERE id=$1`, id); err != nil {
		return err
	}
	return tx.Commit()
}
func lockWebWorkspaceMember(ctx context.Context, tx *sql.Tx, w, actor string) error {
	var id string
	err := tx.QueryRowContext(ctx, `SELECT user_id FROM platform_members WHERE workspace_id=$1 AND user_id=$2 AND status='active' FOR SHARE`, w, actor).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return service.Fail("workspace_forbidden", 403, "没有工作区权限")
	}
	return err
}
