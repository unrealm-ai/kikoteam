package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

// The overview and the navigation badge use the same open states.
const historyOpen = "r.state IN ('pending','preparing','blocked')"
const historyFrom = ` FROM manager_human_requests r JOIN platform_agents a ON a.id=r.agent_id
 JOIN platform_members viewer ON viewer.workspace_id=r.workspace_id AND viewer.user_id=$2 AND viewer.status='active'`
const historyWhere = ` WHERE r.workspace_id=$1 AND ($3::text='' OR r.id=NULLIF($3,'')::uuid)`

func (s *Store) HumanPendingCount(ctx context.Context, workspace, actor string) (int, error) {
	if !service.ValidID(workspace) {
		return 0, service.Fail("invalid_reference", 400, "工作区标识无效")
	}
	var count int
	err := s.DB.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM manager_human_requests r WHERE r.workspace_id=m.workspace_id AND `+historyOpen+`)
 FROM platform_members m WHERE m.workspace_id=$1 AND m.user_id=$2 AND m.status='active'`, workspace, actor).Scan(&count)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, service.Fail("workspace_forbidden", 403, "工作区不可访问")
	}
	return count, err
}

func (s *Store) HumanHistory(ctx context.Context, workspace, actor, filter, id string, offset int) (service.HumanHistoryPage, error) {
	out := service.HumanHistoryPage{Items: []service.HumanHistoryItem{}}
	if !service.ValidID(workspace) {
		return out, service.Fail("invalid_reference", 400, "工作区标识无效")
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return out, err
	}
	defer tx.Rollback() //nolint:errcheck // A committed or failed read transaction is already closed.
	var member bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM platform_members WHERE workspace_id=$1 AND user_id=$2 AND status='active')`, workspace, actor).Scan(&member); err != nil {
		return out, err
	}
	if !member {
		return out, service.Fail("workspace_forbidden", 403, "工作区不可访问")
	}
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FILTER (WHERE "+historyOpen+"), count(*) FILTER (WHERE NOT ("+historyOpen+"))"+historyFrom+historyWhere, workspace, actor, id).Scan(&out.Pending, &out.Resolved); err != nil {
		return out, err
	}
	out.Total = out.Pending + out.Resolved
	bucket := ""
	switch filter {
	case "pending":
		bucket = " AND " + historyOpen
		out.Total = out.Pending
	case "resolved":
		bucket = " AND NOT (" + historyOpen + ")"
		out.Total = out.Resolved
	}
	rows, err := tx.QueryContext(ctx, `SELECT r.id,r.version,r.state,r.created_at,r.answered_at,detail.visible,
 CASE WHEN detail.visible THEN r.question END,
 CASE WHEN detail.visible THEN a.name END,
 CASE WHEN detail.visible THEN r.recipient->>'provider' END,
 CASE WHEN detail.visible THEN COALESCE(manager_human_recipient_name(r.workspace_id,r.agent_id,r.recipient,false),
 (SELECT entry->>'name' FROM platform_agent_human_recipients h CROSS JOIN LATERAL jsonb_array_elements(h.recipients) entry
  WHERE h.agent_id=r.agent_id AND entry->>'id'=r.question->>'recipient_id' AND entry->'ref'=r.recipient LIMIT 1),'') END,
 CASE WHEN detail.visible THEN r.answer END,
 CASE WHEN detail.visible THEN (SELECT c.body FROM manager_human_clarifications c WHERE c.request_id=r.id
 AND c.question_version=r.version ORDER BY c.created_at DESC,c.body_digest DESC LIMIT 1) END`+historyFrom+historyDetailAccess()+historyWhere+bucket+`
 ORDER BY r.created_at DESC,r.id DESC LIMIT 100 OFFSET $4`, workspace, actor, id, offset)
	if err != nil {
		return out, err
	}
	defer rows.Close() //nolint:errcheck // Iteration errors are reported below.
	for rows.Next() {
		v, err := scanHumanHistory(rows)
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, v)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	return out, tx.Commit()
}

// This is a Web membership view. An external channel identity is not inferred
// from a name or from the authenticated platform account UUID.
func historyDetailAccess() string {
	return ` CROSS JOIN LATERAL (SELECT (
 COALESCE(r.recipient @> jsonb_build_object('kind','user','provider','web',
 'workspace_id',r.workspace_id::text,'agent_id',r.agent_id::text,'external_id',viewer.user_id::text),false)
 OR EXISTS(SELECT 1` + sessionFrom + ` WHERE s.workspace_id=r.workspace_id AND s.id=r.session_id
 AND ` + sessionReadWithPlanning(memberRead) + `)) AS visible) detail`
}

func scanHumanHistory(row scanner) (v service.HumanHistoryItem, err error) {
	var question, answer []byte
	var clarification sql.NullString
	err = row.Scan(&v.ID, &v.Version, &v.State, &v.CreatedAt, &v.AnsweredAt, &v.DetailVisible,
		&question, &v.AgentName, &v.RecipientProvider, &v.RecipientName, &answer, &clarification)
	if err != nil || !v.DetailVisible {
		return v, err
	}
	v.Clarification = clarification.String
	if err = json.Unmarshal(question, &v.Question); err == nil && len(answer) > 0 {
		err = json.Unmarshal(answer, &v.Answer)
	}
	return v, err
}
