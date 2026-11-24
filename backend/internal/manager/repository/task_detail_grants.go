package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	platform "github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

const taskDetailGrantTTL = 72 * time.Hour
const taskDetailGrantFields = `g.id,g.workspace_id,g.task_id,g.session_id,COALESCE(s.conversation_id::text,''),
 COALESCE(g.source_chat_session_id::text,''),COALESCE(g.source_input_id::text,''),COALESCE(g.trigger_notification_id::text,''),
 COALESCE(i.channel_principal_id::text,i.submitted_by::text,''),COALESCE(i.channel_generation,0),
 g.scope,g.expires_at,g.destination_conversation_id,g.destination_channel,COALESCE(g.destination_binding_id::text,''),
 g.destination_generation,g.destination_chat_type,g.destination_chat_id,g.destination_topic_id`
const taskDetailGrantJoins = ` FROM manager_task_detail_grants g
 JOIN manager_sessions s ON s.id=g.session_id AND s.task_id=g.task_id AND s.workspace_id=g.workspace_id AND s.mode='task'
 JOIN manager_conversations c ON c.id=g.destination_conversation_id AND c.workspace_id=g.workspace_id AND c.lifecycle='active'
 LEFT JOIN manager_inputs i ON i.id=g.source_input_id AND i.session_id=g.source_chat_session_id
 LEFT JOIN platform_channel_bindings b ON b.id=g.destination_binding_id AND b.workspace_id=g.workspace_id AND b.id=c.channel_binding_id
 LEFT JOIN manager_trigger_notifications n ON n.id=g.trigger_notification_id AND n.task_id=g.task_id AND n.conversation_id=c.id
 WHERE g.revoked_at IS NULL AND g.expires_at>clock_timestamp()
 AND ((g.destination_channel='web' AND c.web_group_id IS NOT NULL
       AND EXISTS (SELECT 1 FROM platform_web_groups wg JOIN platform_web_group_agents ga ON ga.group_id=wg.id
                   WHERE wg.id=c.web_group_id AND ga.agent_id=c.agent_id AND wg.status='active' AND ga.status='active'
                     AND wg.admission_epoch=c.web_group_epoch AND ga.admission_epoch=c.web_group_agent_epoch))
   OR (b.id IS NOT NULL AND b.enabled AND b.agent_id=c.agent_id AND b.provider=g.destination_channel
       AND b.connection_generation=g.destination_generation AND b.identity_epoch=c.channel_identity_epoch
       AND b.external_tenant_id=c.channel_tenant_id AND b.external_tenant_id<>''
       AND (b.provider='feishu' OR b.identity_verified_generation=b.connection_generation)
       AND c.external_chat_id=g.destination_chat_id AND c.channel_thread_id=g.destination_topic_id
       AND c.channel_chat_type=CASE g.destination_chat_type WHEN 'private' THEN 'direct' ELSE 'group' END
       AND (c.channel_chat_type<>'group' OR manager_channel_group_allowed(b.id,b.identity_epoch,c.channel_tenant_id,c.external_chat_id,''))))
 AND ((n.id IS NOT NULL AND n.status='sent' AND n.confirmed_at IS NOT NULL
       AND n.target->>'provider'=g.destination_channel AND COALESCE(n.target->>'binding_id','')=COALESCE(g.destination_binding_id::text,'')
       AND COALESCE((n.target->>'generation')::bigint,0)=g.destination_generation
       AND COALESCE(n.target->>'chat_id','')=g.destination_chat_id AND COALESCE(n.target->>'thread_id','')=g.destination_topic_id)
   OR (g.trigger_notification_id IS NULL AND s.conversation_id=c.id AND i.id IS NOT NULL AND manager_input_actor_allowed(i.id)))`

func (s *Store) TaskDetailLinksEnabled(ctx context.Context) (bool, error) {
	origin, err := readTaskDetailLinkOrigin(ctx, s.DB, false)
	return origin != "", err
}

// TaskDetailLinkOriginTx is read inside issuance/delivery transactions so a
// concurrent disable cannot use a stale grant configuration.
func (s *Store) TaskDetailLinkOriginTx(ctx context.Context, tx *sql.Tx) (string, error) {
	return readTaskDetailLinkOrigin(ctx, tx, true)
}

type taskDetailQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readTaskDetailLinkOrigin(ctx context.Context, q taskDetailQueryer, lock bool) (string, error) {
	query := `SELECT origin FROM platform_task_link_settings WHERE singleton AND enabled`
	if lock {
		query += ` FOR SHARE`
	}
	var origin string
	if err := q.QueryRowContext(ctx, query).Scan(&origin); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	canonical, err := platform.NormalizeTaskLinkOrigin(origin)
	if err != nil || canonical != origin {
		return "", nil // Corrupt configuration must not become an outbound link.
	}
	return canonical, nil
}

func invalidTaskDetailGrant() error {
	return service.Fail("task_detail_link_unavailable", 404, "任务链接不可用或已过期")
}

func (s *Store) ResolveTaskDetailGrant(ctx context.Context, raw string) (service.TaskDetailGrant, error) {
	digest, ok := taskDetailTokenDigest(raw)
	if !ok {
		return service.TaskDetailGrant{}, invalidTaskDetailGrant()
	}
	return scanTaskDetailGrant(s.DB.QueryRowContext(ctx, `SELECT `+taskDetailGrantFields+taskDetailGrantJoins+` AND g.token_digest=$1`, digest[:]))
}

func taskDetailTokenDigest(raw string) ([32]byte, bool) {
	if len(raw) != base64.RawURLEncoding.EncodedLen(32) {
		return [32]byte{}, false
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil || len(decoded) != 32 {
		return [32]byte{}, false
	}
	return sha256.Sum256(decoded), true
}

func (s *Store) RecheckTaskDetailGrant(ctx context.Context, grantID string, write bool) (service.TaskDetailGrant, error) {
	return s.readTaskDetailGrant(ctx, s.DB, grantID, write)
}

func (s *Store) readTaskDetailGrant(ctx context.Context, q taskDetailQueryer, grantID string, write bool) (service.TaskDetailGrant, error) {
	if !service.ValidID(grantID) {
		return service.TaskDetailGrant{}, invalidTaskDetailGrant()
	}
	query := `SELECT ` + taskDetailGrantFields + taskDetailGrantJoins + ` AND g.id=$1`
	if write {
		query += ` FOR SHARE OF g`
	}
	g, err := scanTaskDetailGrant(q.QueryRowContext(ctx, query, grantID))
	if err == nil && write && (!g.CanWrite() || g.SubjectID == "" || g.Channel == nil) {
		return service.TaskDetailGrant{}, service.Fail("task_detail_link_read_only", 403, "此任务链接只可查看")
	}
	return g, err
}

func scanTaskDetailGrant(row *sql.Row) (service.TaskDetailGrant, error) {
	var g service.TaskDetailGrant
	d := &g.Destination
	err := row.Scan(&g.ID, &g.WorkspaceID, &g.TaskID, &g.SessionID, &g.ConversationID, &g.SourceChatSessionID, &g.SubjectInputID,
		&g.TriggerNotificationID, &g.SubjectID, &g.SubjectGeneration, &g.Scope, &g.ExpiresAt, &d.ConversationID, &d.Channel,
		&d.BindingID, &d.Generation, &d.ChatType, &d.ChatID, &d.TopicID)
	if errors.Is(err, sql.ErrNoRows) {
		return g, invalidTaskDetailGrant()
	}
	d.WorkspaceID = g.WorkspaceID
	if g.SubjectGeneration > 0 {
		g.Channel = &service.ChannelActor{ID: g.SubjectID, Generation: g.SubjectGeneration}
	}
	return g, err
}

// CheckTaskDetailGrant is called under the Session lock by bearer mutations.
// Channel policy is also revalidated by WithSession using the original actor.
func (t *sessionTx) CheckTaskDetailGrant(id string, write bool) (service.TaskDetailGrant, error) {
	g, err := t.store.readTaskDetailGrant(t.ctx, t.tx, id, write)
	if err != nil {
		return g, err
	}
	if g.SessionID != t.target.Session || g.WorkspaceID != t.target.Workspace || (write && g.SubjectID != t.target.Actor) {
		return service.TaskDetailGrant{}, invalidTaskDetailGrant()
	}
	return g, nil
}

// RevokeTaskDetailGrant is internal; an authenticated caller must establish
// authority for the grant's task before invoking this workspace-scoped write.
func (s *Store) RevokeTaskDetailGrant(ctx context.Context, workspaceID, grantID string) error {
	if !service.ValidID(workspaceID) || !service.ValidID(grantID) {
		return invalidTaskDetailGrant()
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE manager_task_detail_grants SET revoked_at=COALESCE(revoked_at,clock_timestamp()) WHERE workspace_id=$1 AND id=$2`, workspaceID, grantID)
	return err
}
