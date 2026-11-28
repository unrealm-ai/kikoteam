package repository

import (
	"context"
	"encoding/json"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

const managedChatScheduleColumns = `p.id,p.status,p.instruction,p.title,p.rule,p.next_due,p.version,
 COALESCE(b.provider,'web'),CASE WHEN c.web_group_id IS NOT NULL THEN 'group' ELSE COALESCE(c.channel_chat_type,'direct') END,
 CASE WHEN c.web_group_id IS NOT NULL OR c.channel_chat_type='group' THEN COALESCE(g.title,NULLIF(o.display_name,''),NULLIF(v.display_name,''),NULLIF(c.title,''),RIGHT(c.id::text,8))
 ELSE COALESCE(NULLIF(u.name,''),RIGHT(c.id::text,8)) END`
const managedChatScheduleFrom = ` FROM manager_chat_schedules p JOIN manager_sessions s ON s.id=p.source_session_id AND s.workspace_id=p.workspace_id
 JOIN manager_conversations c ON c.id=s.conversation_id AND c.workspace_id=p.workspace_id
 LEFT JOIN platform_accounts u ON u.id=p.actor_id
 LEFT JOIN platform_web_groups g ON g.id=c.web_group_id
 LEFT JOIN platform_channel_bindings b ON b.id=c.channel_binding_id
 LEFT JOIN platform_channel_observed_chats o ON o.binding_id=b.id AND o.identity_epoch=c.channel_identity_epoch AND o.external_tenant_id=c.channel_tenant_id AND o.external_chat_id=c.external_chat_id AND c.channel_chat_type='group'
 LEFT JOIN platform_channel_group_candidates v ON v.binding_id=b.id AND v.identity_epoch=c.channel_identity_epoch AND v.external_tenant_id=c.channel_tenant_id AND v.external_chat_id=c.external_chat_id AND v.connection_generation=b.connection_generation AND v.config_revision=b.config_revision AND v.expires_at>now() AND c.channel_chat_type='group'
 WHERE p.workspace_id=$1 AND s.agent_id=$2`

func scanManagedChatSchedule(row interface{ Scan(...any) error }) (service.ManagedChatSchedule, error) {
	var v service.ManagedChatSchedule
	var raw []byte
	err := row.Scan(&v.ID, &v.Status, &v.Instruction, &v.Title, &raw, &v.NextDue, &v.Version, &v.Channel, &v.ChatType, &v.SourceLabel)
	if err == nil {
		err = json.Unmarshal(raw, &v.Rule)
	}
	return v, visibleError(err)
}

func (s *Store) ManagedChatSchedules(ctx context.Context, w, actor, agent string) ([]service.ManagedChatSchedule, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Read transaction.
	if err = lockTriggerAdmin(ctx, tx, w, actor, agent); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+managedChatScheduleColumns+managedChatScheduleFrom+` ORDER BY (p.status='active') DESC,p.created_at DESC,p.id DESC LIMIT 100`, w, agent)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Query closure does not write.
	out := []service.ManagedChatSchedule{}
	for rows.Next() {
		v, e := scanManagedChatSchedule(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) UpdateManagedChatSchedule(ctx context.Context, w, actor, agent, id string, q service.ManagedChatSchedulePatch) (service.ManagedChatSchedule, error) {
	out := service.ManagedChatSchedule{}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Explicit commit owns success.
	if err = lockTriggerAdmin(ctx, tx, w, actor, agent); err != nil {
		return out, err
	}
	base := `SELECT ` + managedChatScheduleColumns + managedChatScheduleFrom + ` AND p.id=$3`
	v, err := scanManagedChatSchedule(tx.QueryRowContext(ctx, base+` FOR UPDATE OF p`, w, agent, id))
	if err != nil {
		return out, err
	}
	if v.Version != q.Version {
		return out, service.Fail("version_conflict", 409, "定时计划已变更，请刷新")
	}
	if v.Status != "active" && v.Status != "paused" {
		return out, service.Fail("chat_schedule_inactive", 409, "已取消或已结束的计划不能重新启用")
	}
	var now time.Time
	if err = tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return out, err
	}
	rule, due, status, err := chatScheduleChange(v.Status, v.Rule, v.NextDue, q.Rule, q.Enabled, now)
	if err != nil {
		return out, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE manager_chat_schedules SET rule=$2,next_due=$3,status=$4,version=version+1 WHERE id=$1`, id, rule, due, status); err != nil {
		return out, err
	}
	out, err = scanManagedChatSchedule(tx.QueryRowContext(ctx, base, w, agent, id))
	if err == nil {
		err = tx.Commit()
	}
	return out, err
}

func chatScheduleChange(status string, rule service.ChatScheduleRule, currentDue *time.Time, replacement *service.ChatScheduleRule, enabled *bool, now time.Time) ([]byte, *time.Time, string, error) {
	currentStatus := status
	if replacement != nil {
		var err error
		rule, err = replacement.Normalize(now)
		if err != nil {
			return nil, nil, "", err
		}
	}
	if enabled != nil {
		status = "paused"
		if *enabled {
			status = "active"
		}
	}
	var due *time.Time
	if status == "active" {
		if replacement == nil && currentStatus == "active" {
			due = currentDue
		} else {
			next, err := rule.Next(now)
			if err != nil {
				return nil, nil, "", err
			}
			if next.IsZero() {
				return nil, nil, "", service.Fail("chat_schedule_inactive", 409, "一次性时间已过，请先修改执行时间")
			}
			due = &next
		}
	}
	raw, err := json.Marshal(rule)
	return raw, due, status, err
}
