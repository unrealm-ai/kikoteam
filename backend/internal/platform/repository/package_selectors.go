package repository

import (
	"context"
	"encoding/json"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func (t *packageTx) ValidatePackageSelector(ctx context.Context, agent string, ref service.UsageSelector) error {
	valid, err := t.packageSelectorKnown(ctx, agent, ref)
	if err != nil {
		return err
	}
	if !valid {
		return service.Fail("invalid_package_selector", 409, "能力包名单身份已失效或不属于员工渠道")
	}
	return nil
}

// New group grants accept the same employee-scoped provider evidence as the
// group picker. An arbitrary Web group or a former provider group is not viable.
func (t *packageTx) packageSelectorKnown(ctx context.Context, agent string, ref service.UsageSelector) (bool, error) {
	if ref.Kind != "group" {
		return usageReferenceKnown(ctx, t.model.tx, t.model.workspace, agent, ref)
	}
	var valid bool
	err := t.model.tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM (`+groupSelectionSourcesSQL+`) source
 WHERE provider=$3 AND binding=$4 AND identity_epoch=$5 AND external_id=$6)`,
		t.model.workspace, agent, ref.Channel, ref.BindingID, ref.IdentityEpoch, ref.ID).Scan(&valid)
	return valid, err
}

// PackageScopeAllows is an additional restriction after employee admission. It
// shares directory unknown semantics: unknown deny rejects, unknown allow does
// not grant. The caller must still validate the active execution Input.
func (t *packageTx) PackageScopeAllows(ctx context.Context, p service.CapabilityPackage, actor string) (bool, error) {
	if _, work := service.WorkExecutionFrom(ctx); work {
		return service.PackageAllowsExecution(ctx, p, actor), nil
	}
	if _, trigger := service.TriggerExecutionFrom(ctx); trigger {
		return service.PackageAllowsExecution(ctx, p, actor), nil
	}
	if p.ConversationScope != nil {
		return t.PackageConversationScopeAllows(ctx, p, actor)
	}
	c, channel := service.ChannelExecutionFrom(ctx)
	if !channel || len(p.Scope.Allow) == 0 && len(p.Scope.Deny) == 0 {
		return service.PackageAllowsExecution(ctx, p, actor), nil
	}
	if !p.Enabled || c.PrincipalID != actor {
		return false, nil
	}
	mode := "allowlist"
	if p.Scope.Mode == "inherit_with_blacklist" {
		mode = "all"
	} else if p.Scope.Mode != "whitelist_with_blacklist" {
		return false, nil
	}
	config := map[string]any{"access": service.UsageAccess{Mode: mode, Allow: p.Scope.Allow, Deny: p.Scope.Deny}}
	raw, err := json.Marshal(config)
	if err != nil {
		return false, err
	}
	var allowed bool
	err = t.model.tx.QueryRowContext(ctx, `SELECT EXISTS(
 SELECT 1 FROM manager_channel_principals p
 JOIN platform_channel_bindings b ON b.id=p.binding_id AND b.workspace_id=p.workspace_id
 JOIN manager_conversations c ON c.id=$3 AND c.channel_binding_id=b.id AND c.workspace_id=p.workspace_id
 CROSS JOIN LATERAL (SELECT platform_agent_usage_projection($6::jsonb,b.provider,b.id,b.identity_epoch)->'access' access) policy
 WHERE p.id=$1 AND p.workspace_id=$2 AND b.agent_id=$4 AND b.enabled
 AND b.connection_generation=$5 AND b.identity_epoch=p.identity_epoch
 AND b.external_tenant_id<>'' AND p.external_tenant_id=b.external_tenant_id AND c.channel_tenant_id=b.external_tenant_id
 AND c.channel_identity_epoch=b.identity_epoch AND (b.provider='feishu' OR b.identity_verified_generation=b.connection_generation)
 AND NOT (policy.access->'deny_ids' ? p.external_user_id)
 AND NOT COALESCE(platform_channel_directory_department_match(b.id,p.external_user_id,policy.access->'deny_department_rules'),true)
 AND (policy.access->>'mode'='all' OR policy.access->'allow_ids' ? p.external_user_id
 OR COALESCE(platform_channel_directory_department_match(b.id,p.external_user_id,policy.access->'allow_department_rules'),false)))`, actor, t.model.workspace, c.ConversationID, p.AgentID, c.Generation, raw).Scan(&allowed)
	return allowed, err
}
