package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type ChannelStore struct{ DB *sql.DB }

func NewChannelStore(db *sql.DB) *ChannelStore { return &ChannelStore{db} }

type channelTx struct {
	tx        *sql.Tx
	workspace string
}

func (s *ChannelStore) WithWorkspace(ctx context.Context, w string, fn func(service.ChannelTx) error) error {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback() //nolint:errcheck // Commit owns success.
	if _, e = tx.ExecContext(ctx, `SELECT id FROM platform_channel_config_clock WHERE id=true FOR UPDATE`); e != nil {
		return e
	}
	if e = fn(&channelTx{tx, w}); e != nil {
		return e
	}
	return tx.Commit()
}
func (t *channelTx) Member(ctx context.Context, a string) (service.Member, error) {
	var m service.Member
	e := t.tx.QueryRowContext(ctx, `SELECT user_id,role,status FROM platform_members WHERE workspace_id=$1 AND user_id=$2`, t.workspace, a).Scan(&m.ID, &m.Role, &m.Status)
	if errors.Is(e, sql.ErrNoRows) {
		return m, service.Fail("workspace_forbidden", 403, "没有渠道管理权限")
	}
	return m, e
}
func (t *channelTx) AgentExists(ctx context.Context, a string) error {
	var id string
	e := t.tx.QueryRowContext(ctx, `SELECT id FROM platform_agents WHERE workspace_id=$1 AND id=$2`, t.workspace, a).Scan(&id)
	if errors.Is(e, sql.ErrNoRows) {
		return service.Fail("agent_not_found", 404, "员工不存在")
	}
	return e
}

const channelColumns = `id,workspace_id,agent_id,provider,connection_generation,rules_version,config_revision,enabled,app_id,external_tenant_id,bot_open_id,credential_ciphertext,trigger,history,access,applied_generation,applied_rules_version,applied_revision,applied_status,applied_error,identity_epoch,identity_verified_generation`

func scanChannel(row scanner) (service.ChannelBinding, error) {
	var b service.ChannelBinding
	var tr, hi, ac []byte
	e := row.Scan(&b.ID, &b.WorkspaceID, &b.AgentID, &b.Provider, &b.Generation, &b.RulesVersion, &b.Revision, &b.Enabled, &b.AppID, &b.TenantID, &b.BotID, &b.Ciphertext, &tr, &hi, &ac, &b.Applied.Generation, &b.Applied.RulesVersion, &b.Applied.Revision, &b.Applied.Status, &b.Applied.ErrorCode, &b.IdentityEpoch, &b.IdentityVerifiedGeneration)
	if errors.Is(e, sql.ErrNoRows) {
		return b, service.Fail("channel_not_configured", 404, "尚未配置飞书渠道")
	}
	if e != nil {
		return b, e
	}
	for _, v := range []struct {
		data   []byte
		target any
	}{{tr, &b.Trigger}, {hi, &b.History}, {ac, &b.Access}} {
		if e = json.Unmarshal(v.data, v.target); e != nil {
			return b, e
		}
	}
	if e = service.NormalizeDepartmentRules(&b.Access); e != nil {
		return b, e
	}
	service.SetChannelIdentityPresentation(&b)
	b.HasSecret = len(b.Ciphertext) > 0
	return b, nil
}
func (t *channelTx) Get(ctx context.Context, a string) (service.ChannelBinding, error) {
	return t.GetProvider(ctx, a, "feishu")
}
func (t *channelTx) GetProvider(ctx context.Context, a, provider string) (service.ChannelBinding, error) {
	return scanChannel(t.tx.QueryRowContext(ctx, `SELECT `+channelColumns+` FROM platform_channel_bindings WHERE workspace_id=$1 AND agent_id=$2 AND provider=$3`, t.workspace, a, provider))
}

func (s *ChannelStore) WithBindingRead(ctx context.Context, w string, fn func(service.ChannelTx) error) error {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	if err = fn(&channelTx{tx, w}); err != nil {
		return err
	}
	return tx.Commit()
}

func (t *channelTx) Save(ctx context.Context, b service.ChannelBinding, create bool) error {
	if b.Enabled {
		var duplicate bool
		if e := t.tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM platform_channel_bindings WHERE provider=$1 AND app_id=$2 AND enabled AND id<>$3)`, b.Provider, b.AppID, b.ID).Scan(&duplicate); e != nil {
			return e
		}
		if duplicate {
			return service.Fail("channel_app_binding_ambiguous", 409, "当前接入暂不支持同一应用同时连接多个员工，请先停用原连接")
		}
	}

	tr, e := json.Marshal(b.Trigger)
	if e != nil {
		return e
	}
	hi, e := json.Marshal(b.History)
	if e != nil {
		return e
	}
	ac, e := json.Marshal(b.Access)
	if e != nil {
		return e
	}
	args := []any{b.ID, b.WorkspaceID, b.AgentID, b.Provider, b.Generation, b.RulesVersion, b.Revision, b.Enabled, b.AppID, b.TenantID, b.BotID, b.Ciphertext, tr, hi, ac, b.IdentityEpoch}
	if create {
		_, e = t.tx.ExecContext(ctx, `INSERT INTO platform_channel_bindings(id,workspace_id,agent_id,provider,connection_generation,rules_version,config_revision,enabled,app_id,external_tenant_id,bot_open_id,credential_ciphertext,trigger,history,access,identity_epoch) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`, args...)
	} else {
		_, e = t.tx.ExecContext(ctx, `UPDATE platform_channel_bindings SET connection_generation=$5,rules_version=$6,config_revision=$7,enabled=$8,app_id=$9,external_tenant_id=$10,bot_open_id=$11,credential_ciphertext=$12,trigger=$13,history=$14,access=$15,identity_epoch=$16,applied_status='pending',applied_error='',updated_at=now() WHERE id=$1 AND workspace_id=$2 AND agent_id=$3 AND provider=$4`, args...)
	}
	if e != nil {
		return e
	}
	if create && (b.Provider == "feishu" || b.Provider == "wecom") {
		if e = t.initializeChannelHistory(ctx, b); e != nil {
			return e
		}
	}
	if b.Provider == "feishu" {
		if _, e = t.tx.ExecContext(ctx, `INSERT INTO platform_feishu_permission_checks(binding_id,connection_generation) VALUES($1,$2)
			ON CONFLICT (binding_id) DO UPDATE SET connection_generation=EXCLUDED.connection_generation,
			diagnostics=NULL,next_check_at=now(),lease_token=NULL,lease_until=NULL
			WHERE platform_feishu_permission_checks.connection_generation<EXCLUDED.connection_generation`, b.ID, b.Generation); e != nil {
			return e
		}
	}
	_, e = t.tx.ExecContext(ctx, `UPDATE platform_channel_config_clock SET revision=revision+1 WHERE id=true`)
	return e
}
func (s *ChannelStore) Snapshot(ctx context.Context) (int64, []service.ChannelBinding, error) {
	tx, e := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if e != nil {
		return 0, nil, e
	}
	defer tx.Rollback() //nolint:errcheck // Read transaction committed below.
	var revision int64
	if e = tx.QueryRowContext(ctx, `SELECT revision FROM platform_channel_config_clock WHERE id=true`).Scan(&revision); e != nil {
		return 0, nil, e
	}
	rows, e := tx.QueryContext(ctx, `SELECT `+channelColumns+` FROM platform_channel_bindings ORDER BY id`)
	if e != nil {
		return 0, nil, e
	}
	defer rows.Close() //nolint:errcheck // rows.Err checked.
	out := []service.ChannelBinding{}
	for rows.Next() {
		b, e := scanChannel(rows)
		if e != nil {
			return 0, nil, e
		}
		out = append(out, b)
	}
	if e = rows.Err(); e != nil {
		return 0, nil, e
	}
	return revision, out, tx.Commit()
}
func (s *ChannelStore) Applied(ctx context.Context, id string, a service.ChannelApplied) error {
	r, e := s.DB.ExecContext(ctx, `UPDATE platform_channel_bindings SET applied_generation=$2,applied_rules_version=$3,applied_revision=$4,applied_status=$5,applied_error=$6,identity_verified_generation=CASE WHEN provider='feishu' AND $7<>'' AND $8<>'' THEN $2 ELSE identity_verified_generation END,external_tenant_id=CASE WHEN external_tenant_id='' THEN $7 ELSE external_tenant_id END,bot_open_id=CASE WHEN bot_open_id='' THEN $8 ELSE bot_open_id END WHERE id=$1 AND connection_generation=$2 AND rules_version=$3 AND config_revision=$4 AND (provider='feishu' OR ($7='' AND $8='')) AND ($7='' OR external_tenant_id='' OR external_tenant_id=$7) AND ($8='' OR bot_open_id='' OR bot_open_id=$8)`, id, a.Generation, a.RulesVersion, a.Revision, a.Status, a.ErrorCode, a.ObservedTenantID, a.ObservedBotID)
	if e != nil {
		return e
	}
	n, e := r.RowsAffected()
	if e == nil && n != 1 {
		return service.Fail("channel_version_conflict", 409, "渠道状态版本或身份已变化")
	}
	return e
}
func (s *ChannelStore) PendingNotification(ctx context.Context) (int64, error) {
	var v int64
	e := s.DB.QueryRowContext(ctx, `SELECT CASE WHEN revision>notified_revision THEN revision ELSE 0 END FROM platform_channel_config_clock WHERE id=true`).Scan(&v)
	return v, e
}
func (s *ChannelStore) MarkNotified(ctx context.Context, v int64) error {
	_, e := s.DB.ExecContext(ctx, `UPDATE platform_channel_config_clock SET notified_revision=GREATEST(notified_revision,LEAST(revision,$1)) WHERE id=true`, v)
	return e
}
