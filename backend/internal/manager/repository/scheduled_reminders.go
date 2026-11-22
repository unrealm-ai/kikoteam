package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

// Caller holds the employee/trigger locks. Reminder registration and advancing
// the schedule occur in the same transaction; there is no model Task or actor.
func (s *Store) acceptScheduledReminderTx(ctx context.Context, tx *sql.Tx, b service.Trigger, q service.TriggerAcceptance) (service.TriggerReceipt, error) {
	out := service.TriggerReceipt{EventID: q.EventID, Status: "accepted"}
	if !b.Enabled || b.Kind != "schedule" || b.Version != q.BindingVersion || q.Command.Action != "send_message" || !slices.Contains(b.AllowedActions, "send_message") {
		return out, service.Fail("trigger_forbidden", 403, "提醒绑定已变更")
	}
	var oldHash string
	var oldReceipt []byte
	err := tx.QueryRowContext(ctx, `SELECT body_hash,receipt FROM manager_trigger_events WHERE trigger_id=$1 AND event_id=$2`, b.ID, q.EventID).Scan(&oldHash, &oldReceipt)
	if err == nil {
		if oldHash != q.BodyHash {
			return out, service.Fail("event_conflict", 409, "事件正文不一致")
		}
		if err = json.Unmarshal(oldReceipt, &out); err != nil {
			return out, err
		}
		out.Duplicate = true
		return out, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	targets, err := parseNoticeTargets(b.NotificationTargets)
	if err != nil {
		return out, err
	}
	if len(targets) == 0 {
		return out, service.Fail("invalid_notification_targets", 400, "固定提醒必须指定接收群")
	}
	raw, err := json.Marshal(q.Command)
	if err != nil {
		return out, err
	}
	receipt, err := json.Marshal(out)
	if err != nil {
		return out, err
	}
	eventID := service.NewID()
	_, err = tx.ExecContext(ctx, `INSERT INTO manager_trigger_events(id,trigger_id,workspace_id,agent_id,binding_version,event_id,body_hash,action,payload,occurred_at,status,receipt) VALUES($1,$2,$3,$4,$5,$6,$7,'send_message',$8,$9,'accepted',$10)`, eventID, b.ID, b.WorkspaceID, b.AgentID, b.Version, q.EventID, q.BodyHash, raw, q.OccurredAt, receipt)
	if err != nil {
		return out, err
	}
	text := q.Command.Description
	if !strings.HasPrefix(text, "[系统消息]") {
		text = "[系统消息]" + text
	}
	for _, target := range targets {
		snapshot, err := snapshotNoticeTarget(ctx, tx, target.ConversationID, b.CreatedBy, b.WorkspaceID, b.AgentID)
		if err != nil {
			return out, err
		}
		if snapshot == nil {
			continue
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO manager_trigger_notifications(id,reminder_event_id,conversation_id,trigger_id,trigger_version,target,text,result_status)VALUES($1,$2,$3,$4,$5,$6,$7,'scheduled_reminder')`, service.NewID(), eventID, target.ConversationID, b.ID, b.Version, snapshot, text)
		if err != nil {
			return out, err
		}
	}
	return out, nil
}
