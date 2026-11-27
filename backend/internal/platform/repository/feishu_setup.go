package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type FeishuSetupStore struct{ DB *sql.DB }
type setupTx struct{ *channelTx }

func (s *FeishuSetupStore) DueFeishuRegistrations(ctx context.Context) ([]service.FeishuRegistration, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+setupCols+` FROM platform_feishu_registrations r
		WHERE state IN ('credentials_saved','needs_attention','checking','connected')
		AND (secret_ciphertext IS NOT NULL OR state='connected')
		AND COALESCE((record->>'auto_retry_blocked')::boolean,false)=false
		AND COALESCE((record->>'check_lease_until')::timestamptz,'-infinity')<=now()
		AND (state<>'connected' OR (record ? 'next_check_at'
		AND record#>>'{contact_range,status}' IN ('waiting_permission','applying','failed')))
		AND COALESCE((record->>'next_check_at')::timestamptz,'-infinity')<=now()
		AND EXISTS (SELECT 1 FROM platform_members m WHERE m.workspace_id=r.workspace_id AND m.user_id=r.actor_id
		AND m.status='active' AND platform_can_manage_agent(r.workspace_id,r.agent_id,m.user_id))
		ORDER BY COALESCE((record->>'next_check_at')::timestamptz,updated_at),id LIMIT 4`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Rows.Err reports failures from iterating the read-only query.
	var due []service.FeishuRegistration
	for rows.Next() {
		r, err := scanSetup(rows)
		if err != nil {
			return nil, err
		}
		due = append(due, r)
	}
	return due, rows.Err()
}

func (s *FeishuSetupStore) WithSetup(ctx context.Context, w string, fn func(service.FeishuSetupTx) error) error {
	return (&ChannelStore{DB: s.DB}).WithWorkspace(ctx, w, func(t service.ChannelTx) error { return fn(&setupTx{t.(*channelTx)}) })
}

const setupCols = `id,workspace_id,agent_id,actor_id,operation_key,version,record,device_ciphertext,secret_ciphertext`

func scanSetup(row scanner) (service.FeishuRegistration, error) {
	var r service.FeishuRegistration
	var data []byte
	err := row.Scan(&r.ID, &r.WorkspaceID, &r.AgentID, &r.ActorID, &r.OperationKey, &r.Version, &data, &r.DeviceCipher, &r.SecretCipher)
	if errors.Is(err, sql.ErrNoRows) {
		return r, service.Fail("feishu_registration_not_found", 404, "扫码登记不存在")
	}
	if err != nil {
		return r, err
	}
	err = json.Unmarshal(data, &r)
	return r, err
}
func (t *setupTx) Registration(ctx context.Context, id string) (service.FeishuRegistration, error) {
	return scanSetup(t.tx.QueryRowContext(ctx, `SELECT `+setupCols+` FROM platform_feishu_registrations WHERE workspace_id=$1 AND id=$2 FOR UPDATE`, t.workspace, id))
}
func (t *setupTx) LatestRegistration(ctx context.Context, agent, actor string) (*service.FeishuRegistration, error) {
	r, e := scanSetup(t.tx.QueryRowContext(ctx, `SELECT `+setupCols+` FROM platform_feishu_registrations WHERE workspace_id=$1 AND agent_id=$2 AND actor_id=$3 ORDER BY created_at DESC,id DESC LIMIT 1`, t.workspace, agent, actor))
	return optionalSetup(r, e)
}
func (t *setupTx) RegistrationByKey(ctx context.Context, agent, key string) (*service.FeishuRegistration, error) {
	r, e := scanSetup(t.tx.QueryRowContext(ctx, `SELECT `+setupCols+` FROM platform_feishu_registrations WHERE workspace_id=$1 AND agent_id=$2 AND operation_key=$3`, t.workspace, agent, key))
	return optionalSetup(r, e)
}
func optionalSetup(r service.FeishuRegistration, e error) (*service.FeishuRegistration, error) {
	var se *service.Error
	if errors.As(e, &se) && se.Code == "feishu_registration_not_found" {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	return &r, nil
}
func (t *setupTx) InsertRegistration(ctx context.Context, r service.FeishuRegistration) error {
	// Expire abandoned device challenges before testing the one-active rule.
	if _, e := t.tx.ExecContext(ctx, `UPDATE platform_feishu_registrations SET state='expired',record=jsonb_set(jsonb_set(record,'{state}','"expired"'),'{error_code}','"feishu_registration_expired"'),device_ciphertext=NULL,version=version+1,updated_at=now() WHERE workspace_id=$1 AND agent_id=$2 AND state IN ('starting','awaiting_confirmation','polling') AND (record->>'expires_at')::timestamptz <= now()`, t.workspace, r.AgentID); e != nil {
		return e
	}
	var recent int
	if e := t.tx.QueryRowContext(ctx, `SELECT count(*) FROM platform_feishu_registrations WHERE actor_id=$1 AND created_at>now()-interval '1 minute'`, r.ActorID).Scan(&recent); e != nil {
		return e
	}
	if recent >= 12 {
		return service.Fail("feishu_registration_rate_limited", 429, "扫码登记过于频繁，请稍后重试")
	}

	data, e := json.Marshal(r)
	if e != nil {
		return e
	}
	var busy bool
	if e = t.tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM platform_feishu_registrations WHERE workspace_id=$1 AND agent_id=$2 AND state IN ('starting','awaiting_confirmation','polling','credentials_saved','checking','needs_attention'))`, t.workspace, r.AgentID).Scan(&busy); e != nil {
		return e
	}
	if busy {
		return service.Fail("feishu_registration_in_progress", 409, "已有扫码登记进行中")
	}
	_, e = t.tx.ExecContext(ctx, `INSERT INTO platform_feishu_registrations(id,workspace_id,agent_id,actor_id,operation_key,state,version,record)VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, r.ID, t.workspace, r.AgentID, r.ActorID, r.OperationKey, r.State, r.Version, data)
	return e
}
func (t *setupTx) SaveRegistration(ctx context.Context, r service.FeishuRegistration) error {
	data, e := json.Marshal(r)
	if e != nil {
		return e
	}
	result, e := t.tx.ExecContext(ctx, `UPDATE platform_feishu_registrations SET state=$3,record=$4,device_ciphertext=$5,secret_ciphertext=$6,version=version+1,updated_at=now() WHERE workspace_id=$1 AND id=$2 AND version=$7`, t.workspace, r.ID, r.State, data, r.DeviceCipher, r.SecretCipher, r.Version)
	if e != nil {
		return e
	}
	n, e := result.RowsAffected()
	if e == nil && n != 1 {
		return service.Fail("feishu_registration_changed", 409, "扫码登记已变化")
	}
	return e
}
func (t *setupTx) AgentPreset(ctx context.Context, id string) (string, string, error) {
	var name, description string
	e := t.tx.QueryRowContext(ctx, `SELECT name,left(role,500) FROM platform_agents WHERE workspace_id=$1 AND id=$2`, t.workspace, id).Scan(&name, &description)
	return name, description, e
}

// Hold the requesting administrator's membership through guarded state writes.
func (t *setupTx) Member(ctx context.Context, a string) (service.Member, error) {
	var m service.Member
	e := t.tx.QueryRowContext(ctx, `SELECT user_id,role,status FROM platform_members WHERE workspace_id=$1 AND user_id=$2 FOR SHARE`, t.workspace, a).Scan(&m.ID, &m.Role, &m.Status)
	if errors.Is(e, sql.ErrNoRows) {
		return m, service.Fail("workspace_forbidden", 403, "没有渠道管理权限")
	}
	return m, e
}
