package service

import "context"

// Nil ConversationScope preserves the exact legacy evaluator, including snapshots.
type PackageConversationScope struct {
	Private PackageConversationRule `json:"private"`
	Group   PackageConversationRule `json:"group"`
}
type PackageConversationRule struct {
	Enabled bool            `json:"enabled"`
	Mode    string          `json:"mode"`
	Allow   []UsageSelector `json:"allow"`
	Deny    []UsageSelector `json:"deny"`
}

func normalizePackageConversationScope(p *CapabilityPackage) error {
	if p.ConversationScope == nil {
		return nil
	}
	for i, rule := range []*PackageConversationRule{&p.ConversationScope.Private, &p.ConversationScope.Group} {
		if rule.Mode != "inherit_with_blacklist" && rule.Mode != "allowlist" {
			return Fail("invalid_package_conversation_scope", 400, "会话范围模式无效")
		}
		if rule.Mode == "allowlist" && len(rule.Deny) > 0 || rule.Mode == "inherit_with_blacklist" && len(rule.Allow) > 0 {
			return Fail("invalid_package_conversation_scope", 400, "范围模式与名单不匹配")
		}
		if rule.Allow == nil {
			rule.Allow = []UsageSelector{}
		}
		if rule.Deny == nil {
			rule.Deny = []UsageSelector{}
		}
		for _, list := range [][]UsageSelector{rule.Allow, rule.Deny} {
			if len(list) > 200 {
				return Fail("invalid_package_conversation_scope", 400, "会话名单过长")
			}
			seen := map[UsageSelector]bool{}
			for _, ref := range list {
				if !validUsageSelector(ref) || i == 0 && ref.Kind == "group" || seen[ref] {
					return Fail("invalid_package_conversation_scope", 400, "会话名单引用无效")
				}
				seen[ref] = true
			}
		}
	}
	return nil
}
func validateConversationReferences(ctx context.Context, t PackageTx, p CapabilityPackage, old *PackageConversationScope) error {
	if p.ConversationScope == nil {
		return nil
	}
	var previous []PackageConversationRule
	if old != nil {
		previous = []PackageConversationRule{old.Private, old.Group}
	}
	for scene, r := range []PackageConversationRule{p.ConversationScope.Private, p.ConversationScope.Group} {
		for side, list := range [][]UsageSelector{r.Allow, r.Deny} {
			prior := map[UsageSelector]bool{}
			if old != nil {
				for _, ref := range [][]UsageSelector{previous[scene].Allow, previous[scene].Deny}[side] {
					prior[ref] = true
				}
			}
			for _, ref := range list {
				if prior[ref] {
					continue
				}
				if e := validateConversationReference(ctx, t, p.AgentID, ref); e != nil {
					return e
				}
			}
		}
	}
	return nil
}
func validateConversationReference(ctx context.Context, t PackageTx, agent string, ref UsageSelector) error {
	if ref.Channel == "web" && ref.Kind == "user" {
		m, e := t.Member(ctx, ref.ID)
		if e != nil {
			return e
		}
		if m.Status != "active" {
			return Fail("package_member_unavailable", 409, "成员不可用")
		}
		return nil
	}
	if agent == "" {
		return Fail("invalid_package_conversation_scope", 400, "渠道或群名单必须绑定具体员工")
	}
	v, ok := t.(packageSelectorValidator)
	if !ok {
		return Fail("invalid_package_conversation_scope", 409, "无法验证名单")
	}
	return v.ValidatePackageSelector(ctx, agent, ref)
}
