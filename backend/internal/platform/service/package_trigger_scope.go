package service

import (
	"context"
	"slices"
)

type PackageTriggerScope struct {
	Enabled         *bool    `json:"enabled,omitempty"`
	Mode            string   `json:"mode"`
	AllowTriggerIDs []string `json:"allow_trigger_ids"`
	DenyTriggerIDs  []string `json:"deny_trigger_ids"`
}

func DefaultPackageTriggerScope() PackageTriggerScope {
	return PackageTriggerScope{Mode: "all", AllowTriggerIDs: []string{}, DenyTriggerIDs: []string{}}
}
func normalizePackageTriggerScope(p *CapabilityPackage) error {
	if p.TriggerScope.Mode == "" {
		if len(p.TriggerScope.AllowTriggerIDs) > 0 || len(p.TriggerScope.DenyTriggerIDs) > 0 {
			return Fail("invalid_package_trigger_scope", 400, "触发器范围缺少模式")
		}
		enabled := p.TriggerScope.Enabled
		p.TriggerScope = DefaultPackageTriggerScope()
		p.TriggerScope.Enabled = enabled
	}
	s := &p.TriggerScope
	if s.Mode != "all" && s.Mode != "allowlist" || !packageIDs(s.AllowTriggerIDs) || !packageIDs(s.DenyTriggerIDs) ||
		s.Mode == "all" && len(s.AllowTriggerIDs) > 0 || s.Mode == "allowlist" && len(s.DenyTriggerIDs) > 0 {
		return Fail("invalid_package_trigger_scope", 400, "触发器使用范围无效")
	}
	if p.AgentID == "" && (len(s.AllowTriggerIDs) > 0 || len(s.DenyTriggerIDs) > 0) {
		return Fail("invalid_package_trigger_scope", 400, "模板不能引用员工触发器")
	}
	if s.AllowTriggerIDs == nil {
		s.AllowTriggerIDs = []string{}
	}
	if s.DenyTriggerIDs == nil {
		s.DenyTriggerIDs = []string{}
	}
	return nil
}
func PackageAllowsTrigger(p CapabilityPackage, id string) bool {
	if !p.Enabled || id == "" || p.TriggerScope.Enabled != nil && !*p.TriggerScope.Enabled {
		return false
	}
	s := p.TriggerScope
	// Snapshots predating trigger support have the same explicit default as new packages.
	if s.Mode == "" {
		if len(s.AllowTriggerIDs) > 0 || len(s.DenyTriggerIDs) > 0 {
			return false
		}
		s = DefaultPackageTriggerScope()
	}
	switch s.Mode {
	case "all":
		return len(s.AllowTriggerIDs) == 0 && !slices.Contains(s.DenyTriggerIDs, id)
	case "allowlist":
		return len(s.DenyTriggerIDs) == 0 && slices.Contains(s.AllowTriggerIDs, id)
	default:
		return false
	}
}

type packageTriggerValidator interface {
	ValidateTrigger(context.Context, string, string) error
}

func validatePackageTriggers(ctx context.Context, t PackageTx, p CapabilityPackage) error {
	ids := append(append([]string{}, p.TriggerScope.AllowTriggerIDs...), p.TriggerScope.DenyTriggerIDs...)
	if len(ids) == 0 {
		return nil
	}
	v, ok := t.(packageTriggerValidator)
	if !ok {
		return Fail("package_trigger_unavailable", 409, "触发器引用不可验证")
	}
	for _, id := range ids {
		if err := v.ValidateTrigger(ctx, p.AgentID, id); err != nil {
			return err
		}
	}
	return nil
}
