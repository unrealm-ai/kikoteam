package repository

import (
	"context"
	"database/sql"
	"errors"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"strings"
)

type WecomGroupRefreshStore struct{ DB *sql.DB }

const wecomRefreshScope = `b.enabled AND b.provider='wecom' AND b.identity_verified_generation=b.connection_generation AND b.external_tenant_id<>'' AND b.connection_generation=j.connection_generation AND b.config_revision=j.config_revision AND b.identity_epoch=j.identity_epoch AND b.external_tenant_id=j.external_tenant_id AND EXISTS(SELECT 1 FROM platform_members m WHERE m.workspace_id=b.workspace_id AND m.user_id=j.requested_by AND m.status='active' AND m.role IN('owner','admin')) AND EXISTS(SELECT 1 FROM platform_agents a WHERE a.id=b.agent_id AND a.enabled)`

func (s *WecomGroupRefreshStore) RequestWecomGroups(ctx context.Context, b service.ChannelBinding, actor string) (service.WecomGroupRefreshStatus, error) {
	out := service.WecomGroupRefreshStatus{}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return out, e
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	var id string
	e = tx.QueryRowContext(ctx, `SELECT b.id FROM platform_channel_bindings b JOIN platform_members m ON m.workspace_id=b.workspace_id AND m.user_id=$2 AND m.status='active' AND m.role IN('owner','admin') JOIN platform_agents a ON a.id=b.agent_id AND a.enabled WHERE b.id=$1 AND b.provider='wecom' AND b.enabled AND b.connection_generation=$3 AND b.config_revision=$4 AND b.identity_epoch=$5 AND b.external_tenant_id=$6 AND b.identity_verified_generation=b.connection_generation FOR UPDATE OF b`, b.ID, actor, b.Generation, b.Revision, b.IdentityEpoch, b.TenantID).Scan(&id)
	if e != nil {
		return out, service.Fail("channel_changed", 409, "连接或权限已变化")
	}
	e = tx.QueryRowContext(ctx, `SELECT id,status FROM platform_wecom_group_refresh_jobs WHERE binding_id=$1 AND requested_by=$2 AND connection_generation=$3 AND config_revision=$4 AND status IN('pending','leased') AND deadline>now() ORDER BY created_at DESC LIMIT 1`, b.ID, actor, b.Generation, b.Revision).Scan(&out.ID, &out.Status)
	if errors.Is(e, sql.ErrNoRows) {
		out.ID = service.NewID()
		out.Status = "pending"
		_, e = tx.ExecContext(ctx, `INSERT INTO platform_wecom_group_refresh_jobs(id,binding_id,requested_by,connection_generation,config_revision,identity_epoch,external_tenant_id)VALUES($1,$2,$3,$4,$5,$6,$7)`, out.ID, b.ID, actor, b.Generation, b.Revision, b.IdentityEpoch, b.TenantID)
	}
	if e != nil {
		return out, e
	}
	return out, tx.Commit()
}
func (s *WecomGroupRefreshStore) ReadWecomGroups(ctx context.Context, b service.ChannelBinding, actor, id string) (service.WecomGroupRefreshStatus, error) {
	var out service.WecomGroupRefreshStatus
	e := s.DB.QueryRowContext(ctx, `SELECT j.id,CASE WHEN j.status IN('pending','leased') AND j.deadline<now() THEN 'failed' ELSE j.status END,CASE WHEN j.deadline<now() AND j.status IN('pending','leased') THEN 'refresh_expired' ELSE COALESCE(j.error_code,'') END FROM platform_wecom_group_refresh_jobs j JOIN platform_channel_bindings b ON b.id=j.binding_id WHERE j.id=$1 AND j.binding_id=$2 AND j.requested_by=$3 AND `+wecomRefreshScope, id, b.ID, actor).Scan(&out.ID, &out.Status, &out.ErrorCode)
	if errors.Is(e, sql.ErrNoRows) {
		e = service.Fail("resource_not_found", 404, "刷新任务不存在")
	}
	return out, e
}

const wecomRefreshColumns = `j.id,j.lease_token,j.binding_id,j.connection_generation,j.config_revision,j.identity_epoch,j.external_tenant_id`

func scanWecomRefresh(r interface{ Scan(...any) error }) (service.WecomGroupRefreshJob, error) {
	var j service.WecomGroupRefreshJob
	e := r.Scan(&j.ID, &j.LeaseToken, &j.BindingID, &j.Generation, &j.Revision, &j.IdentityEpoch, &j.TenantID)
	return j, e
}
func (s *WecomGroupRefreshStore) ClaimWecomGroups(ctx context.Context) ([]service.WecomGroupRefreshJob, error) {
	out := []service.WecomGroupRefreshJob{}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return out, e
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	var id string
	e = tx.QueryRowContext(ctx, `SELECT j.id FROM platform_wecom_group_refresh_jobs j JOIN platform_channel_bindings b ON b.id=j.binding_id WHERE (j.status='pending' OR (j.status='leased' AND j.lease_until<now())) AND j.deadline>now() AND `+wecomRefreshScope+` ORDER BY j.created_at,j.id LIMIT 1 FOR UPDATE OF j SKIP LOCKED`).Scan(&id)
	if errors.Is(e, sql.ErrNoRows) {
		return out, nil
	}
	if e != nil {
		return out, e
	}
	_, e = tx.ExecContext(ctx, `UPDATE platform_wecom_group_refresh_jobs SET status='leased',lease_token=gen_random_uuid(),lease_until=now()+interval '45 seconds' WHERE id=$1`, id)
	if e != nil {
		return out, e
	}
	j, e := scanWecomRefresh(tx.QueryRowContext(ctx, `SELECT `+wecomRefreshColumns+` FROM platform_wecom_group_refresh_jobs j WHERE j.id=$1`, id))
	if e != nil {
		return out, e
	}
	out = append(out, j)
	return out, tx.Commit()
}
func (s *WecomGroupRefreshStore) AuthorizeWecomGroups(ctx context.Context, id, token string) (service.WecomGroupRefreshJob, error) {
	if !service.ValidWecomRefreshID(id) || !service.ValidWecomRefreshID(token) {
		return service.WecomGroupRefreshJob{}, service.Fail("invalid_argument", 400, "刷新身份无效")
	}
	j, e := scanWecomRefresh(s.DB.QueryRowContext(ctx, `SELECT `+wecomRefreshColumns+` FROM platform_wecom_group_refresh_jobs j JOIN platform_channel_bindings b ON b.id=j.binding_id WHERE j.id=$1 AND j.lease_token=$2 AND j.status='leased' AND j.lease_until>now() AND j.deadline>now() AND `+wecomRefreshScope, id, token))
	if errors.Is(e, sql.ErrNoRows) {
		e = service.Fail("refresh_changed", 409, "刷新授权已变化")
	}
	return j, e
}
func (s *WecomGroupRefreshStore) CompleteWecomGroups(ctx context.Context, id string, v service.WecomGroupRefreshCompletion) error {
	if !service.ValidWecomRefreshID(id) || !service.ValidWecomRefreshID(v.LeaseToken) {
		return service.Fail("invalid_argument", 400, "刷新身份无效")
	}
	if (v.Status != "completed" && v.Status != "failed") || len(v.Items) > 10000 {
		return service.Fail("invalid_refresh", 400, "刷新结果无效")
	}
	ids := []string{}
	seen := map[string]bool{}
	for _, g := range v.Items {
		if g.ID == "" || len(g.ID) > 256 || strings.ContainsAny(g.ID, "\x00\r\n") || len(g.Name) > 1024 || seen[g.ID] {
			return service.Fail("invalid_refresh", 400, "群数据无效")
		}
		seen[g.ID] = true
		ids = append(ids, g.ID)
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	// Lock the binding before the job, same as request; serialize config changes.
	var binding string
	e = tx.QueryRowContext(ctx, `SELECT binding_id FROM platform_wecom_group_refresh_jobs WHERE id=$1`, id).Scan(&binding)
	if e != nil {
		return e
	}
	if e = tx.QueryRowContext(ctx, `SELECT id FROM platform_channel_bindings WHERE id=$1 FOR SHARE`, binding).Scan(&binding); e != nil {
		return e
	}
	var actor string
	if e = tx.QueryRowContext(ctx, `SELECT m.user_id FROM platform_wecom_group_refresh_jobs j JOIN platform_channel_bindings b ON b.id=j.binding_id JOIN platform_members m ON m.workspace_id=b.workspace_id AND m.user_id=j.requested_by AND m.status='active' AND m.role IN('owner','admin') WHERE j.id=$1 FOR SHARE OF m`, id).Scan(&actor); e != nil {
		return service.Fail("refresh_changed", 409, "刷新权限已变化")
	}
	j, e := scanWecomRefresh(tx.QueryRowContext(ctx, `SELECT `+wecomRefreshColumns+` FROM platform_wecom_group_refresh_jobs j JOIN platform_channel_bindings b ON b.id=j.binding_id WHERE j.id=$1 AND j.lease_token=$2 AND j.status='leased' AND j.lease_until>now() AND j.deadline>now() AND `+wecomRefreshScope+` FOR UPDATE OF j`, id, v.LeaseToken))
	if errors.Is(e, sql.ErrNoRows) {
		return service.Fail("refresh_changed", 409, "刷新授权已变化")
	}
	if e != nil {
		return e
	}
	if v.Status == "completed" {
		if _, e = tx.ExecContext(ctx, `DELETE FROM platform_channel_group_candidates WHERE binding_id=$1 AND external_chat_id<>ALL($2::text[])`, binding, ids); e != nil {
			return e
		}
		for _, g := range v.Items {
			_, e = tx.ExecContext(ctx, `INSERT INTO platform_channel_group_candidates(id,binding_id,identity_epoch,external_tenant_id,connection_generation,config_revision,external_chat_id,display_name,expires_at)VALUES(gen_random_uuid(),$1,$2,$3,$4,$5,$6,$7,now()+interval '5 minutes') ON CONFLICT(binding_id,identity_epoch,external_tenant_id,external_chat_id)DO UPDATE SET connection_generation=EXCLUDED.connection_generation,config_revision=EXCLUDED.config_revision,display_name=EXCLUDED.display_name,confirmed_at=now(),expires_at=EXCLUDED.expires_at`, binding, j.IdentityEpoch, j.TenantID, j.Generation, j.Revision, g.ID, g.Name)
			if e != nil {
				return e
			}
		}
	}
	_, e = tx.ExecContext(ctx, `UPDATE platform_wecom_group_refresh_jobs SET status=$2,error_code=CASE WHEN $2='failed' THEN 'provider_refresh_unavailable' ELSE NULL END,lease_until=NULL WHERE id=$1`, id, v.Status)
	if e != nil {
		return e
	}
	return tx.Commit()
}
