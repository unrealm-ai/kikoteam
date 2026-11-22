package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	platform "github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func boardProjection(ctx context.Context, actor string) string {
	return `SELECT w.id,w.title,w.goal,w.criteria,w.status,
 CASE WHEN w.reason<>'' THEN w.reason WHEN w.status='awaiting_acceptance' AND NOT manager_work_review_ready(r.id) THEN '等待原模型恢复' ELSE '' END,
 origin.name,CASE WHEN w.current_submission_id IS NOT NULL THEN origin.name ELSE COALESCE(target.name,origin.name) END,w.created_at,w.updated_at,
 array_to_json(ARRAY(SELECT k.id ` + sessionFrom + ` JOIN manager_tasks k ON k.id=s.task_id
 WHERE s.workspace_id=w.workspace_id AND s.id IN (SELECT w.root_session_id UNION ALL
 SELECT wa.session_id FROM manager_work_assignments wa WHERE wa.work_item_id=w.id) AND ` + sessionRead(ctx, actor) +
		` ORDER BY k.created_at DESC,k.id DESC LIMIT 3))` + boardFrom
}

const boardFrom = ` FROM manager_work_items w JOIN manager_sessions r ON r.id=w.root_session_id JOIN platform_agents origin ON origin.id=r.agent_id
 LEFT JOIN manager_work_assignments a ON a.id=w.current_assignment_id LEFT JOIN platform_agents target ON target.id=a.target_agent_id`

// Use the source task's visibility for both board rows and direct timeline links.
const boardVisible = `EXISTS(SELECT 1 ` + sessionFrom + ` WHERE s.id=w.root_session_id AND (` + memberRead + `
 OR (c.web_group_id IS NOT NULL AND manager_web_group_actor_allowed(c.id,$2))))`

func scanOverview(row scanner) (v service.WorkOverview, err error) {
	var links []byte
	err = row.Scan(&v.ID, &v.Title, &v.Goal, &v.Criteria, &v.Status, &v.Reason, &v.Origin, &v.Owner, &v.CreatedAt, &v.UpdatedAt, &links)
	if err == nil {
		err = json.Unmarshal(links, &v.LinkedTaskIDs)
	}
	return
}

const boardFilter = ` WHERE w.workspace_id=$1 AND ` + boardVisible + ` AND ($3='' OR w.status=$3)
 AND ($4::text='' OR (CASE WHEN w.current_submission_id IS NOT NULL THEN r.agent_id ELSE COALESCE(a.target_agent_id,r.agent_id) END)=NULLIF($4,'')::uuid)`

func (s *Store) WorkCounts(ctx context.Context, w, actor, status, employee string) (map[string]int, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT w.status,count(*)`+boardFrom+boardFilter+` GROUP BY w.status`, w, actor, status, employee)
	return readStatusCounts(rows, err)
}
func (s *Store) WorkList(ctx context.Context, w, actor, status, employee string, before time.Time, id string, n int) ([]service.WorkOverview, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Release locks on every exit; commits report their own errors.
	if err = lockTaskSpaceMember(ctx, tx, w, actor); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, boardProjection(ctx, actor)+boardFilter+`
 AND ($5::uuid IS NULL OR (w.created_at,w.id)<($6,$5)) ORDER BY w.created_at DESC,w.id DESC LIMIT $7`, w, actor, status, employee, nullID(id), before, n)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Iteration errors are returned.
	items := []service.WorkOverview{}
	for rows.Next() {
		v, e := scanOverview(rows)
		if e != nil {
			return nil, e
		}
		items = append(items, v)
	}
	return items, rows.Err()
}
func (s *Store) WorkDetail(ctx context.Context, w, actor, id string, before int64, n int) (out service.WorkDetail, err error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Release locks on every exit; commits report their own errors.
	if err = lockTaskSpaceMember(ctx, tx, w, actor); err != nil {
		return out, err
	}
	out.WorkOverview, err = scanOverview(tx.QueryRowContext(ctx, boardProjection(ctx, actor)+` WHERE w.workspace_id=$1 AND `+boardVisible+` AND w.id=$3`, w, actor, id))
	if err != nil {
		return out, visibleError(err)
	}
	rows, err := tx.QueryContext(ctx, `SELECT e.id,e.kind,COALESCE(a.name,''),COALESCE(b.name,''),e.summary,e.created_at,
 COALESCE((SELECT jsonb_agg(jsonb_build_object('conversation_id',d.conversation_id,'destination',COALESCE(g.title,NULLIF(dest.title,''),'来源会话'),'status',COALESCE(t.status,c.status,d.status)) ORDER BY d.conversation_id) FROM manager_work_notice_targets d JOIN manager_conversations dest ON dest.id=d.conversation_id LEFT JOIN platform_web_groups g ON g.id=dest.web_group_id LEFT JOIN manager_trigger_notifications t ON t.id=d.trigger_notice_id LEFT JOIN manager_channel_deliveries c ON c.id=d.channel_delivery_id WHERE d.event_id=e.id),'[]')
 FROM manager_work_events e LEFT JOIN platform_agents a ON a.id=e.agent_id LEFT JOIN platform_agents b ON b.id=e.target_agent_id
 WHERE e.work_item_id=$1 AND ($2::bigint=0 OR e.id<$2) ORDER BY e.id DESC LIMIT $3`, id, before, n+1)
	if err != nil {
		return out, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Iteration errors are returned.
	out.Events = []service.WorkHistory{}
	for rows.Next() {
		var h service.WorkHistory
		var raw []byte
		if err = rows.Scan(&h.ID, &h.Kind, &h.Employee, &h.Target, &h.Summary, &h.CreatedAt, &raw); err != nil {
			return out, err
		}
		if err = json.Unmarshal(raw, &h.Deliveries); err != nil {
			return out, err
		}
		out.Events = append(out.Events, h)
	}
	if len(out.Events) > n {
		out.More = true
		out.Events = out.Events[:n]
	}
	return out, rows.Err()
}
func (s *Store) ReceiverPolicy(ctx context.Context, w, actor, agent string, next *service.ReceiverPolicy) (out service.ReceiverPolicy, err error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Release locks on every exit; commits report their own errors.
	if err = lockTriggerAdmin(ctx, tx, w, actor, agent); err != nil {
		return out, err
	}
	if next != nil {
		if err = next.Scope.Validate("all"); err != nil {
			return out, err
		}
		var valid bool
		if err = tx.QueryRowContext(ctx, `SELECT count(*)=cardinality($3::uuid[]) FROM platform_agents WHERE workspace_id=$1 AND id<>$2 AND id=ANY($3)`, w, agent, next.Scope.EmployeeIDs).Scan(&valid); err != nil {
			return out, err
		}
		if !valid {
			return out, service.Fail("invalid_work_scope", 400, "来源员工无效")
		}
		raw, e := json.Marshal(next.Scope)
		if e != nil {
			return out, e
		}
		if next.Version == 0 {
			err = tx.QueryRowContext(ctx, `INSERT INTO platform_work_receivers(agent_id,scope,version) VALUES($1,$2,1) ON CONFLICT DO NOTHING RETURNING version`, agent, raw).Scan(&out.Version)
		} else {
			err = tx.QueryRowContext(ctx, `UPDATE platform_work_receivers SET scope=$2,version=version+1 WHERE agent_id=$1 AND version=$3 RETURNING version`, agent, raw, next.Version).Scan(&out.Version)
		}
		if errors.Is(err, sql.ErrNoRows) {
			return out, service.Fail("work_scope_changed", 409, "员工接手规则已变更，请重新读取后保存")
		}
		if err != nil {
			return out, err
		}
		out.Scope = next.Scope
		return out, tx.Commit()
	}
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT version,scope FROM platform_work_receivers WHERE agent_id=$1`, agent).Scan(&out.Version, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		out.Scope = platform.WorkScope{Mode: "all", EmployeeIDs: []string{}}
		return out, nil
	}
	if err == nil {
		err = json.Unmarshal(raw, &out.Scope)
	}
	return out, err
}
