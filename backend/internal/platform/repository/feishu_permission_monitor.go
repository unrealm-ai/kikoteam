package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func (s *ChannelStore) ClaimFeishuPermissionCheck(ctx context.Context) (*service.FeishuPermissionClaim, error) {
	token := service.NewID()
	b, err := scanChannel(s.DB.QueryRowContext(ctx, `WITH candidate AS (
		SELECT c.binding_id FROM platform_feishu_permission_checks c JOIN platform_channel_bindings b ON b.id=c.binding_id
		WHERE b.enabled AND c.connection_generation=b.connection_generation AND c.next_check_at<=now()
		AND (c.lease_until IS NULL OR c.lease_until<=now())
		ORDER BY c.next_check_at,c.binding_id LIMIT 1 FOR UPDATE OF c SKIP LOCKED
	), claimed AS (
		UPDATE platform_feishu_permission_checks c SET lease_token=$1,lease_until=now()+interval '90 seconds'
		FROM candidate WHERE c.binding_id=candidate.binding_id RETURNING c.binding_id,c.connection_generation
	)
	SELECT `+channelColumns+` FROM platform_channel_bindings WHERE (id,connection_generation) IN (SELECT binding_id,connection_generation FROM claimed)`, token))
	if service.IsChannelMissing(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &service.FeishuPermissionClaim{Binding: b, Token: token}, nil
}

func (s *ChannelStore) FinishFeishuPermissionCheck(ctx context.Context, claim service.FeishuPermissionClaim, d service.FeishuDiagnostics, delay time.Duration) error {
	raw, err := json.Marshal(d)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `WITH finished AS (
		UPDATE platform_feishu_permission_checks c SET diagnostics=$4,next_check_at=now()+$5::bigint*interval '1 second',
		lease_token=NULL,lease_until=NULL FROM platform_channel_bindings b
		WHERE c.binding_id=$1 AND c.connection_generation=$2 AND c.lease_token=$3 AND c.lease_until>now()
		AND b.id=c.binding_id AND b.enabled AND b.connection_generation=c.connection_generation
		RETURNING c.binding_id,c.connection_generation
	)
	UPDATE platform_channel_directory_state d SET next_sync_at=now()
	FROM finished WHERE d.binding_id=finished.binding_id AND d.connection_generation=finished.connection_generation
	AND NOT d.full_snapshot AND (d.lease_until IS NULL OR d.lease_until<=now()) AND $6
	AND (d.synced_at IS NULL OR d.synced_at < now()-interval '30 seconds')`,
		claim.Binding.ID, claim.Binding.Generation, claim.Token, raw, int64(delay/time.Second), d.Ready)
	return err
}

func (s *ChannelStore) FeishuPermissionDiagnostics(ctx context.Context, b service.ChannelBinding) (*service.FeishuDiagnostics, error) {
	var raw []byte
	err := s.DB.QueryRowContext(ctx, `SELECT diagnostics FROM platform_feishu_permission_checks
		WHERE binding_id=$1 AND connection_generation=$2 AND diagnostics IS NOT NULL`, b.ID, b.Generation).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var result service.FeishuDiagnostics
	if err = json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
