package repository

import (
	"context"
	"database/sql"
	"errors"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"strings"
)

type WecomGroupRefreshStore struct{ DB *sql.DB }

const wecomRefreshScope = `b.enabled AND b.provider='wecom' AND b.identity_verified_generation=b.connection_generation AND b.external_tenant_id<>'' AND b.connection_generation=j.connection_generation AND b.config_revision=j.config_revision AND b.identity_epoch=j.identity_epoch AND b.external_tenant_id=j.external_tenant_id AND (j.requested_by IS NULL OR EXISTS(SELECT 1 FROM platform_members m WHERE m.workspace_id=b.workspace_id AND m.user_id=j.requested_by AND m.status='active' AND platform_can_manage_agent(b.workspace_id,b.agent_id,m.user_id))) AND EXISTS(SELECT 1 FROM platform_agents a WHERE a.id=b.agent_id AND a.enabled)`

func (s *WecomGroupRefreshStore) RequestWecomGroups(ctx context.Context, b service.ChannelBinding, actor string) (service.WecomGroupRefreshStatus, error) {
	out := service.WecomGroupRefreshStatus{}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return out, e
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	var id string
	e = tx.QueryRowContext(ctx, `SELECT b.id FROM platform_channel_bindings b JOIN platform_members m ON m.workspace_id=b.workspace_id AND m.user_id=$2 AND m.status='active' AND platform_can_manage_agent(b.workspace_id,b.agent_id,m.user_id) JOIN platform_agents a ON a.id=b.agent_id AND a.enabled WHERE b.id=$1 AND b.provider='wecom' AND b.enabled AND b.connection_generation=$3 AND b.config_revision=$4 AND b.identity_epoch=$5 AND b.external_tenant_id=$6 AND b.identity_verified_generation=b.connection_generation FOR UPDATE OF b`, b.ID, actor, b.Generation, b.Revision, b.IdentityEpoch, b.TenantID).Scan(&id)
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

// Automatic discovery runs only in the background claim path: inbox delivery
// relies solely on its existing observed-group write, not on the provider or this queue.
const enqueueWecomGroupNames = `INSERT INTO platform_wecom_group_refresh_jobs(id,binding_id,connection_generation,config_revision,identity_epoch,external_tenant_id)
 SELECT gen_random_uuid(),b.id,b.connection_generation,b.config_revision,b.identity_epoch,b.external_tenant_id
 FROM platform_channel_bindings b JOIN platform_agents a ON a.id=b.agent_id AND a.enabled
 LEFT JOIN platform_wecom_group_refresh_jobs old ON old.binding_id=b.id AND old.requested_by IS NULL
 WHERE b.provider='wecom' AND b.enabled AND b.external_tenant_id<>'' AND b.identity_verified_generation=b.connection_generation
 AND (old.id IS NULL OR old.created_at<=now()-interval '15 minutes'
      OR (old.connection_generation,old.config_revision,old.identity_epoch,old.external_tenant_id)
         <> (b.connection_generation,b.config_revision,b.identity_epoch,b.external_tenant_id))
 AND EXISTS (SELECT 1 FROM platform_channel_observed_chats x
   WHERE x.binding_id=b.id AND x.identity_epoch=b.identity_epoch AND x.external_tenant_id=b.external_tenant_id
   AND btrim(x.display_name)='' AND x.observed_at>=now()-interval '7 days'
   AND (old.id IS NULL OR old.identity_epoch<>b.identity_epoch OR old.connection_generation<>b.connection_generation
        OR old.config_revision<>b.config_revision OR x.observed_at>old.created_at)
   AND manager_channel_group_allowed(b.id,b.identity_epoch,b.external_tenant_id,x.external_chat_id,''))
 ORDER BY old.created_at NULLS FIRST,b.id LIMIT 1
 ON CONFLICT(binding_id) WHERE requested_by IS NULL DO UPDATE SET id=EXCLUDED.id,
 connection_generation=EXCLUDED.connection_generation,config_revision=EXCLUDED.config_revision,
 identity_epoch=EXCLUDED.identity_epoch,external_tenant_id=EXCLUDED.external_tenant_id,status='pending',
 error_code=NULL,lease_token=NULL,lease_until=NULL,created_at=EXCLUDED.created_at,deadline=EXCLUDED.deadline
 WHERE platform_wecom_group_refresh_jobs.created_at<=now()-interval '15 minutes'
 OR (platform_wecom_group_refresh_jobs.connection_generation,platform_wecom_group_refresh_jobs.config_revision,
     platform_wecom_group_refresh_jobs.identity_epoch,platform_wecom_group_refresh_jobs.external_tenant_id)
 <> (EXCLUDED.connection_generation,EXCLUDED.config_revision,EXCLUDED.identity_epoch,EXCLUDED.external_tenant_id)`

func (s *WecomGroupRefreshStore) ClaimWecomGroups(ctx context.Context) ([]service.WecomGroupRefreshJob, error) {
	out := []service.WecomGroupRefreshJob{}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return out, e
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	var id string
	claim := func() error {
		return tx.QueryRowContext(ctx, `SELECT j.id FROM platform_wecom_group_refresh_jobs j JOIN platform_channel_bindings b ON b.id=j.binding_id
 WHERE (j.status='pending' OR (j.status='leased' AND j.lease_until<now())) AND j.deadline>now() AND `+wecomRefreshScope+
			` ORDER BY (j.requested_by IS NULL),j.created_at,j.id LIMIT 1 FOR UPDATE OF j SKIP LOCKED`).Scan(&id)
	}
	if e = claim(); errors.Is(e, sql.ErrNoRows) {
		var added sql.Result
		if added, e = tx.ExecContext(ctx, enqueueWecomGroupNames); e != nil {
			return out, e
		}
		n, e := added.RowsAffected()
		if e != nil {
			return out, e
		}
		if n == 0 {
			return out, nil
		}
		if e = claim(); errors.Is(e, sql.ErrNoRows) {
			return out, tx.Commit()
		} else if e != nil {
			return out, e
		}
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
	var actor sql.NullString
	if e = tx.QueryRowContext(ctx, `SELECT requested_by FROM platform_wecom_group_refresh_jobs WHERE id=$1`, id).Scan(&actor); e != nil {
		return e
	}
	if actor.Valid {
		if e = tx.QueryRowContext(ctx, `SELECT m.user_id FROM platform_channel_bindings b JOIN platform_members m ON m.workspace_id=b.workspace_id
 WHERE b.id=$1 AND m.user_id=$2 AND m.status='active' AND platform_can_manage_agent(b.workspace_id,b.agent_id,m.user_id) FOR SHARE OF m`, binding, actor.String).Scan(&actor); e != nil {
			return service.Fail("refresh_changed", 409, "刷新权限已变化")
		}
	}
	j, e := scanWecomRefresh(tx.QueryRowContext(ctx, `SELECT `+wecomRefreshColumns+` FROM platform_wecom_group_refresh_jobs j JOIN platform_channel_bindings b ON b.id=j.binding_id WHERE j.id=$1 AND j.lease_token=$2 AND j.status='leased' AND j.lease_until>now() AND j.deadline>now() AND `+wecomRefreshScope+` FOR UPDATE OF j`, id, v.LeaseToken))
	if errors.Is(e, sql.ErrNoRows) {
		return service.Fail("refresh_changed", 409, "刷新授权已变化")
	}
	if e != nil {
		return e
	}
	if v.Status == "completed" {
		if e = fillWecomObservedNames(ctx, tx, j, v.Items); e != nil {
			return e
		}
	}
	if v.Status == "completed" && actor.Valid {
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

// Fill display metadata only; do not change group identity, freshness or grants.
func fillWecomObservedNames(ctx context.Context, tx *sql.Tx, j service.WecomGroupRefreshJob, items []service.WecomGroupRefreshItem) error {
	ids, names := []string{}, []string{}
	for _, g := range items {
		name := strings.TrimSpace(g.Name)
		if name != "" && !strings.ContainsAny(name, "\x00\r\n") {
			ids, names = append(ids, g.ID), append(names, name)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	_, err := tx.ExecContext(ctx, `UPDATE platform_channel_observed_chats x SET display_name=g.name
 FROM unnest($4::text[],$5::text[]) AS g(id,name)
 WHERE x.binding_id=$1 AND x.identity_epoch=$2 AND x.external_tenant_id=$3
 AND x.external_chat_id=g.id AND btrim(x.display_name)=''
 AND manager_channel_group_allowed(x.binding_id,x.identity_epoch,x.external_tenant_id,x.external_chat_id,'')`,
		j.BindingID, j.IdentityEpoch, j.TenantID, ids, names)
	return err
}
