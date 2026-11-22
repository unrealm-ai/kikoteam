package repository

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"reflect"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

// The snapshot is composed only from currently verified database identity.
const noticeTargetSQL = `SELECT jsonb_build_object('conversation_id',c.id,'workspace_id',c.workspace_id,'agent_id',c.agent_id,'actor_id',$2::text,
 'provider',CASE WHEN c.web_group_id IS NOT NULL THEN 'web' ELSE b.provider END,
 'binding_id',COALESCE(b.id::text,''),'generation',COALESCE(b.connection_generation,0),
 'identity_epoch',COALESCE(b.identity_epoch,0),'tenant_id',COALESCE(c.channel_tenant_id,''),
 'chat_id',COALESCE(c.external_chat_id,''),'thread_id',COALESCE(c.channel_thread_id,''),
 'group_watermark',COALESCE((SELECT g.watermark_ms FROM manager_channel_groups g WHERE g.binding_id=b.id AND g.identity_epoch=b.identity_epoch AND g.external_tenant_id=c.channel_tenant_id AND g.external_chat_id=c.external_chat_id),0),
 'web_group_id',COALESCE(c.web_group_id::text,''),'group_epoch',COALESCE(c.web_group_epoch,0),'agent_epoch',COALESCE(c.web_group_agent_epoch,0))
 FROM manager_conversations c JOIN platform_agents a ON a.id=c.agent_id
 LEFT JOIN platform_channel_bindings b ON b.id=c.channel_binding_id
 WHERE c.id=$1 AND c.lifecycle='active' AND a.enabled
 AND EXISTS(SELECT 1 FROM platform_members m WHERE m.workspace_id=c.workspace_id AND m.user_id=$2::uuid AND m.status='active' AND m.role IN ('owner','admin'))
 AND ((c.web_group_id IS NOT NULL AND EXISTS(
 SELECT 1 FROM platform_web_groups g JOIN platform_web_group_agents ga ON ga.group_id=g.id WHERE g.id=c.web_group_id AND g.status='active' AND ga.agent_id=c.agent_id AND ga.status='active' AND g.admission_epoch=c.web_group_epoch AND ga.admission_epoch=c.web_group_agent_epoch))
 OR (c.channel_chat_type='group' AND b.enabled AND b.agent_id=c.agent_id AND b.identity_epoch=c.channel_identity_epoch AND b.external_tenant_id=c.channel_tenant_id
 AND b.external_tenant_id<>'' AND (b.provider='feishu' OR b.identity_verified_generation=b.connection_generation) AND c.channel_thread_id=''
 AND manager_channel_group_allowed(b.id,b.identity_epoch,c.channel_tenant_id,c.external_chat_id,'')))`

func parseNoticeTargets(raw json.RawMessage) ([]service.TriggerNotificationTarget, error) {
	out := []service.TriggerNotificationTarget{}
	if len(raw) == 0 || string(raw) == "null" {
		return out, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return nil, service.Fail("invalid_notification_targets", 400, "通知群目标无效")
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, service.Fail("invalid_notification_targets", 400, "通知目标格式无效")
	}
	if len(out) > 50 {
		return nil, service.Fail("invalid_notification_targets", 400, "通知目标过多")
	}
	seen := map[string]bool{}
	for _, v := range out {
		if !service.ValidID(v.ConversationID) || seen[v.ConversationID] {
			return nil, service.Fail("invalid_notification_targets", 400, "通知目标无效或重复")
		}
		seen[v.ConversationID] = true
	}
	return out, nil
}
func ValidateTriggerTargetsTx(ctx context.Context, tx *sql.Tx, w, actor, agent string, raw json.RawMessage) error {
	values, err := parseNoticeTargets(raw)
	if err != nil {
		return err
	}
	for _, v := range values {
		var data []byte
		if err = tx.QueryRowContext(ctx, noticeTargetSQL+` AND c.workspace_id=$3 AND c.agent_id=$4`, v.ConversationID, actor, w, agent).Scan(&data); err != nil {
			return visibleError(err)
		}
	}
	return nil
}
func SnapshotTriggerNotificationsTx(ctx context.Context, tx *sql.Tx, triggerID, taskID string, version int64, raw json.RawMessage) error {
	values, err := parseNoticeTargets(raw)
	if err != nil {
		return err
	}
	var w, actor, agent string
	if err = tx.QueryRowContext(ctx, `SELECT workspace_id,created_by,agent_id FROM manager_triggers WHERE id=$1 AND version=$2 AND enabled`, triggerID, version).Scan(&w, &actor, &agent); err != nil {
		return visibleError(err)
	}
	for _, v := range values {
		data, err := snapshotNoticeTarget(ctx, tx, v.ConversationID, actor, w, agent)
		if err != nil {
			return err
		}
		if data == nil {
			continue
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO manager_trigger_notification_targets(task_id,trigger_id,trigger_version,conversation_id,target)VALUES($1,$2,$3,$4,$5)ON CONFLICT DO NOTHING`, taskID, triggerID, version, v.ConversationID, data); err != nil {
			return err
		}
	}
	return nil
}

func snapshotNoticeTarget(ctx context.Context, tx *sql.Tx, conversation, actor, workspace, agent string) ([]byte, error) {
	var snapshot []byte
	err := tx.QueryRowContext(ctx, noticeTargetSQL+` AND c.workspace_id=$3 AND c.agent_id=$4`, conversation, actor, workspace, agent).Scan(&snapshot)
	if !errors.Is(err, sql.ErrNoRows) {
		return snapshot, visibleError(err)
	}
	var replaced bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM manager_conversations c
 JOIN platform_channel_bindings b ON b.id=c.channel_binding_id AND b.workspace_id=c.workspace_id AND b.agent_id=c.agent_id
 WHERE c.id=$1 AND c.workspace_id=$2 AND c.agent_id=$3 AND c.channel_identity_epoch<b.identity_epoch)`, conversation, workspace, agent).Scan(&replaced); err != nil {
		return nil, err
	}
	if replaced {
		return nil, nil
	}
	return nil, visibleError(err)
}
func noticeAllowed(ctx context.Context, tx *sql.Tx, id string) (bool, error) {
	var snapshot, current []byte
	var conv, actor string
	var valid bool
	err := tx.QueryRowContext(ctx, `SELECT n.target,n.conversation_id,n.target->>'actor_id',t.enabled AND t.version=n.trigger_version FROM manager_trigger_notifications n JOIN manager_triggers t ON t.id=n.trigger_id WHERE n.id=$1`, id).Scan(&snapshot, &conv, &actor, &valid)
	if err != nil {
		return false, err
	}
	if !valid {
		return false, nil
	}
	err = tx.QueryRowContext(ctx, noticeTargetSQL, conv, actor).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var a, b any
	left := json.NewDecoder(bytes.NewReader(snapshot))
	left.UseNumber()
	if err = left.Decode(&a); err != nil {
		return false, err
	}
	right := json.NewDecoder(bytes.NewReader(current))
	right.UseNumber()
	if err = right.Decode(&b); err != nil {
		return false, err
	}
	return reflect.DeepEqual(a, b), nil
}
