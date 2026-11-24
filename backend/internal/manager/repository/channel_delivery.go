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

func (s *Store) MaterializeChannelReplies(ctx context.Context) error {
	if err := s.materializeTaskDetailContexts(ctx); err != nil {
		return err
	}
	if err := s.materializeTaskReplies(ctx); err != nil {
		return err
	}
	// Start at the Turn's original Inbox. Choose the last user message admitted
	// into its native context before completion. An existing reply covers the
	// Turn, even if an older Backend delivered it to another Inbox.
	rows, e := s.DB.QueryContext(ctx, `SELECT COALESCE(reply.id,n.id),n.binding_id,n.connection_generation,COALESCE(reply.input_id,i.id),i.id,i.status,COALESCE(t.status,''),b.provider,s.id,COALESCE(t.id::text,'')
 FROM manager_channel_inbox n JOIN platform_channel_bindings b ON b.id=n.binding_id
 JOIN manager_inputs i ON i.id=n.input_id JOIN manager_sessions s ON s.id=i.session_id
 LEFT JOIN manager_turns t ON t.id=i.turn_id
 LEFT JOIN LATERAL (
   SELECT dest.id, dest.input_id, d.id AS delivery_id
   FROM manager_inputs target JOIN manager_channel_inbox dest ON dest.input_id=target.id
   LEFT JOIN manager_channel_deliveries d ON d.inbox_id=dest.id AND d.kind='reply'
   WHERE target.session_id=i.session_id AND target.turn_id=t.id AND target.seq>i.seq AND dest.status='accepted'
   AND dest.binding_id=n.binding_id AND dest.connection_generation=n.connection_generation
   AND dest.conversation_id=n.conversation_id AND target.channel_principal_id=i.channel_principal_id
   AND target.source_kind='user' AND target.trigger_agent_loop
   AND (d.id IS NOT NULL OR (target.context_state='committed'
     AND (target.context_committed_at IS NULL OR target.context_committed_at<=t.finished_at)))
   ORDER BY (d.id IS NOT NULL) DESC,target.seq DESC,dest.id LIMIT 1
 ) reply ON true
 WHERE n.status='accepted' AND s.mode='chat' AND i.source_kind<>'task_report' AND reply.delivery_id IS NULL
 AND ((i.turn_id IS NULL AND i.status IN ('rejected','canceled'))
   OR (i.id=t.input_id AND t.status IN ('completed','failed','canceled','interrupted')))
 AND NOT EXISTS(SELECT FROM manager_channel_deliveries d WHERE d.inbox_id=n.id AND d.kind='reply')
 ORDER BY n.created_at,n.id LIMIT 50`)
	if e != nil {
		return e
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Explicit closes report iteration/close failures.
	type item struct {
		id, binding, input, rootInput, status, turn, provider, sessionID, turnID string
		generation                                                               int64
	}
	items := []item{}
	for rows.Next() {
		var v item
		if e = rows.Scan(&v.id, &v.binding, &v.generation, &v.input, &v.rootInput, &v.status, &v.turn, &v.provider, &v.sessionID, &v.turnID); e != nil {
			return errors.Join(e, rows.Close())
		}
		items = append(items, v)
	}
	if e = rows.Err(); e != nil {
		return errors.Join(e, rows.Close())
	}
	if e = rows.Close(); e != nil {
		return e
	}
	for _, v := range items {
		evs, e := s.DB.QueryContext(ctx, `SELECT event FROM manager_session_events
 WHERE session_id=$1 AND event->>'type'='agent.message'
 AND (event->>'turn_id'=$2 OR (event->>'turn_id' IS NULL AND event->>'input_id'=$3)) ORDER BY seq`, v.sessionID, v.turnID, v.rootInput)
		if e != nil {
			return e
		}
		defer func() { _ = evs.Close() }() //nolint:errcheck // Each iteration explicitly closes before continuing.
		texts := []string{}
		for evs.Next() {
			var raw []byte
			if e = evs.Scan(&raw); e != nil {
				return errors.Join(e, evs.Close())
			}
			var event struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			}
			if e = json.Unmarshal(raw, &event); e != nil {
				return errors.Join(e, evs.Close())
			}
			for _, part := range event.Content {
				if part.Type == "text" && part.Text != "" {
					texts = append(texts, part.Text)
				}
			}
		}
		if e = evs.Err(); e != nil {
			return errors.Join(e, evs.Close())
		}
		if e = evs.Close(); e != nil {
			return e
		}
		text, format := channelReplyContent(v.provider, v.status, v.turn, texts)
		deliveryID := service.NewID()
		result, e := s.DB.ExecContext(ctx, `INSERT INTO manager_channel_deliveries(id,inbox_id,binding_id,connection_generation,kind,text,content_format) VALUES($1,$2,$3,$4,'reply',$5,$6) ON CONFLICT(inbox_id,kind) DO NOTHING`, deliveryID, v.id, v.binding, v.generation, text, format)
		if e != nil {
			return e
		}
		if n, e := result.RowsAffected(); e == nil && n > 0 {
			s.channelLog(ctx, "channel.outbox_persisted", "provider", v.provider, "binding_id", v.binding, "inbox_id", v.id, "input_id", v.input, "session_id", v.sessionID, "turn_id", v.turnID, "delivery_id", deliveryID, "kind", "reply", "status", "pending")
		}
	}
	return s.materializeArtifactReplies(ctx)
}

func channelReplyText(inputStatus, turnStatus string, texts []string) string {
	text, _ := channelReplyContent("", inputStatus, turnStatus, texts)
	return text
}

// A terminal failure must not present intermediate model prose as a final answer.
func channelReplyContent(provider, inputStatus, turnStatus string, texts []string) (string, string) {
	if inputStatus == "rejected" {
		return "当前权限或员工配置已变更，本条消息未执行。", "text"
	}
	if inputStatus == "canceled" || turnStatus == "canceled" {
		return "本次处理已取消，未完成。", "text"
	}
	if turnStatus == "failed" || turnStatus == "interrupted" {
		return "本次处理未成功完成，请稍后重试。", "text"
	}
	if text := strings.TrimSpace(strings.Join(texts, "\n")); text != "" {
		return text, channelReplyFormat(provider, turnStatus, text)
	}
	return "本次处理已结束，没有可发送的文字回复。", "text"
}

func channelReplyFormat(provider, turnStatus, text string) string {
	if slices.Contains(markdownReplyProviders(turnStatus, text), provider) {
		return "markdown"
	}
	return "text"
}

func markdownReplyProviders(turnStatus, text string) []string {
	if turnStatus != "completed" || text == "" || strings.Contains(text, "![") || strings.Contains(strings.ToLower(text), "<img") {
		return nil
	}
	return []string{"feishu", "dingtalk"}
}

const deliverySelect = `d.id,d.lease_token,d.binding_id,d.connection_generation,n.external_chat_id,n.provider_message_id,n.thread_id,d.text,d.content_format,CASE WHEN d.authorized_at IS NULL THEN (SELECT provider FROM platform_channel_bindings WHERE id=d.binding_id) ELSE d.provider END,n.external_tenant_id,n.chat_type,CASE WHEN n.chat_type='direct' THEN n.sender_id ELSE '' END,COALESCE(d.provider_request_id,''),COALESCE(d.attempt_target_digest,''),COALESCE(d.query_token::text,''),d.kind,COALESCE(d.artifact_id::text,''),
 COALESCE((SELECT x.title FROM manager_task_detail_deliveries x WHERE x.delivery_id=d.id),''),
 COALESCE((SELECT x.state FROM manager_task_detail_deliveries x WHERE x.delivery_id=d.id),''),
 COALESCE((SELECT x.presentation FROM manager_task_detail_deliveries x WHERE x.delivery_id=d.id),'')`

func scanDelivery(row scanner) (service.ChannelDelivery, error) {
	return scanDeliveryLease(row, false)
}

func scanDeliveryLease(row scanner, authorized bool) (service.ChannelDelivery, error) {
	var v service.ChannelDelivery
	var kind, cardTitle, cardState string
	e := row.Scan(&v.ID, &v.LeaseToken, &v.BindingID, &v.ConnectionGeneration, &v.ChatID, &v.ReplyToMessageID, &v.ThreadID, &v.Text, &v.Format, &v.Provider, &v.ExternalTenantID, &v.ChatType, &v.RecipientID, &v.ProviderRequestID, &v.TargetDigest, &v.QueryToken, &kind, &v.ArtifactID, &cardTitle, &cardState, &v.Presentation)
	if cardTitle != "" {
		v.TaskDetailCard = &service.TaskDetailCard{Title: cardTitle, State: cardState}
		if strings.HasPrefix(kind, "task_detail:body:") {
			v.TaskDetailCard.Description = v.Text
		}
	}
	// A private task result has its own authorized outbox operation. Address
	// the original user even if the interactive callback socket has expired.
	if v.Provider == "wecom" && v.ChatType == "direct" && v.ThreadID == "" && v.ChatID == v.RecipientID && (strings.HasPrefix(kind, "task_reply:") || strings.HasPrefix(kind, "task_detail:") || v.ArtifactID != "") {
		v.ReplyToMessageID = ""
	}
	// The inbound anchor already formed part of the destination contract.
	// Confirm explicit support only on an ordinary, authorized send so a
	// newer Hub cannot infer this mode from an older Backend's reply hint.
	v.SessionWebhookEligible = authorized && v.Provider == "dingtalk" && dingSessionReply(kind, v.Format, v.ArtifactID != "") && v.ReplyToMessageID != ""
	v.IdempotencyKey = v.ID
	return v, e
}

func dingSessionReply(kind, format string, artifact bool) bool {
	return kind == "reply" && !artifact && (format == "text" || format == "markdown")
}

// Each claim spends one attachment attempt, including a lease that expires
// before dispatch. Ordinary text deliveries retain their existing behavior.
const maxArtifactDeliveryAttempts = 3

func (s *Store) ClaimChannelDeliveries(ctx context.Context, limit int, supportsMarkdown bool, supportsMedia ...bool) ([]service.ChannelDelivery, error) {
	if limit < 1 || limit > 50 {
		return nil, service.Fail("invalid_argument", 400, "投递批量无效")
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Preserve operation error.
	if _, e = tx.ExecContext(ctx, `UPDATE manager_channel_deliveries SET status='unknown',error_code='send_lease_expired',updated_at=now() WHERE status='sending' AND lease_until<now()`); e != nil {
		return nil, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE manager_channel_deliveries
 SET status='failed',error_code='artifact_attempts_exhausted',lease_until=NULL,confirmed_at=now(),updated_at=now()
 WHERE artifact_id IS NOT NULL AND attempts >= $1
 AND (status='pending' OR (status='leased' AND lease_until<now()))`, maxArtifactDeliveryAttempts); e != nil {
		return nil, e
	}
	media := len(supportsMedia) > 0 && supportsMedia[0]
	taskDetail := len(supportsMedia) > 1 && supportsMedia[1]
	taskDetailBody := len(supportsMedia) > 2 && supportsMedia[2]
	rows, e := tx.QueryContext(ctx, `WITH claimed AS (
 SELECT d.id FROM manager_channel_deliveries d
 WHERE (d.content_format='text' OR ($2 AND d.content_format='markdown') OR ($3 AND d.artifact_id IS NOT NULL))
 AND (d.status='pending' OR (d.status='leased' AND d.lease_until<now()))
 AND (d.artifact_id IS NULL OR d.attempts<$4)
 AND ($5 OR d.kind NOT LIKE 'task_detail:%')
 AND ($6 OR d.kind NOT LIKE 'task_detail:body:%')
 AND (d.artifact_id IS NULL OR NOT EXISTS(
   SELECT FROM manager_artifact_selections current JOIN manager_artifact_selections earlier USING (chat_turn_id,inbox_id)
   JOIN manager_channel_deliveries prev ON prev.inbox_id=earlier.inbox_id AND prev.kind='artifact:'||earlier.chat_turn_id||':'||earlier.artifact_id
   WHERE current.inbox_id=d.inbox_id AND d.kind='artifact:'||current.chat_turn_id||':'||current.artifact_id
   AND earlier.position<current.position AND prev.status IN ('pending','leased','sending')))
 ORDER BY d.created_at,d.id FOR UPDATE OF d SKIP LOCKED LIMIT $1),
 updated AS (UPDATE manager_channel_deliveries d SET status='leased',lease_token=gen_random_uuid(),lease_until=now()+interval '30 seconds',attempts=attempts+1,updated_at=now() FROM claimed WHERE d.id=claimed.id RETURNING d.*)
 SELECT `+deliverySelect+` FROM updated d JOIN manager_channel_inbox n ON n.id=d.inbox_id ORDER BY d.created_at,d.id`, limit, supportsMarkdown, media, maxArtifactDeliveryAttempts, taskDetail, taskDetailBody)
	if e != nil {
		return nil, e
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Explicit close checked before commit.
	out := []service.ChannelDelivery{}
	for rows.Next() {
		v, e := scanDelivery(rows)
		if e != nil {
			return nil, errors.Join(e, rows.Close())
		}
		out = append(out, v)
	}
	if e = rows.Err(); e != nil {
		return nil, errors.Join(e, rows.Close())
	}
	if e = rows.Close(); e != nil {
		return nil, e
	}
	return out, tx.Commit()
}
func (s *Store) AuthorizeChannelDelivery(ctx context.Context, id, lease string) (service.ChannelDelivery, error) {
	var out service.ChannelDelivery
	if !service.ValidID(id) || !service.ValidID(lease) {
		return out, service.Fail("invalid_argument", 400, "投递身份无效")
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return out, e
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Preserve operation error.
	// Match lifecycle lock order: binding first, delivery second. This prevents
	// revocation (binding -> queued deliveries) racing authorization inversely.
	var binding string
	e = tx.QueryRowContext(ctx, `SELECT b.id FROM manager_channel_deliveries d JOIN platform_channel_bindings b ON b.id=d.binding_id WHERE d.id=$1 AND d.lease_token=$2 FOR SHARE OF b`, id, lease).Scan(&binding)
	if errors.Is(e, sql.ErrNoRows) {
		return out, service.Fail("lease_lost", 409, "投递租约已失效")
	}
	if e != nil {
		return out, e
	}
	var allowed bool
	// Fixed refusals may be sent to an authenticated inbound sender without
	// granting them access to a conversation. Model output always rechecks ACL.
	e = tx.QueryRowContext(ctx, `SELECT `+deliveryAllowedExpression+` FROM manager_channel_deliveries d JOIN manager_channel_inbox n ON n.id=d.inbox_id JOIN platform_channel_bindings b ON b.id=d.binding_id JOIN platform_agents a ON a.id=b.agent_id WHERE d.id=$1 AND d.lease_token=$2 AND d.status='leased' AND d.lease_until>now() FOR UPDATE OF d FOR SHARE OF b,a`, id, lease).Scan(&allowed)
	if errors.Is(e, sql.ErrNoRows) {
		return out, service.Fail("lease_lost", 409, "投递租约已失效")
	}
	if e != nil {
		return out, e
	}
	if !allowed {
		if _, e = tx.ExecContext(ctx, `UPDATE manager_channel_deliveries SET status='cancelled',error_code='access_revoked',updated_at=now() WHERE id=$1`, id); e != nil {
			return out, e
		}
		if e = tx.Commit(); e != nil {
			return out, e
		}
		return out, service.Fail("access_revoked", 409, "投递权限已撤销")
	}
	if _, e = tx.ExecContext(ctx, `UPDATE manager_channel_deliveries SET status='sending',provider=(SELECT provider FROM platform_channel_bindings WHERE id=binding_id),authorized_at=CASE WHEN (SELECT provider FROM platform_channel_bindings WHERE id=binding_id)='dingtalk' THEN clock_timestamp() ELSE now() END,lease_until=now()+CASE WHEN artifact_id IS NULL THEN interval '45 seconds' ELSE interval '3 minutes' END,updated_at=now() WHERE id=$1`, id); e != nil {
		return out, e
	}
	out, e = scanDeliveryLease(tx.QueryRowContext(ctx, `SELECT `+deliverySelect+` FROM manager_channel_deliveries d JOIN manager_channel_inbox n ON n.id=d.inbox_id WHERE d.id=$1`, id), true)
	if e != nil {
		return out, e
	}
	if e = s.populateTaskDetailCard(ctx, tx, &out, true); e != nil {
		var issue *service.Error
		if !errors.As(e, &issue) || issue.Status >= 500 {
			return service.ChannelDelivery{}, e
		}
		if _, cleanupErr := tx.ExecContext(ctx, `UPDATE manager_channel_deliveries SET status='cancelled',error_code=$2,updated_at=now() WHERE id=$1`, id, issue.Code); cleanupErr != nil {
			return service.ChannelDelivery{}, cleanupErr
		}
		if _, cleanupErr := tx.ExecContext(ctx, `UPDATE manager_task_detail_deliveries SET sealed_url=NULL WHERE delivery_id=$1`, id); cleanupErr != nil {
			return service.ChannelDelivery{}, cleanupErr
		}
		return service.ChannelDelivery{}, errors.Join(e, tx.Commit())
	}
	out.TargetDigest = deliveryTargetDigest(out)
	if _, e = tx.ExecContext(ctx, `UPDATE manager_channel_deliveries SET attempt_target_digest=$2 WHERE id=$1`, id, out.TargetDigest); e != nil {
		return out, e
	}
	out.Allowed = true
	return out, tx.Commit()
}

const deliveryAllowedExpression = `b.enabled AND b.connection_generation=d.connection_generation AND (b.external_tenant_id<>'' AND b.external_tenant_id=n.external_tenant_id) AND (n.chat_type='direct' OR (manager_channel_group_allowed(n.binding_id,n.identity_epoch,n.external_tenant_id,n.external_chat_id,n.sender_id) AND NOT EXISTS(SELECT 1 FROM manager_channel_groups g WHERE g.binding_id=n.binding_id AND g.identity_epoch=n.identity_epoch AND g.external_tenant_id=n.external_tenant_id AND g.external_chat_id=n.external_chat_id AND g.watermark_ms>n.trigger_created_at_ms) AND NOT EXISTS(SELECT 1 FROM manager_channel_group_members gm WHERE gm.binding_id=n.binding_id AND gm.identity_epoch=n.identity_epoch AND gm.external_tenant_id=n.external_tenant_id AND gm.external_chat_id=n.external_chat_id AND gm.member_id=n.sender_id AND gm.watermark_ms>n.trigger_created_at_ms)))
 AND CASE d.kind WHEN 'fixed' THEN true
 ELSE a.enabled AND (manager_channel_actor_allowed(n.principal_id,n.conversation_id,d.connection_generation)
 OR (d.kind='reply' AND manager_human_chat_input_allowed(n.input_id))) END`
