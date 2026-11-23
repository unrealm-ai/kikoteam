package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

// Claim, authorization and receipt queries use the same immutable body and format.
const noticeDeliverySelect = `n.id,COALESCE(n.lease_token::text,''),n.target->>'binding_id',
    (n.target->>'generation')::bigint,n.target->>'chat_id','',n.target->>'thread_id',n.text,n.content_format,
    n.target->>'provider',n.target->>'tenant_id','group','',COALESCE(n.provider_request_id,''),
    COALESCE(n.attempt_target_digest,''),COALESCE(n.query_token::text,''),'trigger_notification' AS kind,'' AS artifact_id`

func noticeBatchValid(limit int) error {
	if limit < 1 || limit > 50 {
		return service.Fail("invalid_argument", 400, "批量无效")
	}
	return nil
}
func scanNoticeRows(rows *sql.Rows) ([]service.ChannelDelivery, error) {
	out := []service.ChannelDelivery{}
	for rows.Next() {
		v, e := scanDelivery(rows)
		if e != nil {
			return nil, errors.Join(e, rows.Close())
		}
		out = append(out, v)
	}
	return out, errors.Join(rows.Err(), rows.Close())
}
func (s *Store) ClaimTriggerNotifications(ctx context.Context, limit int) ([]service.ChannelDelivery, error) {
	if err := noticeBatchValid(limit); err != nil {
		return nil, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	_, err = tx.ExecContext(ctx, `UPDATE manager_trigger_notifications SET status='unknown',error_code='send_outcome_unknown',updated_at=now()WHERE status='sending' AND lease_until<now()`)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `WITH pick AS(SELECT id FROM manager_trigger_notifications WHERE target->>'provider'<>'web' AND (status='pending' OR (status='leased' AND lease_until<now())) ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT $1), upd AS(UPDATE manager_trigger_notifications n SET status='leased',lease_token=gen_random_uuid(),lease_until=now()+interval '30 seconds',updated_at=now()FROM pick WHERE n.id=pick.id RETURNING n.*) SELECT `+noticeDeliverySelect+` FROM upd n`, limit)
	if err != nil {
		return nil, err
	}
	out, err := scanNoticeRows(rows)
	if err != nil {
		return nil, err
	}
	return out, tx.Commit()
}
func (s *Store) AuthorizeTriggerNotification(ctx context.Context, id, lease string) (service.ChannelDelivery, error) {
	var out service.ChannelDelivery
	if !service.ValidID(id) || !service.ValidID(lease) {
		return out, service.Fail("invalid_argument", 400, "投递身份无效")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	var binding string
	err = tx.QueryRowContext(ctx, `SELECT target->>'binding_id' FROM manager_trigger_notifications WHERE id=$1 AND status='leased' AND lease_token=$2 AND lease_until>now()FOR UPDATE`, id, lease).Scan(&binding)
	if errors.Is(err, sql.ErrNoRows) {
		return out, service.Fail("lease_lost", 409, "投递租约已失效")
	}
	if err != nil {
		return out, err
	}
	var locked string
	if err = tx.QueryRowContext(ctx, `SELECT id FROM platform_channel_bindings WHERE id=$1 FOR SHARE`, binding).Scan(&locked); err != nil {
		return out, err
	}
	allowed, err := noticeAllowed(ctx, tx, id)
	if err != nil {
		return out, err
	}
	if !allowed {
		_, err = tx.ExecContext(ctx, `UPDATE manager_trigger_notifications SET status='cancelled',error_code='access_revoked',context_status='suppressed',updated_at=now() WHERE id=$1`, id)
		if err != nil {
			return out, err
		}
		if err = tx.Commit(); err != nil {
			return out, err
		}
		return out, service.Fail("access_revoked", 409, "通知目标权限已撤销")
	}
	out, err = scanDelivery(tx.QueryRowContext(ctx, `SELECT `+noticeDeliverySelect+` FROM manager_trigger_notifications n WHERE id=$1`, id))
	if err != nil {
		return out, err
	}
	out.Allowed = true
	out.TargetDigest = deliveryTargetDigest(out)
	_, err = tx.ExecContext(ctx, `UPDATE manager_trigger_notifications SET status='sending',authorized_at=CASE WHEN target->>'provider'='dingtalk' THEN clock_timestamp() ELSE now() END,lease_until=now()+interval '45 seconds',attempt_target_digest=$2,updated_at=now()WHERE id=$1`, id, out.TargetDigest)
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}
func (s *Store) CompleteTriggerNotification(ctx context.Context, id string, v service.DeliveryCompletion) error {
	if !service.ValidID(id) {
		return service.Fail("invalid_argument", 400, "通知标识无效")
	}
	if err := validateDeliveryReceipt(v); err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	var state, provider, lease, digest, request, query, message string
	err = tx.QueryRowContext(ctx, `SELECT status,target->>'provider',COALESCE(lease_token::text,''),COALESCE(attempt_target_digest,''),COALESCE(provider_request_id,''),COALESCE(query_token::text,''),COALESCE(provider_message_id,'') FROM manager_trigger_notifications WHERE id=$1 FOR UPDATE`, id).Scan(&state, &provider, &lease, &digest, &request, &query, &message)
	if err != nil {
		return visibleError(err)
	}
	if lease != v.LeaseToken || digest == "" || digest != v.TargetDigest || state == "pending" || state == "leased" || state == "cancelled" {
		return service.Fail("receipt_mismatch", 409, "回执与尝试不符")
	}
	if !deliveryEvidence(provider, v) {
		return service.Fail("receipt_evidence_required", 400, "缺少供应商证据")
	}
	if request != "" && v.ProviderRequestID != "" && request != v.ProviderRequestID {
		return service.Fail("receipt_mismatch", 409, "请求身份不符")
	}
	if message != "" && v.ProviderMessageID != "" && message != v.ProviderMessageID {
		return service.Fail("receipt_mismatch", 409, "供应商消息身份不符")
	}
	if v.QueryToken != "" && (query != v.QueryToken || request == "") {
		return service.Fail("query_lease_lost", 409, "查询租约失效")
	}
	if err = validateNoticeAnchor(ctx, tx, id, provider, v); err != nil {
		return err
	}
	allowed, err := noticeAllowed(ctx, tx, id)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	_, err = tx.ExecContext(ctx, `INSERT INTO manager_trigger_notification_receipts(notification_id,attempt_token,receipt_digest,receipt,authorization_current)VALUES($1,$2,$3,$4,$5)ON CONFLICT DO NOTHING`, id, v.LeaseToken, hex.EncodeToString(sum[:]), raw, allowed)
	if err != nil {
		return err
	}
	if v.Status == "sent" && v.ProviderCreatedAtMS > 0 {
		_, err = tx.ExecContext(ctx, `UPDATE manager_trigger_notifications SET provider_created_at_ms=$2,provider_chat_id=$3,provider_thread_id=$4 WHERE id=$1 AND (provider_created_at_ms IS NULL OR provider_created_at_ms=$2)`, id, v.ProviderCreatedAtMS, v.ProviderChatID, v.ProviderThreadID)
		if err != nil {
			return err
		}
	}
	if state == "sent" || state == "failed" {
		return tx.Commit()
	}
	if v.Status == "unknown" && state == "accepted" {
		v.Status = "accepted"
	}
	_, err = tx.ExecContext(ctx, `UPDATE manager_trigger_notifications SET status=$2,provider_message_id=COALESCE(NULLIF($3,''),provider_message_id),provider_request_id=COALESCE(NULLIF($4,''),provider_request_id),provider_session_id=COALESCE(NULLIF($5,''),provider_session_id),error_code=NULLIF($6,''),confirmed_at=CASE WHEN $2 IN ('sent','failed')THEN now()ELSE confirmed_at END,next_query_at=CASE WHEN $2 IN ('accepted','unknown')AND COALESCE(NULLIF($4,''),provider_request_id)IS NOT NULL THEN now()+make_interval(secs=>CASE query_attempts WHEN 0 THEN 2 WHEN 1 THEN 5 WHEN 2 THEN 10 WHEN 3 THEN 20 ELSE 30 END)ELSE NULL END,query_until=NULL,updated_at=now()WHERE id=$1`, id, v.Status, v.ProviderMessageID, v.ProviderRequestID, v.ProviderSessionID, v.ErrorCode)
	if err != nil {
		return err
	}
	if err := recordDingProactiveSend(ctx, tx, provider, dingTriggerAttempt, id, v); err != nil {
		return err
	}
	err = tx.Commit()
	if err == nil {
		s.channelLog(ctx, "channel.notification_receipt_persisted", "provider", provider, "notification_id", id, "previous_status", state, "status", v.Status, "receipt_kind", v.EvidenceType)
	}
	return err
}
func (s *Store) ClaimTriggerNotificationQueries(ctx context.Context, limit int) ([]service.ChannelDelivery, error) {
	if err := noticeBatchValid(limit); err != nil {
		return nil, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	_, err = tx.ExecContext(ctx, `UPDATE manager_trigger_notifications SET status='unknown',next_query_at=NULL,error_code='confirmation_window_exhausted'WHERE status='accepted'AND(query_attempts>=5 OR authorized_at<now()-interval '5 minutes')`)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `WITH pick AS(SELECT id FROM manager_trigger_notifications WHERE target->>'provider'='dingtalk' AND status IN ('accepted','unknown')AND provider_request_id IS NOT NULL AND attempt_target_digest IS NOT NULL AND query_attempts<5 AND authorized_at>=now()-interval '5 minutes'AND(query_until IS NULL OR query_until<now())AND next_query_at<=now()ORDER BY next_query_at,id FOR UPDATE SKIP LOCKED LIMIT $1),upd AS(UPDATE manager_trigger_notifications n SET query_token=COALESCE(query_token,gen_random_uuid()),query_until=now()+interval '30 seconds',next_query_at=now()+interval '30 seconds',query_attempts=query_attempts+1 FROM pick WHERE n.id=pick.id RETURNING n.*)SELECT `+noticeDeliverySelect+` FROM upd n`, limit)
	if err != nil {
		return nil, err
	}
	out, err := scanNoticeRows(rows)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Text = ""
	}
	return out, tx.Commit()
}

func validateNoticeAnchor(ctx context.Context, tx *sql.Tx, id, provider string, v service.DeliveryCompletion) error {
	if v.ProviderCreatedAtMS != 0 || v.ProviderChatID != "" || v.ProviderThreadID != "" {
		var chat, thread string
		if err := tx.QueryRowContext(ctx, `SELECT target->>'chat_id',target->>'thread_id' FROM manager_trigger_notifications WHERE id=$1`, id).Scan(&chat, &thread); err != nil {
			return err
		}
		if provider != "feishu" || v.Status != "sent" || v.ProviderCreatedAtMS <= 0 || v.ProviderChatID != chat || v.ProviderThreadID != thread {
			return service.Fail("receipt_anchor_mismatch", 400, "消息锚点与目标不符")
		}
	}
	return nil
}
