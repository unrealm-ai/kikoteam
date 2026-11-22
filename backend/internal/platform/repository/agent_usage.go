package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type AgentUsageStore struct{ DB *sql.DB }

func usageAdmin(ctx context.Context, tx *sql.Tx, w, actor string) error {
	var allowed bool
	err := tx.QueryRowContext(ctx, `SELECT true FROM platform_members WHERE workspace_id=$1 AND user_id=$2 AND status='active' AND role IN ('owner','admin') FOR SHARE`, w, actor).Scan(&allowed)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !allowed {
		return service.Fail("workspace_forbidden", 403, "没有员工管理权限")
	}
	return err
}
func loadAgentUsage(ctx context.Context, tx *sql.Tx, w, id string) (service.AgentUsageResource, error) {
	var v service.AgentUsageResource
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT usage_version,usage_config FROM platform_agents WHERE workspace_id=$1 AND id=$2 FOR UPDATE`, w, id).Scan(&v.Version, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return v, service.Fail("agent_not_found", 404, "员工不存在")
	}
	if err == nil {
		err = json.Unmarshal(raw, &v.UsageConfig)
	}
	return v, err
}
func (s *AgentUsageStore) ReadUsage(ctx context.Context, w, actor, id string) (service.AgentUsageResource, error) {
	var out service.AgentUsageResource
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	if err = usageAdmin(ctx, tx, w, actor); err != nil {
		return out, err
	}
	out, err = loadAgentUsage(ctx, tx, w, id)
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}
func (s *AgentUsageStore) ReplaceUsage(ctx context.Context, w, actor, id string, v service.AgentUsageResource) (service.AgentUsageResource, error) {
	var out service.AgentUsageResource
	if err := service.ValidateAgentUsage(v.UsageConfig); err != nil {
		return out, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	// Same global ordering as channel configuration, then agent, then bindings.
	if _, err = tx.ExecContext(ctx, `SELECT id FROM platform_channel_config_clock WHERE id=true FOR UPDATE`); err != nil {
		return out, err
	}
	if err = usageAdmin(ctx, tx, w, actor); err != nil {
		return out, err
	}
	old, err := loadAgentUsage(ctx, tx, w, id)
	if err != nil {
		return out, err
	}
	if old.Version != v.Version {
		return out, service.Fail("usage_version_conflict", 409, "使用配置已更新，请刷新后重试")
	}
	if err = validateUsageAccessReplacement(old.UsageConfig.Access, v.UsageConfig.Access, v.ReplaceLegacyAccess); err != nil {
		return out, err
	}
	if err = validateUsageReferences(ctx, tx, w, id, old.UsageConfig, v.UsageConfig); err != nil {
		return out, err
	}
	raw, err := json.Marshal(v.UsageConfig)
	if err != nil {
		return out, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE platform_agents SET usage_config=$3,usage_version=usage_version+1,version=version+1,admission_epoch=admission_epoch+$4,updated_at=now() WHERE workspace_id=$1 AND id=$2`, w, id, raw, usageAccessFence(old.UsageConfig.Access, v.UsageConfig.Access)); err != nil {
		return out, err
	}
	// Projection hook is supplied with the policy implementation before this API
	// is registered. Keep the transaction atomic across policy and Hub revisions.
	if err = RefreshChannelUsageTx(ctx, tx, w, id, raw); err != nil {
		return out, err
	}
	out = v
	out.ReplaceLegacyAccess = false
	out.Version++
	return out, tx.Commit()
}
func validateUsageReferences(ctx context.Context, tx *sql.Tx, w, agent string, old, next service.AgentUsage) error {
	oldLists := [][]service.UsageSelector{old.Access.Allow, old.Access.Deny}
	for index, list := range [][]service.UsageSelector{next.Access.Allow, next.Access.Deny} {
		existing := map[service.UsageSelector]bool{}
		for _, v := range oldLists[index] {
			existing[v] = true
		}
		for _, v := range list {
			// Retain stale selectors for truthful display/removal. SQL policy evaluates
			// current binding/epoch and never resurrects a stale grant.
			if existing[v] {
				continue
			}
			allowed, err := usageReferenceKnown(ctx, tx, w, agent, v)
			if err != nil {
				return err
			}
			if !allowed {
				return service.Fail("invalid_usage_selector", 400, "名单对象不属于当前员工渠道或身份已失效")
			}
		}
	}
	return nil
}
func usageReferenceKnown(ctx context.Context, tx *sql.Tx, w, agent string, v service.UsageSelector) (bool, error) {
	var yes bool
	if v.Channel == "web" {
		if v.Kind == "user" {
			err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM platform_members WHERE workspace_id=$1 AND user_id=$2 AND status='active')`, w, v.ID).Scan(&yes)
			return yes, err
		}
		err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM platform_web_groups WHERE workspace_id=$1 AND id=$2 AND status='active')`, w, v.ID).Scan(&yes)
		return yes, err
	}
	var generation int64
	err := tx.QueryRowContext(ctx, `SELECT connection_generation FROM platform_channel_bindings WHERE id=$1 AND workspace_id=$2 AND agent_id=$3 AND provider=$4 AND identity_epoch=$5 AND external_tenant_id<>'' AND (provider='feishu' OR identity_verified_generation=connection_generation) FOR SHARE`, v.BindingID, w, agent, v.Channel, v.IdentityEpoch).Scan(&generation)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if v.Kind == "group" {
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM platform_channel_observed_chats WHERE binding_id=$1 AND identity_epoch=$2 AND external_chat_id=$3 AND external_tenant_id=(SELECT external_tenant_id FROM platform_channel_bindings WHERE id=$1))`, v.BindingID, v.IdentityEpoch, v.ID).Scan(&yes)
		return yes, err
	}
	if v.Channel == "wecom" {
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM platform_wecom_identity_mappings WHERE binding_id=$1 AND identity_epoch=$2 AND connection_generation=$3 AND callback_user_id=$4)`, v.BindingID, v.IdentityEpoch, generation, v.ID).Scan(&yes)
		return yes, err
	}
	condition := `EXISTS(SELECT 1 FROM platform_channel_directory_users u WHERE u.binding_id=d.binding_id AND u.user_id=$4 AND u.status='active')`
	if v.Kind == "department" {
		condition = `EXISTS(SELECT 1 FROM platform_channel_directory_departments p WHERE p.binding_id=d.binding_id AND p.department_id=$4)`
	}
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM platform_channel_directory_state d WHERE d.binding_id=$1 AND d.identity_epoch=$2 AND d.connection_generation=$3 AND d.full_snapshot AND d.expires_at>now() AND d.external_tenant_id=(SELECT external_tenant_id FROM platform_channel_bindings WHERE id=$1) AND `+condition+`)`, v.BindingID, v.IdentityEpoch, generation, v.ID).Scan(&yes)
	return yes, err
}

// Trigger/history editing and selector reordering do not cancel queued inputs.
// Actual access-policy changes fence pre-change admission; current permissions
// are also rechecked by Manager and immediately before sending.
func usageAccessFence(old, next service.UsageAccess) int {
	if old.Mode != next.Mode || old.GroupMembersEnabled != next.GroupMembersEnabled {
		return 1
	}
	for index, list := range [][]service.UsageSelector{old.Allow, old.Deny} {
		other := [][]service.UsageSelector{next.Allow, next.Deny}[index]
		if len(list) != len(other) {
			return 1
		}
		found := map[service.UsageSelector]bool{}
		for _, v := range list {
			found[v] = true
		}
		for _, v := range other {
			if !found[v] {
				return 1
			}
		}
	}
	return 0
}

// Legacy restrictions are never silently removed by a newer editor. Compare
// sets rather than ordering so unrelated edits and harmless reordering work.
func validateUsageAccessReplacement(old, next service.UsageAccess, replace bool) error {
	if !service.SimplifiedUsageAccess(old) {
		if !replace && usageAccessFence(old, next) == 0 {
			return nil
		}
		if !replace {
			return service.Fail("usage_policy_requires_review", 409, "旧使用范围包含新模式无法表示的规则，请复核并明确确认替换")
		}
	} else if replace {
		return service.Fail("invalid_legacy_access_replacement", 400, "当前使用范围无需旧规则迁移，请刷新后保存")
	}
	if !service.SimplifiedUsageAccess(next) {
		return service.Fail("invalid_usage_config", 400, "使用范围仅支持全员加人员黑名单或纯人员白名单，不支持群名单")
	}
	return nil
}
