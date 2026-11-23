package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func deliveryTargetDigest(d service.ChannelDelivery) string {
	// An opaque digest binds one authorization attempt to its exact immutable
	// destination, identity, operation and content. It is never inferred from text.
	raw := strings.Join([]string{d.ID, d.LeaseToken, d.BindingID, strconv.FormatInt(d.ConnectionGeneration, 10), d.Provider, d.ExternalTenantID, d.ChatType, d.ChatID, d.RecipientID, d.ThreadID, d.ReplyToMessageID, d.IdempotencyKey, d.Text}, "\x00")
	if d.ArtifactID != "" {
		raw += "\x00artifact:" + d.ArtifactID
	}
	if d.ConfigRevision > 0 {
		raw += "\x00revision:" + strconv.FormatInt(d.ConfigRevision, 10)
	}
	if d.Format != "" && d.Format != "text" {
		raw += "\x00" + d.Format
	}
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
func validateDeliveryReceipt(v service.DeliveryCompletion) error {
	if !service.ValidID(v.LeaseToken) || len(v.TargetDigest) != 64 {
		return service.Fail("invalid_argument", 400, "投递回执身份无效")
	}
	if _, e := hex.DecodeString(v.TargetDigest); e != nil {
		return service.Fail("invalid_argument", 400, "投递回执身份无效")
	}
	switch v.Status {
	case "sent", "accepted", "failed", "unknown":
	default:
		return service.Fail("invalid_argument", 400, "投递回执状态无效")
	}
	for _, s := range []string{v.ProviderMessageID, v.ProviderSessionID, v.ErrorCode, v.EvidenceType} {
		if len(s) > 512 || strings.ContainsAny(s, "\x00\r\n") {
			return service.Fail("invalid_argument", 400, "投递回执无效")
		}
	}
	for _, s := range []string{v.ProviderRequestID, v.EvidenceID} {
		if len(s) > 2048 || strings.ContainsAny(s, "\x00\r\n") {
			return service.Fail("invalid_argument", 400, "投递回执无效")
		}
	}
	if v.QueryToken != "" && !service.ValidID(v.QueryToken) {
		return service.Fail("invalid_argument", 400, "查询回执无效")
	}
	return nil
}
func deliveryEvidence(provider string, v service.DeliveryCompletion) bool {
	if v.EvidenceType == "session_webhook" {
		return false // Requires the source Inbox and ordinary send lease, not a proactive ledger.
	}
	switch v.Status {
	case "sent":
		switch provider {
		case "feishu":
			return v.ProviderMessageID != "" && v.EvidenceType == "send_response" && v.EvidenceID == v.ProviderMessageID
		case "dingtalk":
			return v.ProviderRequestID != "" && v.QueryToken != "" && v.EvidenceType == "status_query" && v.EvidenceID == v.ProviderRequestID
		case "wecom":
			return v.ProviderRequestID != "" && v.ProviderSessionID != "" && v.EvidenceType == "business_ack" && v.EvidenceID == v.ProviderRequestID
		}
	case "accepted":
		return provider == "dingtalk" && v.ProviderRequestID != "" && v.EvidenceID == v.ProviderRequestID && (v.EvidenceType == "send_response" || v.EvidenceType == "status_query" && v.QueryToken != "")
	case "failed":
		return v.ErrorCode != "" && (v.EvidenceType == "send_response" || v.EvidenceType == "not_dispatched" || v.EvidenceType == "business_ack" && v.ProviderSessionID != "" || v.EvidenceType == "status_query" && v.QueryToken != "")
	case "unknown":
		return true
	}
	return false
}
func validDingSessionReplyEvidence(v service.DeliveryCompletion, original string) bool {
	return v.Status == "sent" && original != "" && v.EvidenceID == original &&
		v.ProviderMessageID == "" && v.ProviderRequestID == "" && v.ProviderSessionID == "" &&
		v.ProviderChatID == "" && v.ProviderThreadID == "" && v.ProviderCreatedAtMS == 0 && v.QueryToken == "" && v.ErrorCode == ""
}

func sessionWebhookReceipt(ctx context.Context, tx *sql.Tx, id, provider, kind, format, original, state, request, query string, artifact, leaseCurrent bool, v service.DeliveryCompletion) (replay bool, err error) {
	if provider != "dingtalk" || !dingSessionReply(kind, format, artifact) || !validDingSessionReplyEvidence(v, original) {
		return false, service.Fail("receipt_evidence_required", 400, "缺少可信供应商证据")
	}
	if state == "sending" && leaseCurrent && request == "" && query == "" {
		return false, nil
	}
	// Hub may lose the response to a committed receipt. Only recognize an
	// exact retry; never turn an unknown or active query into sent.
	if state == "sent" {
		if replayed, err := hasDeliveryReceipt(ctx, tx, id, v); replayed || err != nil {
			return replayed, err
		}
	}
	return false, service.Fail("receipt_mismatch", 409, "回执与发送尝试不符")
}

func (s *Store) CompleteChannelDelivery(ctx context.Context, id string, v service.DeliveryCompletion) error {
	if !service.ValidID(id) {
		return service.Fail("invalid_argument", 400, "投递身份无效")
	}
	if e := validateDeliveryReceipt(v); e != nil {
		return e
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	var state, provider, lease, digest, request, query, binding, inbox, chat, thread, kind, format, original string
	var artifact, leaseCurrent bool
	var attempts int
	e = tx.QueryRowContext(ctx, `SELECT d.status,d.provider,COALESCE(d.lease_token::text,''),COALESCE(d.attempt_target_digest,''),
 COALESCE(d.provider_request_id,''),COALESCE(d.query_token::text,''),d.binding_id,d.inbox_id,n.external_chat_id,n.thread_id,d.kind,d.content_format,n.provider_message_id,d.artifact_id IS NOT NULL,d.attempts,COALESCE(d.lease_until>now(),false)
 FROM manager_channel_deliveries d JOIN manager_channel_inbox n ON n.id=d.inbox_id
 WHERE d.id=$1 FOR UPDATE OF d`, id).Scan(&state, &provider, &lease, &digest, &request, &query, &binding, &inbox, &chat, &thread, &kind, &format, &original, &artifact, &attempts, &leaseCurrent)
	if errors.Is(e, sql.ErrNoRows) {
		return service.Fail("delivery_not_found", 404, "投递不存在")
	}
	if e != nil {
		return e
	}
	if !matchesDeliveryAttempt(state, lease, digest, v) {
		return acceptArtifactReceiptReplay(ctx, tx, id, artifact, v)
	}
	if v.EvidenceType == "session_webhook" {
		replay, e := sessionWebhookReceipt(ctx, tx, id, provider, kind, format, original, state, request, query, artifact, leaseCurrent, v)
		if e != nil {
			return e
		}
		if replay {
			return tx.Commit()
		}
	} else if !deliveryEvidence(provider, v) {
		return service.Fail("receipt_evidence_required", 400, "缺少可信供应商证据")
	}
	if request != "" && v.ProviderRequestID != "" && request != v.ProviderRequestID {
		return service.Fail("receipt_mismatch", 409, "供应商请求标识不符")
	}
	// A query token is for read-only reconciliation, never a new send lease.
	if v.QueryToken != "" && v.QueryToken != query {
		return service.Fail("query_lease_lost", 409, "查询回执已失效")
	}
	if v.QueryToken != "" && request == "" {
		return service.Fail("receipt_mismatch", 409, "未登记供应商请求")
	}
	if e = recordDeliveryReceipt(ctx, tx, id, v); e != nil {
		return e
	}
	if state == "sent" || state == "failed" {
		return tx.Commit()
	} // Monotonic transport facts; stale results never downgrade them.
	if shouldRetryArtifactDelivery(artifact, state, request, attempts, v) {
		if e = requeueArtifactDelivery(ctx, tx, id, v.ErrorCode); e == nil {
			s.channelLog(ctx, "channel.outbox_artifact_retry_scheduled", "provider", provider, "binding_id", binding, "inbox_id", inbox, "delivery_id", id, "attempts", attempts, "error_code", v.ErrorCode)
		}
		return e
	}
	if provider == "feishu" && thread != "" {
		v = feishuTopicOutcome(v, chat, thread)
	}
	if v.Status == "unknown" && state == "accepted" {
		v.Status = "accepted"
	} // A failed query does not erase provider acceptance.
	_, e = tx.ExecContext(ctx, `UPDATE manager_channel_deliveries SET status=$2,provider_message_id=COALESCE(NULLIF($3,''),provider_message_id),provider_request_id=COALESCE(NULLIF($4,''),provider_request_id),error_code=NULLIF($5,''),accepted_at=CASE WHEN $2='accepted' THEN COALESCE(accepted_at,now()) ELSE accepted_at END,confirmed_at=CASE WHEN $2 IN ('sent','failed') THEN now() ELSE confirmed_at END,next_query_at=CASE WHEN $2 IN ('accepted','unknown') AND COALESCE(NULLIF($4,''),provider_request_id) IS NOT NULL THEN now()+make_interval(secs => CASE query_attempts WHEN 0 THEN 2 WHEN 1 THEN 5 WHEN 2 THEN 10 WHEN 3 THEN 20 ELSE 30 END) ELSE NULL END,query_until=NULL,updated_at=now() WHERE id=$1`, id, v.Status, v.ProviderMessageID, v.ProviderRequestID, v.ErrorCode)
	if e != nil {
		return e
	}
	if v.Status == "sent" && kind == "human_confirmation" {
		if e = captureLegacyHumanReplyContext(ctx, tx, id); e != nil {
			return e
		}
	}
	e = tx.Commit()
	if e == nil {
		s.channelLog(ctx, "channel.outbox_receipt_persisted", "provider", provider, "binding_id", binding, "inbox_id", inbox, "delivery_id", id, "previous_status", state, "status", v.Status, "receipt_kind", v.EvidenceType)
	}
	return e
}

func matchesDeliveryAttempt(state, lease, digest string, v service.DeliveryCompletion) bool {
	return lease == v.LeaseToken && digest != "" && digest == v.TargetDigest &&
		state != "pending" && state != "leased" && state != "cancelled"
}

func acceptArtifactReceiptReplay(ctx context.Context, tx *sql.Tx, id string, artifact bool, v service.DeliveryCompletion) error {
	// A successful POST can lose its response. If it requeued an attachment,
	// acknowledge exact replays without changing the newer attempt.
	if artifact && retryableArtifactFailure(v) {
		replayed, err := hasDeliveryReceipt(ctx, tx, id, v)
		if err != nil {
			return err
		}
		if replayed {
			return tx.Commit()
		}
	}
	return service.Fail("receipt_mismatch", 409, "回执与发送尝试不符")
}

func shouldRetryArtifactDelivery(artifact bool, state, request string, attempts int, v service.DeliveryCompletion) bool {
	return artifact && state == "sending" && request == "" && attempts < maxArtifactDeliveryAttempts && retryableArtifactFailure(v)
}

// Only known negative outcomes can trigger another attempt. Do not resend
// an ambiguous provider operation or one already accepted for reconciliation.
func retryableArtifactFailure(v service.DeliveryCompletion) bool {
	if v.Status != "failed" || v.QueryToken != "" || v.ProviderRequestID != "" {
		return false
	}
	switch v.EvidenceType {
	case "not_dispatched":
		switch v.ErrorCode {
		case "artifact_unavailable", "media_upload_failed", "binding_unavailable_before_send", "cancelled_before_send",
			"send_cancelled", "dingtalk_authentication_failed", "thread_reply_source_unavailable", "wecom_session_unavailable", "wecom_send_busy":
			return true
		}
	case "send_response":
		return v.ErrorCode == "provider_rejected" || v.ErrorCode == "dingtalk_recipient_rejected"
	}
	return false
}

func hasDeliveryReceipt(ctx context.Context, tx *sql.Tx, id string, v service.DeliveryCompletion) (bool, error) {
	_, digest, err := deliveryReceiptBytes(v)
	if err != nil {
		return false, err
	}
	var exists bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT FROM manager_channel_delivery_receipts
 WHERE delivery_id=$1 AND attempt_token=$2 AND receipt_digest=$3)`, id, v.LeaseToken, digest).Scan(&exists)
	return exists, err
}

func requeueArtifactDelivery(ctx context.Context, tx *sql.Tx, id, errorCode string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE manager_channel_deliveries
 SET status='pending',lease_until=NULL,error_code=$2,updated_at=now() WHERE id=$1`, id, errorCode); err != nil {
		return err
	}
	return tx.Commit()
}

func feishuTopicOutcome(v service.DeliveryCompletion, chat, thread string) service.DeliveryCompletion {
	if v.Status == "sent" && (v.ProviderChatID == "" || v.ProviderChatID != chat || v.ProviderThreadID != thread) {
		v.Status, v.ErrorCode = "unknown", "thread_reply_receipt_unconfirmed"
	}
	if v.Status == "unknown" {
		// Keep the observed ID in the attempt receipt only. It may belong to another topic.
		v.ProviderMessageID = ""
	}
	return v
}

func recordDeliveryReceipt(ctx context.Context, tx *sql.Tx, id string, v service.DeliveryCompletion) error {
	var authorized bool
	if e := tx.QueryRowContext(ctx, `SELECT `+deliveryAllowedExpression+` FROM manager_channel_deliveries d JOIN manager_channel_inbox n ON n.id=d.inbox_id JOIN platform_channel_bindings b ON b.id=d.binding_id JOIN platform_agents a ON a.id=b.agent_id WHERE d.id=$1`, id).Scan(&authorized); e != nil {
		return e
	}
	raw, digest, e := deliveryReceiptBytes(v)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO manager_channel_delivery_receipts(delivery_id,attempt_token,target_digest,receipt_digest,receipt,authorization_current)VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(delivery_id,attempt_token,receipt_digest) DO NOTHING`, id, v.LeaseToken, v.TargetDigest, digest, raw, authorized)
	return e
}

func deliveryReceiptBytes(v service.DeliveryCompletion) ([]byte, string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(raw)
	return raw, hex.EncodeToString(sum[:]), nil
}
