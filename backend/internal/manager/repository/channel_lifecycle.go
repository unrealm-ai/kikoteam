package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"slices"
	"strings"
	"time"
)

func validLifecycle(v service.ChannelLifecycle) bool {
	if !service.ValidID(v.BindingID) || v.Generation < 1 || v.Revision < 1 || v.OccurredAtMS <= 0 || v.OccurredAtMS > time.Now().Add(5*time.Minute).UnixMilli() || len(v.MemberIDs) > 1000 {
		return false
	}
	for _, s := range append([]string{v.TenantID, v.ChatID, v.EventID}, v.MemberIDs...) {
		if s == "" || len(s) > 256 || strings.ContainsRune(s, 0) {
			return false
		}
	}
	switch v.EventType {
	case "bot_added", "bot_removed", "chat_disbanded":
		return len(v.MemberIDs) == 0
	case "members_added", "members_removed":
		return len(v.MemberIDs) > 0
	}
	return false
}
func (s *Store) ReceiveChannelLifecycle(ctx context.Context, v service.ChannelLifecycle) (service.ChannelReceipt, error) {
	out := service.ChannelReceipt{ID: service.NewID(), Status: "accepted"}
	if !validLifecycle(v) {
		return out, service.Fail("invalid_argument", 400, "群生命周期事件无效")
	}
	v.MemberIDs = slices.Clone(v.MemberIDs)
	slices.Sort(v.MemberIDs)
	v.MemberIDs = slices.Compact(v.MemberIDs)
	content := v
	content.Generation = 0
	content.Revision = 0
	raw, err := json.Marshal(content)
	if err != nil {
		return out, err
	}
	hash := sha256.Sum256(raw)
	fingerprint := hex.EncodeToString(hash[:])
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Preserve operation error.
	var epoch int64
	err = tx.QueryRowContext(ctx, `SELECT identity_epoch FROM platform_channel_bindings WHERE id=$1 AND enabled AND provider='feishu' AND connection_generation=$2 AND config_revision=$3 AND external_tenant_id=$4 AND external_tenant_id<>'' FOR UPDATE`, v.BindingID, v.Generation, v.Revision, v.TenantID).Scan(&epoch)
	if errors.Is(err, sql.ErrNoRows) {
		return out, service.Fail("binding_generation_stale", 409, "渠道身份已过期")
	}
	if err != nil {
		return out, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO manager_channel_group_events(id,binding_id,identity_epoch,external_tenant_id,provider_event_id,fingerprint) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, out.ID, v.BindingID, epoch, v.TenantID, v.EventID, fingerprint)
	if err != nil {
		return out, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return out, err
	}
	if count == 0 {
		var old string
		err = tx.QueryRowContext(ctx, `SELECT id,fingerprint FROM manager_channel_group_events WHERE binding_id=$1 AND identity_epoch=$2 AND external_tenant_id=$3 AND provider_event_id=$4`, v.BindingID, epoch, v.TenantID, v.EventID).Scan(&out.ID, &old)
		if err != nil {
			return out, err
		}
		if old != fingerprint {
			return out, service.Fail("event_conflict", 409, "事件身份冲突")
		}
		out.Status = "duplicate"
		return out, tx.Commit()
	}
	if v.EventType == "members_added" || v.EventType == "members_removed" {
		for _, id := range v.MemberIDs {
			_, err = tx.ExecContext(ctx, `INSERT INTO manager_channel_group_members(binding_id,identity_epoch,external_tenant_id,external_chat_id,member_id,present,watermark_ms) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(binding_id,identity_epoch,external_tenant_id,external_chat_id,member_id) DO UPDATE SET present=EXCLUDED.present,watermark_ms=EXCLUDED.watermark_ms WHERE EXCLUDED.watermark_ms>manager_channel_group_members.watermark_ms OR (EXCLUDED.watermark_ms=manager_channel_group_members.watermark_ms AND NOT EXCLUDED.present)`, v.BindingID, epoch, v.TenantID, v.ChatID, id, v.EventType == "members_added", v.OccurredAtMS)
			if err != nil {
				return out, err
			}
		}
	} else {
		state := "pending"
		if v.EventType == "chat_disbanded" {
			state = "disbanded"
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO manager_channel_groups(id,binding_id,identity_epoch,external_tenant_id,external_chat_id,state,watermark_ms,connection_generation,config_revision) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT(binding_id,identity_epoch,external_tenant_id,external_chat_id) DO UPDATE SET state=EXCLUDED.state,watermark_ms=GREATEST(EXCLUDED.watermark_ms,manager_channel_groups.watermark_ms),version=manager_channel_groups.version+1,connection_generation=EXCLUDED.connection_generation,config_revision=EXCLUDED.config_revision,retry_at=now(),attempts=0,lease_token=NULL,lease_until=NULL WHERE manager_channel_groups.state<>'disbanded' AND (EXCLUDED.state='disbanded' OR EXCLUDED.watermark_ms>=manager_channel_groups.watermark_ms)`, service.NewID(), v.BindingID, epoch, v.TenantID, v.ChatID, state, v.OccurredAtMS, v.Generation, v.Revision)
		if err != nil {
			return out, err
		}
	}
	// Pending/leased output predating a revocation is never resurrected by rejoin.
	_, err = tx.ExecContext(ctx, `UPDATE manager_channel_deliveries d SET status='cancelled',error_code='group_membership_changed',updated_at=now() FROM manager_channel_inbox n WHERE d.inbox_id=n.id AND d.status IN ('pending','leased') AND n.binding_id=$1 AND n.identity_epoch=$2 AND n.external_tenant_id=$3 AND n.external_chat_id=$4 AND n.chat_type='group' AND NOT manager_channel_group_allowed(n.binding_id,n.identity_epoch,n.external_tenant_id,n.external_chat_id,n.sender_id)`, v.BindingID, epoch, v.TenantID, v.ChatID)
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}

func (s *Store) ClaimChannelGroupChecks(ctx context.Context) ([]service.ChannelGroupCheck, error) {
	rows, err := s.DB.QueryContext(ctx, `WITH claim AS (SELECT g.id,b.connection_generation,b.config_revision FROM manager_channel_groups g JOIN platform_channel_bindings b ON b.id=g.binding_id WHERE g.state='pending' AND g.retry_at<=now() AND (g.lease_until IS NULL OR g.lease_until<now()) AND b.enabled AND b.identity_epoch=g.identity_epoch AND b.external_tenant_id=g.external_tenant_id ORDER BY g.retry_at,g.id FOR UPDATE OF g SKIP LOCKED LIMIT 5) UPDATE manager_channel_groups g SET lease_token=gen_random_uuid(),lease_until=now()+interval '30 seconds',connection_generation=c.connection_generation,config_revision=c.config_revision FROM claim c WHERE g.id=c.id RETURNING g.id,g.lease_token,g.binding_id,g.connection_generation,g.config_revision,g.external_tenant_id,g.external_chat_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // rows.Err is checked below.
	out := []service.ChannelGroupCheck{}
	for rows.Next() {
		var v service.ChannelGroupCheck
		if err = rows.Scan(&v.ID, &v.LeaseToken, &v.BindingID, &v.Generation, &v.Revision, &v.TenantID, &v.ChatID); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) CompleteChannelGroupCheck(ctx context.Context, id string, v service.ChannelGroupCompletion) error {
	if !service.ValidID(id) || !service.ValidID(v.LeaseToken) {
		return service.Fail("invalid_argument", 400, "群核验回执无效")
	}
	state := "pending"
	if v.Present != nil {
		state = "absent"
		if *v.Present {
			state = "present"
		}
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE manager_channel_groups g SET state=$3,last_error_code=CASE WHEN $3='pending' THEN 'membership_verification_unavailable' ELSE '' END,attempts=g.attempts+1,retry_at=now()+interval '30 seconds'*LEAST(10,g.attempts+1),lease_token=NULL,lease_until=NULL FROM platform_channel_bindings b WHERE g.id=$1 AND g.lease_token=$2 AND g.lease_until>now() AND g.state='pending' AND b.id=g.binding_id AND b.enabled AND b.identity_epoch=g.identity_epoch AND b.external_tenant_id=g.external_tenant_id AND b.connection_generation=g.connection_generation AND b.config_revision=g.config_revision`, id, v.LeaseToken, state)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return service.Fail("lease_lost", 409, "群核验代次已过期")
	}
	return nil
}

// A newer live message may recover a missed rejoin event, but never revives a
// disbanded group or an older pre-leave message. No remote I/O in this transaction.
func queueGroupRecheck(ctx context.Context, tx *sql.Tx, v service.ChannelInbound, epoch int64) error {
	if v.ChatType != "group" || v.CreatedAtMS <= 0 {
		return nil
	}
	_, err := tx.ExecContext(ctx, `UPDATE manager_channel_groups SET state='pending',watermark_ms=$5,version=version+1,connection_generation=$6,config_revision=$7,retry_at=now(),attempts=0,lease_token=NULL,lease_until=NULL WHERE binding_id=$1 AND identity_epoch=$2 AND external_tenant_id=$3 AND external_chat_id=$4 AND state='absent' AND watermark_ms<$5 AND EXISTS(SELECT 1 FROM platform_channel_bindings b WHERE b.id=$1 AND b.enabled AND b.connection_generation=$6 AND b.config_revision=$7 AND b.identity_epoch=$2 AND b.external_tenant_id=$3)`, v.BindingID, epoch, v.ExternalTenantID, v.ExternalChatID, v.CreatedAtMS, v.ConnectionGeneration, v.ConfigRevision)
	return err
}

func channelGroupAdmission(ctx context.Context, tx *sql.Tx, inbox, binding string, epoch int64, tenant, chat string) (bool, bool, error) {
	var state string
	var beforeChange bool
	err := tx.QueryRowContext(ctx, `SELECT g.state,g.watermark_ms>n.trigger_created_at_ms FROM manager_channel_groups g JOIN manager_channel_inbox n ON n.id=$5 WHERE g.binding_id=$1 AND g.identity_epoch=$2 AND g.external_tenant_id=$3 AND g.external_chat_id=$4`, binding, epoch, tenant, chat, inbox).Scan(&state, &beforeChange)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	return state == "pending", state == "absent" || state == "disbanded" || beforeChange, nil
}
