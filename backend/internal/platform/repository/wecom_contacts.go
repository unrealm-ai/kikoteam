package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"time"
)

type WecomContactStore struct{ DB *sql.DB }

func lockWecomContactSource(ctx context.Context, tx *sql.Tx, b service.ChannelBinding, actor string) error {
	var ok bool
	err := tx.QueryRowContext(ctx, `SELECT b.enabled AND b.provider='wecom' AND b.identity_epoch=$3 AND b.connection_generation=$4 AND b.identity_verified_generation=b.connection_generation AND b.external_tenant_id=$5 AND m.status='active' AND m.role IN ('owner','admin') FROM platform_channel_bindings b JOIN platform_members m ON m.workspace_id=b.workspace_id AND m.user_id=$6 WHERE b.id=$1 AND b.workspace_id=$2 FOR UPDATE OF b FOR SHARE OF m`, b.ID, b.WorkspaceID, b.IdentityEpoch, b.Generation, b.TenantID, actor).Scan(&ok)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !ok {
		return service.Fail("wecom_contact_source_changed", 409, "渠道来源或管理权限已变化")
	}
	return err
}
func (s *WecomContactStore) CacheWecomContacts(ctx context.Context, b service.ChannelBinding, actor string, items []service.WecomContact) ([]service.WecomContact, error) {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback() //nolint:errcheck // Commit owns success.
	if e = lockWecomContactSource(ctx, tx, b, actor); e != nil {
		return nil, e
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM platform_wecom_contacts WHERE binding_id=$1 AND (identity_epoch<>$2 OR connection_generation<>$3 OR (expires_at<now() AND NOT EXISTS(SELECT 1 FROM platform_wecom_identity_mappings m WHERE m.binding_id=platform_wecom_contacts.binding_id AND m.identity_epoch=platform_wecom_contacts.identity_epoch AND m.connection_generation=platform_wecom_contacts.connection_generation AND m.search_user_id=platform_wecom_contacts.search_user_id)))`, b.ID, b.IdentityEpoch, b.Generation); e != nil {
		return nil, e
	}
	out := make([]service.WecomContact, 0, len(items))
	seen := map[string]string{}
	for _, v := range items {
		if v.DepartmentPaths == nil {
			v.DepartmentPaths = []string{}
		}
		if prior, ok := seen[v.SearchUserID]; ok {
			if prior != v.DisplayName {
				return nil, service.Fail("wecom_contacts_invalid", 502, "联系人响应冲突")
			}
			continue
		}
		seen[v.SearchUserID] = v.DisplayName
		paths, e := json.Marshal(v.DepartmentPaths)
		if e != nil {
			return nil, e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO platform_wecom_contacts(binding_id,identity_epoch,connection_generation,search_user_id,display_name,department_paths,expires_at) VALUES($1,$2,$3,$4,$5,$6,now()+interval '10 minutes') ON CONFLICT(binding_id,identity_epoch,connection_generation,search_user_id) DO UPDATE SET display_name=EXCLUDED.display_name,department_paths=EXCLUDED.department_paths,expires_at=EXCLUDED.expires_at`, b.ID, b.IdentityEpoch, b.Generation, v.SearchUserID, v.DisplayName, paths); e != nil {
			return nil, e
		}
		v.CallbackUserID = ""
		v.VerificationID = ""
		v.VerificationStatus = "unverified"
		e = tx.QueryRowContext(ctx, `SELECT callback_user_id,verification_id FROM platform_wecom_identity_mappings WHERE binding_id=$1 AND identity_epoch=$2 AND connection_generation=$3 AND search_user_id=$4`, b.ID, b.IdentityEpoch, b.Generation, v.SearchUserID).Scan(&v.CallbackUserID, &v.VerificationID)
		if e == nil {
			v.VerificationStatus = "verified"
		} else if !errors.Is(e, sql.ErrNoRows) {
			return nil, e
		} else {
			var state string
			e = tx.QueryRowContext(ctx, `SELECT id,CASE WHEN status='pending' AND expires_at<=now() THEN 'expired' ELSE status END FROM platform_wecom_verifications WHERE binding_id=$1 AND identity_epoch=$2 AND connection_generation=$3 AND search_user_id=$4 ORDER BY created_at DESC LIMIT 1`, b.ID, b.IdentityEpoch, b.Generation, v.SearchUserID).Scan(&v.VerificationID, &state)
			if e == nil {
				v.VerificationStatus = state
			} else if !errors.Is(e, sql.ErrNoRows) {
				return nil, e
			}
		}
		out = append(out, v)
	}
	// Bound temporary search material; verified profiles remain attached to their
	// exact mapping scope for display, not fresh candidate authorization.
	if _, e = tx.ExecContext(ctx, `DELETE FROM platform_wecom_contacts WHERE ctid IN (SELECT ctid FROM platform_wecom_contacts c WHERE binding_id=$1 AND NOT EXISTS(SELECT 1 FROM platform_wecom_identity_mappings m WHERE m.binding_id=c.binding_id AND m.identity_epoch=c.identity_epoch AND m.connection_generation=c.connection_generation AND m.search_user_id=c.search_user_id) ORDER BY expires_at DESC,search_user_id OFFSET 1000)`, b.ID); e != nil {
		return nil, e
	}
	return out, tx.Commit()
}

const wecomVerificationColumns = `id,search_user_id,CASE WHEN status='pending' AND expires_at<=now() THEN 'expired' ELSE status END,callback_user_id,expires_at,code_hash,code_ciphertext`
const wecomVerificationScope = `id=$1 AND binding_id=$2 AND identity_epoch=$3
 AND (connection_generation=$4 OR (status='verified' AND EXISTS(SELECT FROM platform_wecom_identity_mappings m
 WHERE m.binding_id=$2 AND m.identity_epoch=$3 AND m.connection_generation=$4 AND m.verification_id=$1)))`

func scanWecomVerification(row scanner) (service.WecomVerification, error) {
	var v service.WecomVerification
	e := row.Scan(&v.ID, &v.SearchUserID, &v.Status, &v.CallbackUserID, &v.ExpiresAt, &v.CodeHash, &v.Ciphertext)
	return v, e
}
func (s *WecomContactStore) IssueWecomVerification(ctx context.Context, b service.ChannelBinding, actor string, v service.WecomVerification) (service.WecomVerification, error) {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return v, e
	}
	defer tx.Rollback() //nolint:errcheck // Commit owns success.
	if e = lockWecomContactSource(ctx, tx, b, actor); e != nil {
		return v, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE platform_wecom_verifications SET code_ciphertext='' WHERE id IN (SELECT id FROM platform_wecom_verifications WHERE binding_id=$1 AND expires_at<=now() AND octet_length(code_ciphertext)>0 LIMIT 100)`, b.ID); e != nil {
		return v, e
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM platform_wecom_verification_attempts WHERE binding_id=$1 AND window_start<now()-interval '2 days'`, b.ID); e != nil {
		return v, e
	}
	prior, e := scanWecomVerification(tx.QueryRowContext(ctx, `SELECT `+wecomVerificationColumns+` FROM platform_wecom_verifications WHERE `+wecomVerificationScope, v.ID, b.ID, b.IdentityEpoch, b.Generation))
	if e == nil {
		if prior.SearchUserID != v.SearchUserID {
			return v, service.Fail("verification_request_conflict", 409, "请求标识已用于其他联系人")
		}
		return prior, tx.Commit()
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return v, e
	}
	var eligible bool
	e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM platform_wecom_contacts WHERE binding_id=$1 AND identity_epoch=$2 AND connection_generation=$3 AND search_user_id=$4 AND expires_at>now())`, b.ID, b.IdentityEpoch, b.Generation, v.SearchUserID).Scan(&eligible)
	if e != nil {
		return v, e
	}
	if !eligible {
		return v, service.Fail("wecom_contact_candidate_expired", 409, "请重新搜索联系人后再生成验证码")
	}
	var active bool
	e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM platform_wecom_identity_mappings WHERE binding_id=$1 AND identity_epoch=$2 AND connection_generation=$3 AND search_user_id=$4) OR EXISTS(SELECT 1 FROM platform_wecom_verifications WHERE binding_id=$1 AND identity_epoch=$2 AND connection_generation=$3 AND search_user_id=$4 AND status='pending' AND expires_at>now())`, b.ID, b.IdentityEpoch, b.Generation, v.SearchUserID).Scan(&active)
	if e != nil {
		return v, e
	}
	if active {
		return v, service.Fail("wecom_verification_exists", 409, "已有映射或有效验证码，请刷新联系人状态")
	}
	var count int
	e = tx.QueryRowContext(ctx, `SELECT count(*) FROM platform_wecom_verifications WHERE binding_id=$1 AND created_at>now()-interval '1 hour'`, b.ID).Scan(&count)
	if e != nil {
		return v, e
	}
	if count >= 200 {
		return v, service.Fail("wecom_verification_rate_limited", 429, "验证码创建过于频繁，请稍后再试")
	}
	// Release a reissued number only after its recognition window. Keep the
	// verification row and mapping foreign key intact; a retired marker cannot
	// match a real keyed hex digest.
	if _, e = tx.ExecContext(ctx, `UPDATE platform_wecom_verifications SET code_hash='retired:'||id::text,code_ciphertext=''
 WHERE code_hash=$1 AND binding_id=$2 AND `+wecomCodeRetentionEnd+`<=now()`, v.CodeHash, b.ID); e != nil {
		return v, e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO platform_wecom_verifications(id,binding_id,identity_epoch,connection_generation,search_user_id,created_by,code_hash,code_ciphertext,status,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'pending',$9)`, v.ID, b.ID, b.IdentityEpoch, b.Generation, v.SearchUserID, actor, v.CodeHash, v.Ciphertext, v.ExpiresAt)
	if e != nil {
		var pg *pgconn.PgError
		if errors.As(e, &pg) && pg.Code == "23505" {
			switch pg.ConstraintName {
			case "platform_wecom_verifications_code_hash_key":
				return v, service.Fail("verification_code_collision", 503, "验证码暂不可用")
			case "platform_wecom_verifications_pkey":
				return v, service.Fail("verification_request_conflict", 409, "请求标识已用于其他验证")
			}
		}
		return v, e
	}
	return v, tx.Commit()
}
func (s *WecomContactStore) GetWecomVerification(ctx context.Context, b service.ChannelBinding, actor, id string, cancel bool) (service.WecomVerification, error) {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return service.WecomVerification{}, e
	}
	defer tx.Rollback() //nolint:errcheck // Commit owns success.
	if e = lockWecomContactSource(ctx, tx, b, actor); e != nil {
		return service.WecomVerification{}, e
	}
	v, e := scanWecomVerification(tx.QueryRowContext(ctx, `SELECT `+wecomVerificationColumns+` FROM platform_wecom_verifications WHERE `+wecomVerificationScope+` FOR UPDATE`, id, b.ID, b.IdentityEpoch, b.Generation))
	if errors.Is(e, sql.ErrNoRows) {
		return v, service.Fail("verification_not_found", 404, "验证记录不存在或来源已变化")
	}
	if e != nil {
		return v, e
	}
	if cancel && v.Status == "pending" {
		_, e = tx.ExecContext(ctx, `UPDATE platform_wecom_verifications SET status='cancelled',completed_at=now(),code_ciphertext='' WHERE id=$1`, id)
		if e != nil {
			return v, e
		}
		v.Status = "cancelled"
		v.Ciphertext = nil
	}
	if v.ExpiresAt.Before(time.Now()) && v.Status == "pending" {
		v.Status = "expired"
	}
	return v, tx.Commit()
}
