package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type DingTalkSetupStore struct{ DB *sql.DB }
type dingSetupTx struct{ *channelTx }

func (s *DingTalkSetupStore) DueDingTalkRegistrations(ctx context.Context) ([]service.DingTalkRegistration, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+dingSetupCols+` FROM platform_dingtalk_registrations r
		WHERE state IN ('awaiting_confirmation','ready') AND (record->>'expires_at')::timestamptz>now()
		AND (state='ready' OR (record->>'next_poll_at')::timestamptz<=now())
		AND EXISTS (SELECT 1 FROM platform_members m WHERE m.workspace_id=r.workspace_id AND m.user_id=r.actor_id
		AND m.status='active' AND platform_can_manage_agent(r.workspace_id,r.agent_id,m.user_id))
		AND NOT EXISTS (SELECT 1 FROM platform_channel_bindings b WHERE b.agent_id=r.agent_id AND b.workspace_id=r.workspace_id AND b.provider='dingtalk')
		ORDER BY updated_at,id LIMIT 4`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Rows.Err reports failures from iterating the read-only query.
	var due []service.DingTalkRegistration
	for rows.Next() {
		r, err := scanDingSetup(rows)
		if err != nil {
			return nil, err
		}
		due = append(due, r)
	}
	return due, rows.Err()
}

func (s *DingTalkSetupStore) WithSetup(ctx context.Context, w string, fn func(service.DingTalkSetupTx) error) error {
	return (&ChannelStore{DB: s.DB}).WithWorkspace(ctx, w, func(t service.ChannelTx) error { return fn(&dingSetupTx{t.(*channelTx)}) })
}

const dingSetupCols = `id,workspace_id,agent_id,actor_id,operation_key,version,record,device_ciphertext,secret_ciphertext`

func scanDingSetup(row scanner) (service.DingTalkRegistration, error) {
	var r service.DingTalkRegistration
	var data []byte
	err := row.Scan(&r.ID, &r.WorkspaceID, &r.AgentID, &r.ActorID, &r.OperationKey, &r.Version, &data, &r.DeviceCipher, &r.SecretCipher)
	if errors.Is(err, sql.ErrNoRows) {
		return r, service.Fail("dingtalk_registration_not_found", 404, "扫码登记不存在")
	}
	if err != nil {
		return r, err
	}
	err = json.Unmarshal(data, &r)
	return r, err
}
func (t *dingSetupTx) Registration(ctx context.Context, id string) (service.DingTalkRegistration, error) {
	return scanDingSetup(t.tx.QueryRowContext(ctx, `SELECT `+dingSetupCols+` FROM platform_dingtalk_registrations WHERE workspace_id=$1 AND id=$2 FOR UPDATE`, t.workspace, id))
}
func (t *dingSetupTx) LatestRegistration(ctx context.Context, agent, actor string) (*service.DingTalkRegistration, error) {
	r, e := scanDingSetup(t.tx.QueryRowContext(ctx, `SELECT `+dingSetupCols+` FROM platform_dingtalk_registrations WHERE workspace_id=$1 AND agent_id=$2 AND actor_id=$3 ORDER BY created_at DESC,id DESC LIMIT 1`, t.workspace, agent, actor))
	return optionalDingSetup(r, e)
}
func (t *dingSetupTx) RegistrationByKey(ctx context.Context, agent, key string) (*service.DingTalkRegistration, error) {
	r, e := scanDingSetup(t.tx.QueryRowContext(ctx, `SELECT `+dingSetupCols+` FROM platform_dingtalk_registrations WHERE workspace_id=$1 AND agent_id=$2 AND operation_key=$3`, t.workspace, agent, key))
	return optionalDingSetup(r, e)
}
func optionalDingSetup(r service.DingTalkRegistration, e error) (*service.DingTalkRegistration, error) {
	var se *service.Error
	if errors.As(e, &se) && se.Code == "dingtalk_registration_not_found" {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	return &r, nil
}
func (t *dingSetupTx) InsertRegistration(ctx context.Context, r service.DingTalkRegistration) error {
	// Expire abandoned device challenges before testing the one-active rule.
	if _, e := t.tx.ExecContext(ctx, `UPDATE platform_dingtalk_registrations SET state='expired',record=jsonb_set(jsonb_set(record,'{state}','"expired"'),'{error_code}','"dingtalk_registration_expired"'),device_ciphertext=NULL,secret_ciphertext=NULL,version=version+1,updated_at=now() WHERE workspace_id=$1 AND agent_id=$2 AND state IN ('starting','awaiting_confirmation','polling','ready') AND (record->>'expires_at')::timestamptz <= now()`, t.workspace, r.AgentID); e != nil {
		return e
	}
	var recent int
	if e := t.tx.QueryRowContext(ctx, `SELECT count(*) FROM platform_dingtalk_registrations WHERE actor_id=$1 AND created_at>now()-interval '1 minute'`, r.ActorID).Scan(&recent); e != nil {
		return e
	}
	if recent >= 12 {
		return service.Fail("dingtalk_registration_rate_limited", 429, "扫码登记过于频繁，请稍后重试")
	}

	data, e := json.Marshal(r)
	if e != nil {
		return e
	}
	var busy bool
	if e = t.tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM platform_dingtalk_registrations WHERE workspace_id=$1 AND agent_id=$2 AND state IN ('starting','awaiting_confirmation','polling','ready','credentials_saved','checking','needs_attention'))`, t.workspace, r.AgentID).Scan(&busy); e != nil {
		return e
	}
	if busy {
		return service.Fail("dingtalk_registration_in_progress", 409, "已有扫码登记进行中")
	}
	_, e = t.tx.ExecContext(ctx, `INSERT INTO platform_dingtalk_registrations(id,workspace_id,agent_id,actor_id,operation_key,state,version,record)VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, r.ID, t.workspace, r.AgentID, r.ActorID, r.OperationKey, r.State, r.Version, data)
	return e
}
func (t *dingSetupTx) SaveRegistration(ctx context.Context, r service.DingTalkRegistration) error {
	data, e := json.Marshal(r)
	if e != nil {
		return e
	}
	result, e := t.tx.ExecContext(ctx, `UPDATE platform_dingtalk_registrations SET state=$3,record=$4,device_ciphertext=$5,secret_ciphertext=$6,version=version+1,updated_at=now() WHERE workspace_id=$1 AND id=$2 AND version=$7`, t.workspace, r.ID, r.State, data, r.DeviceCipher, r.SecretCipher, r.Version)
	if e != nil {
		return e
	}
	n, e := result.RowsAffected()
	if e == nil && n != 1 {
		return service.Fail("dingtalk_registration_changed", 409, "扫码登记已变化")
	}
	return e
}

// Hold the requesting administrator's membership through guarded state writes.
func (t *dingSetupTx) Member(ctx context.Context, a string) (service.Member, error) {
	var m service.Member
	e := t.tx.QueryRowContext(ctx, `SELECT user_id,role,status FROM platform_members WHERE workspace_id=$1 AND user_id=$2 FOR SHARE`, t.workspace, a).Scan(&m.ID, &m.Role, &m.Status)
	if errors.Is(e, sql.ErrNoRows) {
		return m, service.Fail("workspace_forbidden", 403, "没有渠道管理权限")
	}
	return m, e
}
