package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func (s *Store) CompleteHumanNotice(ctx context.Context, id string, v service.DeliveryCompletion) error {
	if !service.ValidID(id) {
		return service.Fail("invalid_argument", 400, "投递身份无效")
	}
	if err := validateDeliveryReceipt(v); err != nil {
		return err
	}
	if len(v.ProviderChatID) > 512 || strings.ContainsAny(v.ProviderChatID, "\x00\r\n") || v.ProviderThreadID != "" {
		return service.Fail("invalid_argument", 400, "确认通知回执目标无效")
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	var provider, state, lease, digest, query, request string
	var exact bool
	err = tx.QueryRowContext(ctx, `SELECT provider,status,COALESCE(lease_token::text,''),COALESCE(attempt_target_digest,''),
 COALESCE(query_token::text,''),COALESCE(provider_request_id,''),COALESCE(receipt=$2::jsonb,false)
 FROM manager_human_notices WHERE id=$1 FOR UPDATE`, id, raw).Scan(&provider, &state, &lease, &digest, &query, &request, &exact)
	if errors.Is(err, sql.ErrNoRows) {
		return service.Fail("delivery_not_found", 404, "投递不存在")
	}
	if err != nil {
		return err
	}
	if exact {
		return nil
	}
	if lease != v.LeaseToken || digest != v.TargetDigest || !validHumanNoticeReceipt(provider, state, query, request, v) {
		return service.Fail("human_notice_receipt_conflict", 409, "投递回执与原尝试不一致")
	}
	_, err = tx.ExecContext(ctx, `UPDATE manager_human_notices SET status=$2,
 provider_message_id=NULLIF($3,''),provider_chat_id=NULLIF($4,''),provider_request_id=COALESCE(NULLIF($5,''),provider_request_id),
 evidence_type=NULLIF($6,''),evidence_id=NULLIF($7,''),error_code=NULLIF($8,''),receipt=$9,
 sent_at=CASE WHEN $2='sent' THEN now() ELSE NULL END,
 query_token=NULL,query_until=NULL,
 next_query_at=CASE WHEN $2 IN ('accepted','unknown') AND COALESCE(NULLIF($5,''),provider_request_id) IS NOT NULL AND provider='dingtalk'
 THEN now()+interval '5 seconds' ELSE NULL END
 WHERE id=$1`, id, v.Status, v.ProviderMessageID, v.ProviderChatID, v.ProviderRequestID, v.EvidenceType, v.EvidenceID, v.ErrorCode, raw)
	if err != nil {
		return err
	}
	if err := recordDingProactiveSend(ctx, tx, provider, dingHumanAttempt, id, v); err != nil {
		return err
	}
	if v.Status == "sent" {
		if err = captureHumanDMNoticeContext(ctx, tx, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func validHumanNoticeReceipt(provider, state, query, request string, v service.DeliveryCompletion) bool {
	if !deliveryEvidence(provider, v) {
		return false
	}
	if v.QueryToken != "" {
		if provider != "dingtalk" || query != v.QueryToken || request == "" || request != v.ProviderRequestID ||
			(state != "accepted" && state != "unknown") {
			return false
		}
	} else if (state != "sending" && state != "unknown") || request != "" {
		return false
	}
	if provider != "feishu" && (v.ProviderMessageID != "" || v.ProviderChatID != "") {
		return false
	}
	if provider == "feishu" && v.Status == "sent" && (v.ProviderChatID == "" || v.ProviderMessageID == "") {
		return false
	}
	return true
}
