package repository

import (
	"context"
	"database/sql"
	"errors"
)

// A first proactive notice cannot fabricate a private conversation. When the
// recipient later opens the trusted main DM, admit confirmed history under
// that same person's current permissions and use the actual sent timestamp.
func captureRecentHumanDMNotices(ctx context.Context, tx *sql.Tx, w inboxWork) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO manager_human_dm_context(source_id,conversation_id,principal_id,connection_generation,admission_epoch,text,is_system,created_at)
 SELECT d.id,c.id,p.id,d.connection_generation,a.admission_epoch,d.text,true,d.sent_at
 FROM manager_human_notices d JOIN manager_conversations c ON c.id=$1
 JOIN manager_channel_principals p ON p.id=$2
 JOIN platform_agents a ON a.id=c.agent_id JOIN platform_channel_bindings b ON b.id=c.channel_binding_id
 WHERE d.status='sent' AND d.sent_at BETWEEN now()-interval '24 hours' AND $3
 AND `+humanDMNoticeAudience+` ORDER BY d.sent_at DESC LIMIT 50 ON CONFLICT DO NOTHING`, w.Conversation, w.Principal, w.CreatedAt)
	return err
}

func captureHumanDMNoticeContext(ctx context.Context, tx *sql.Tx, id string) error {
	query := `INSERT INTO manager_human_dm_context(source_id,conversation_id,principal_id,connection_generation,admission_epoch,text,is_system,created_at)
 SELECT d.id,c.id,p.id,d.connection_generation,a.admission_epoch,d.text,true,d.sent_at
 FROM manager_human_notices d JOIN manager_conversations c ON c.channel_binding_id=d.binding_id
 JOIN manager_channel_principals p ON p.id=c.channel_created_by
 JOIN platform_agents a ON a.id=c.agent_id JOIN platform_channel_bindings b ON b.id=c.channel_binding_id
 WHERE d.id=$1 AND d.status='sent' AND ` + humanDMNoticeAudience + `
 ON CONFLICT DO NOTHING RETURNING conversation_id`
	var conversation string
	err := tx.QueryRowContext(ctx, query, id).Scan(&conversation)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return flushExistingHumanDMContext(ctx, tx, conversation)
}

// Recipient, current binding, and actual conversation must all agree.
const humanDMNoticeAudience = `d.text IS NOT NULL AND d.sent_at IS NOT NULL
 AND d.workspace_id=c.workspace_id AND d.agent_id=c.agent_id AND d.binding_id=c.channel_binding_id
 AND d.identity_epoch=c.channel_identity_epoch AND d.tenant_id=c.channel_tenant_id
 AND d.provider=b.provider AND d.config_revision=b.config_revision
 AND c.lifecycle='active' AND c.channel_chat_type='direct' AND c.channel_thread_id='' AND c.channel_created_by=p.id
 AND (c.external_chat_id=d.provider_chat_id OR (d.provider IN ('dingtalk','wecom') AND COALESCE(d.provider_chat_id,'')='' AND ` + uniqueHumanDM + `))
 AND p.external_user_id=d.recipient->>'external_id'
 AND manager_human_recipient_matches(d.workspace_id,d.agent_id,d.recipient,p.id,true)
 AND a.enabled AND manager_channel_actor_allowed(p.id,c.id,d.connection_generation)`

const uniqueHumanDM = `NOT EXISTS(SELECT 1 FROM manager_conversations sibling
 WHERE sibling.id<>c.id AND sibling.channel_binding_id=c.channel_binding_id AND sibling.channel_identity_epoch=c.channel_identity_epoch
 AND sibling.channel_tenant_id=c.channel_tenant_id AND sibling.channel_created_by=c.channel_created_by
 AND sibling.channel_chat_type='direct' AND sibling.channel_thread_id='' AND sibling.lifecycle='active')`

func isHumanMainDM(w inboxWork, kind, thread string, allowed bool) bool {
	return allowed && kind == "direct" && thread == "" && supportedChannelInput(w.Provider, w.MessageType) &&
		(w.Provider == "feishu" || w.Provider == "dingtalk" || w.Provider == "wecom")
}
