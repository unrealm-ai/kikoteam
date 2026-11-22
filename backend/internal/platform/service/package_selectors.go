package service

import "context"

func normalizePackageSelectors(s *PackageScope) error {
	if s.Allow == nil {
		s.Allow = []UsageSelector{}
	}
	if s.Deny == nil {
		s.Deny = []UsageSelector{}
	}
	for _, list := range [][]UsageSelector{s.Allow, s.Deny} {
		if len(list) > 200 {
			return Fail("invalid_package_scope", 400, "能力包名单过长")
		}
		seen := map[UsageSelector]bool{}
		for _, v := range list {
			if !validUsageSelector(v) || v.Kind == "group" || seen[v] {
				return Fail("invalid_package_scope", 400, "能力包人员或部门引用无效")
			}
			seen[v] = true
		}
	}
	return nil
}

type packageSelectorValidator interface {
	ValidatePackageSelector(context.Context, string, UsageSelector) error
}

func validatePackageSelectors(ctx context.Context, t PackageTx, p CapabilityPackage) error {
	for _, list := range [][]UsageSelector{p.Scope.Allow, p.Scope.Deny} {
		for _, ref := range list {
			if ref.Channel == "web" {
				m, err := t.Member(ctx, ref.ID)
				if err != nil {
					return err
				}
				if m.Status != "active" {
					return Fail("package_member_unavailable", 409, "成员已不可用")
				}
				continue
			}
			if p.AgentID == "" {
				return Fail("invalid_package_scope", 400, "渠道名单必须绑定具体员工")
			}
			validator, ok := t.(packageSelectorValidator)
			if !ok {
				return Fail("invalid_package_scope", 400, "无法验证渠道名单")
			}
			if err := validator.ValidatePackageSelector(ctx, p.AgentID, ref); err != nil {
				return err
			}
		}
	}
	return nil
}

// Package scope evaluation uses current trusted source facts, never submitted selector IDs.
type packageScopeEvaluator interface {
	PackageScopeAllows(context.Context, CapabilityPackage, string) (bool, error)
}

func packageScopeAllows(ctx context.Context, t PackageTx, p CapabilityPackage, actor string) (bool, error) {
	if _, work := WorkExecutionFrom(ctx); work {
		return PackageAllowsExecution(ctx, p, actor), nil
	}
	if _, trigger := TriggerExecutionFrom(ctx); trigger {
		return PackageAllowsExecution(ctx, p, actor), nil
	}
	if _, channel := ChannelExecutionFrom(ctx); p.ConversationScope != nil || channel && (len(p.Scope.Allow) > 0 || len(p.Scope.Deny) > 0) {
		evaluator, ok := t.(packageScopeEvaluator)
		if !ok {
			return false, nil
		}
		return evaluator.PackageScopeAllows(ctx, p, actor)
	}
	return PackageAllowsExecution(ctx, p, actor), nil
}
func (s *PackageService) runtimeScopeAllows(ctx context.Context, w, actor, employee string, p CapabilityPackage) (bool, error) {
	var allowed bool
	err := s.Store.WithWorkspace(ctx, w, func(t PackageTx) error {
		if err := authorizeRuntime(ctx, t, actor, employee, func() error { return packageAuthorize(ctx, t, actor, false) }); err != nil {
			return err
		}
		var err error
		allowed, err = packageScopeAllows(ctx, t, p, actor)
		return err
	})
	return allowed, err
}

// Existing stale refs may remain for display/removal, but cannot gain new grants.
func validatePackageSelectorChanges(ctx context.Context, t PackageTx, p CapabilityPackage, old PackageScope) error {
	next := p
	next.Scope.Allow = nil
	next.Scope.Deny = nil
	for i, list := range [][]UsageSelector{p.Scope.Allow, p.Scope.Deny} {
		prior := [][]UsageSelector{old.Allow, old.Deny}[i]
		seen := map[UsageSelector]bool{}
		for _, v := range prior {
			seen[v] = true
		}
		for _, v := range list {
			if !seen[v] {
				next.Scope.Allow = append(next.Scope.Allow, v)
			}
		}
	}
	return validatePackageSelectors(ctx, t, next)
}
