package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	platform "github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

const humanRecoveryOpen = `r.state='pending' AND r.fenced_at IS NOT NULL AND r.answer IS NULL
 AND s.active_turn_id IS NULL AND s.stop_revision=s.applied_stop_revision AND (` + humanRequestActive + `)
 AND NOT EXISTS(SELECT 1 FROM manager_human_clarifications x WHERE x.request_id=r.id AND x.question_version=r.version)
 AND NOT EXISTS(SELECT 1 FROM manager_human_numbered_answers x WHERE x.request_id=r.id AND x.question_version=r.version)`

const humanRecoverySafe = `(n.id IS NULL OR n.status IN ('pending','failed','cancelled') AND n.attempt_id IS NULL
 OR n.status='failed' AND n.evidence_type='not_dispatched')`

const humanRecoveryJoin = ` FROM manager_human_requests r JOIN manager_sessions s ON s.id=r.session_id
 LEFT JOIN manager_conversations c ON c.id=s.conversation_id LEFT JOIN manager_task_spaces p ON p.id=s.task_space_id
 LEFT JOIN manager_human_notices n ON n.request_id=r.id AND n.request_version=r.version AND n.recipient=r.recipient `

func humanRecoveryAdmin(ctx context.Context, tx *sql.Tx, w, actor, agent string) error {
	if !service.ValidID(w) || !service.ValidID(actor) || !service.ValidID(agent) {
		return service.Fail("invalid_reference", 400, "工作区、员工或操作者标识无效")
	}
	var id string
	return visibleError(tx.QueryRowContext(ctx, `SELECT a.id FROM platform_agents a JOIN platform_members m ON m.workspace_id=a.workspace_id
 WHERE a.id=$1 AND a.workspace_id=$2 AND m.user_id=$3 AND m.status='active' AND platform_can_manage_agent(a.workspace_id,a.id,m.user_id) FOR SHARE OF a,m`, agent, w, actor).Scan(&id))
}

func (s *Store) HumanRecoveryList(ctx context.Context, w, actor, agent string) (service.HumanRecoveryListing, error) {
	out := service.HumanRecoveryListing{Items: []service.HumanRecoveryItem{}, Recipients: []service.HumanRecipient{}}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	if err = humanRecoveryAdmin(ctx, tx, w, actor, agent); err != nil {
		return out, err
	}
	recipients, err := eligibleHumanRecipients(ctx, tx, w, agent)
	if err != nil {
		return out, err
	}
	out.Recipients = recipients.Recipients
	query := `SELECT r.id,r.version,r.recipient->>'provider',
 COALESCE(n.status,CASE WHEN r.state<>'pending' THEN r.state WHEN r.recipient->>'provider'='web' THEN 'web_inbox' ELSE 'not_sent' END),
 COALESCE(n.error_code,''), COALESCE((` + humanRecoveryOpen + `) AND ` + humanRecoverySafe + `,false),
 (` + humanRecipientStillConfigured + `) AND manager_human_recipient_name(r.workspace_id,r.agent_id,r.recipient) IS NOT NULL ` +
		humanRecoveryJoin + ` WHERE r.workspace_id=$1 AND r.agent_id=$2 AND r.state IN ('pending','blocked') ORDER BY r.created_at DESC,r.id DESC LIMIT 100`
	rows, err := tx.QueryContext(ctx, query, w, agent)
	if err != nil {
		return out, err
	}
	defer rows.Close() //nolint:errcheck // Iteration reports read failures.
	for rows.Next() {
		var item service.HumanRecoveryItem
		var safe, valid bool
		if err = rows.Scan(&item.ID, &item.Version, &item.Provider, &item.Status, &item.ErrorCode, &safe, &valid); err != nil {
			return out, err
		}
		item.CanRetry = safe && valid && item.Provider != "web"
		item.CanReassign = safe && len(out.Recipients) > 0
		out.Items = append(out.Items, item)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	return out, tx.Commit()
}

type humanRecoveryTarget struct {
	version   int64
	ref       platform.SelectionRef
	raw       []byte
	candidate string
}

func (s *Store) RecoverHumanRequest(ctx context.Context, w, actor, agent, id string, action service.HumanRecoveryAction) (service.HumanRecoveryItem, error) {
	var out service.HumanRecoveryItem
	action.Reason = strings.TrimSpace(action.Reason)
	if !service.ValidID(id) || action.Version < 1 || !validHumanRecoveryAction(action) {
		return out, service.Fail("invalid_human_recovery", 400, "请提供当前版本、有效操作和原因")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	if err = humanRecoveryAdmin(ctx, tx, w, actor, agent); err != nil {
		return out, err
	}
	target, err := lockHumanRecovery(ctx, tx, w, agent, id, action.Version)
	if err != nil {
		return out, err
	}
	recipients, err := eligibleHumanRecipients(ctx, tx, w, agent)
	if err != nil {
		return out, err
	}
	chosen, err := chooseRecoveryRecipient(target, recipients, action)
	if err != nil {
		return out, err
	}
	out, err = applyHumanRecovery(ctx, tx, id, actor, action, target, chosen, recipients.Version)
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}

func validHumanRecoveryAction(a service.HumanRecoveryAction) bool {
	return a.Reason != "" && utf8.RuneCountInString(a.Reason) <= 1000 && !strings.ContainsRune(a.Reason, 0) &&
		(a.Action == "retry" && a.RecipientID == "" || a.Action == "reassign" && service.ValidID(a.RecipientID))
}

func lockHumanRecovery(ctx context.Context, tx *sql.Tx, w, agent, id string, expected int64) (humanRecoveryTarget, error) {
	var target humanRecoveryTarget
	var session string
	if err := tx.QueryRowContext(ctx, `SELECT s.id FROM manager_human_requests r JOIN manager_sessions s ON r.session_id=s.id
 WHERE r.id=$1 AND r.workspace_id=$2 AND r.agent_id=$3 FOR UPDATE OF s`, id, w, agent).Scan(&session); err != nil {
		return target, visibleError(err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT version,recipient,question->>'recipient_id' FROM manager_human_requests WHERE id=$1 FOR UPDATE`, id).
		Scan(&target.version, &target.raw, &target.candidate); err != nil {
		return target, err
	}
	if target.version != expected {
		return target, service.Fail("human_request_changed", 409, "确认请求已变化，请刷新")
	}
	if err := json.Unmarshal(target.raw, &target.ref); err != nil {
		return target, err
	}
	// Block leasing of the current row until the version change commits.
	if _, err := tx.ExecContext(ctx, `SELECT id FROM manager_human_notices WHERE request_id=$1 AND request_version=$2 FOR UPDATE`, id, expected); err != nil {
		return target, err
	}
	var safe bool
	query := `SELECT COALESCE((` + humanRecoveryOpen + `) AND ` + humanRecoverySafe + `,false) ` + humanRecoveryJoin + ` WHERE r.id=$1`
	if err := tx.QueryRowContext(ctx, query, id).Scan(&safe); err != nil {
		return target, err
	}
	if !safe {
		return target, service.Fail("human_recovery_blocked", 409, "不能证明通知未发送，或确认已开始处理；不能重试或改派")
	}
	return target, nil
}

func chooseRecoveryRecipient(target humanRecoveryTarget, candidates service.HumanRecipients, action service.HumanRecoveryAction) (service.HumanRecipient, error) {
	for _, c := range candidates.Recipients {
		if action.Action == "retry" && c.ID == target.candidate && c.Ref == target.ref && c.Ref.Provider != "web" {
			return c, nil
		}
		if action.Action == "reassign" && c.ID == action.RecipientID && c.Ref != target.ref {
			return c, nil
		}
	}
	return service.HumanRecipient{}, service.Fail("human_recipient_forbidden", 403, "请选择已配置且当前已验证的不同确认人")
}

func applyHumanRecovery(ctx context.Context, tx *sql.Tx, id, actor string, action service.HumanRecoveryAction, target humanRecoveryTarget, selected service.HumanRecipient, rulesVersion int64) (service.HumanRecoveryItem, error) {
	out := service.HumanRecoveryItem{ID: id, Version: target.version + 1, Provider: selected.Ref.Provider, Status: "not_sent"}
	raw, err := json.Marshal(selected.Ref)
	if err != nil {
		return out, err
	}
	if out.Provider == "web" {
		out.Status = "web_inbox"
	}
	_, err = tx.ExecContext(ctx, `UPDATE manager_human_requests SET version=$2,recipient=$3,rules_version=$5,
 question=question||jsonb_build_object('recipient_id',$4::text,'rules_version',$5::bigint::text),proactive_notice_enabled=true WHERE id=$1`,
		id, out.Version, raw, selected.ID, rulesVersion)
	if err != nil {
		return out, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO manager_human_recovery_audit(request_id,actor_id,action,reason,previous_version,next_version,previous_recipient,next_recipient) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
		id, actor, action.Action, action.Reason, target.version, out.Version, target.raw, raw)
	return out, err
}
