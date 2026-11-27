package service

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// PackageScope restricts employee admission; it never grants employee access.
type PackageScope struct {
	Allow          []UsageSelector `json:"allow"`
	Deny           []UsageSelector `json:"deny"`
	Mode           string          `json:"mode"`
	AllowMemberIDs []string        `json:"allow_member_ids"`
	DenyMemberIDs  []string        `json:"deny_member_ids"`
}
type PackageSkill struct {
	SkillID   string `json:"skill_id"`
	VersionID string `json:"version_id"`
}
type PackagePlugin struct {
	PluginID  string `json:"plugin_id"`
	VersionID string `json:"version_id"`
}
type CapabilityPackage struct {
	WorkScope         *WorkScope                `json:"work_scope,omitempty"`
	ConversationScope *PackageConversationScope `json:"conversation_scope"`
	TriggerScope      PackageTriggerScope       `json:"trigger_scope"`
	ID                string                    `json:"id"`
	AgentID           string                    `json:"agent_id,omitempty"`
	SourceTemplateID  string                    `json:"source_template_id,omitempty"`
	Name              string                    `json:"name"`
	Description       string                    `json:"description"`
	Enabled           bool                      `json:"enabled"`
	Version           int64                     `json:"version"`
	Scope             PackageScope              `json:"scope"`
	Skills            []PackageSkill            `json:"skills"`
	Plugins           []PackagePlugin           `json:"plugins"`
	CreatedAt         time.Time                 `json:"created_at"`
	UpdatedAt         time.Time                 `json:"updated_at"`
}
type PackageCreate struct {
	WorkScope         *WorkScope                `json:"work_scope,omitempty"`
	ConversationScope *PackageConversationScope `json:"conversation_scope,omitempty"`
	TriggerScope      *PackageTriggerScope      `json:"trigger_scope,omitempty"`
	Name              string                    `json:"name"`
	Description       string                    `json:"description"`
	Enabled           *bool                     `json:"enabled,omitempty"`
	Scope             *PackageScope             `json:"scope,omitempty"`
	Skills            []PackageSkill            `json:"skills"`
	Plugins           []PackagePlugin           `json:"plugins"`
}
type PackagePatch struct {
	WorkScope         *WorkScope                `json:"work_scope,omitempty"`
	ConversationScope *PackageConversationScope `json:"conversation_scope,omitempty"`
	TriggerScope      *PackageTriggerScope      `json:"trigger_scope,omitempty"`
	Version           int64                     `json:"version"`
	Name              *string                   `json:"name,omitempty"`
	Description       *string                   `json:"description,omitempty"`
	Enabled           *bool                     `json:"enabled,omitempty"`
	Scope             *PackageScope             `json:"scope,omitempty"`
	Skills            *[]PackageSkill           `json:"skills,omitempty"`
	Plugins           *[]PackagePlugin          `json:"plugins,omitempty"`
}
type PackageCopy struct {
	TemplateVersion int64  `json:"template_version"`
	Name            string `json:"name,omitempty"`
}
type PackagePage struct {
	Items    []CapabilityPackage `json:"items"`
	Total    int                 `json:"total"`
	Page     int                 `json:"page"`
	PageSize int                 `json:"page_size"`
}

// PackageResource describes the current resource state under the workspace lock.
type PackageResource struct {
	CurrentVersionID string
	Unlisted         bool
	Enabled          bool
}
type PackageTx interface {
	Upgrade(context.Context, string, ResourceUpgrade) error
	Member(context.Context, string) (Member, error)
	Employee(context.Context, string) error
	Get(context.Context, string, string) (CapabilityPackage, error)
	List(context.Context, string, int, int) ([]CapabilityPackage, int, error)
	ValidateSkill(context.Context, PackageSkill) (PackageResource, error)
	ValidatePlugin(context.Context, PackagePlugin) (PackageResource, error)
	Save(context.Context, CapabilityPackage, bool) error
}
type PackageStore interface {
	WithWorkspace(context.Context, string, func(PackageTx) error) error
}
type PackageService struct{ Store PackageStore }

func packageAuthorize(ctx context.Context, t PackageTx, actor string, write bool) error {
	m, e := t.Member(ctx, actor)
	if errors.Is(e, ErrWorkspaceMissing) || e == nil && (m.Status != "active" || write && m.Role != "owner" && m.Role != "admin") {
		return Fail("workspace_forbidden", 403, "没有能力包管理权限")
	}
	return e
}
func (s *PackageService) List(ctx context.Context, w, actor, employee string, p, n int) (PackagePage, error) {
	out := PackagePage{Items: []CapabilityPackage{}, Page: p, PageSize: n}
	if e := pageValid(p, n); e != nil {
		return out, e
	}
	e := s.Store.WithWorkspace(ctx, w, func(t PackageTx) error {
		if e := packageAuthorize(ctx, t, actor, false); e != nil {
			return e
		}
		if employee != "" {
			if e := t.Employee(ctx, employee); e != nil {
				return e
			}
		}
		var e error
		out.Items, out.Total, e = t.List(ctx, employee, p, n)
		return e
	})
	return out, e
}
func (s *PackageService) Get(ctx context.Context, w, actor, employee, id string) (CapabilityPackage, error) {
	var out CapabilityPackage
	e := s.Store.WithWorkspace(ctx, w, func(t PackageTx) error {
		if e := packageAuthorize(ctx, t, actor, false); e != nil {
			return e
		}
		var e error
		out, e = t.Get(ctx, employee, id)
		return e
	})
	return out, e
}
func (s *PackageService) Create(ctx context.Context, w, actor, employee string, r PackageCreate) (CapabilityPackage, error) {
	if r.Scope != nil && r.ConversationScope != nil {
		return CapabilityPackage{}, Fail("invalid_package_scope", 400, "不能同时修改新旧范围")
	}
	v := CapabilityPackage{WorkScope: r.WorkScope, ConversationScope: r.ConversationScope, ID: NewID(), AgentID: employee, Name: r.Name, Description: r.Description, Enabled: true, Version: 1, Scope: PackageScope{Mode: "inherit_with_blacklist", AllowMemberIDs: []string{}, DenyMemberIDs: []string{}}, Skills: r.Skills, Plugins: r.Plugins}
	if r.Enabled != nil {
		v.Enabled = *r.Enabled
	}
	if r.Scope != nil {
		v.Scope = *r.Scope
	}
	if r.TriggerScope != nil {
		v.TriggerScope = *r.TriggerScope
	}
	return s.createPackage(ctx, w, actor, v)
}
func (s *PackageService) createPackage(ctx context.Context, w, actor string, v CapabilityPackage) (CapabilityPackage, error) {
	if e := validatePackage(&v); e != nil {
		return CapabilityPackage{}, e
	}
	e := s.Store.WithWorkspace(ctx, w, func(t PackageTx) error {
		if e := AuthorizeEmployee(ctx, t, actor, v.AgentID); e != nil {
			return e
		}
		if e := validatePackageReferences(ctx, t, v); e != nil {
			return e
		}
		if e := t.Save(ctx, v, true); e != nil {
			return e
		}
		var e error
		v, e = t.Get(ctx, v.AgentID, v.ID)
		return e
	})
	return v, e
}
func (s *PackageService) Update(ctx context.Context, w, actor, employee, id string, r PackagePatch) (CapabilityPackage, error) {
	var v CapabilityPackage
	if r.Version < 1 {
		return v, Fail("version_required", 400, "请提供当前能力包版本")
	}
	if r.WorkScope == nil && r.ConversationScope == nil && r.Name == nil && r.Description == nil && r.Enabled == nil && r.Scope == nil && r.TriggerScope == nil && r.Skills == nil && r.Plugins == nil {
		return v, Fail("invalid_package_patch", 400, "没有需要修改的能力包字段")
	}
	e := s.Store.WithWorkspace(ctx, w, func(t PackageTx) error {
		if e := AuthorizeEmployee(ctx, t, actor, employee); e != nil {
			return e
		}
		var e error
		v, e = t.Get(ctx, employee, id)
		if e != nil {
			return e
		}
		if v.Version != r.Version {
			return Fail("package_version_conflict", 409, "能力包已更新，请刷新后重试")
		}
		if r.Scope != nil && (r.ConversationScope != nil || v.ConversationScope != nil) {
			return Fail("invalid_package_scope", 400, "不能同时修改新旧范围或回退旧范围")
		}
		previous := v
		oldConversationScope := v.ConversationScope
		oldScope := v.Scope
		applyPackagePatch(&v, r)
		if e = validatePackage(&v); e != nil {
			return e
		}
		if r.WorkScope != nil {
			for _, id := range r.WorkScope.EmployeeIDs {
				if e = t.Employee(ctx, id); e != nil {
					return e
				}
			}
		}
		if r.ConversationScope != nil {
			if e = validateConversationReferences(ctx, t, v, oldConversationScope); e != nil {
				return e
			}
		}
		if r.Scope != nil {
			if e = validatePackageSelectorChanges(ctx, t, v, oldScope); e != nil {
				return e
			}
			if e = validatePackageMembers(ctx, t, v.Scope); e != nil {
				return e
			}
		}
		if r.TriggerScope != nil {
			if e = validatePackageTriggers(ctx, t, v); e != nil {
				return e
			}
		}
		// Metadata/disable edits must still work when a bound resource was disabled.
		if r.Skills != nil || r.Plugins != nil || r.Enabled != nil && *r.Enabled {
			if e = validatePackageReferences(ctx, t, v, previous); e != nil {
				return e
			}
		}
		v.Version++
		if e = t.Save(ctx, v, false); e != nil {
			return e
		}
		v, e = t.Get(ctx, employee, id)
		return e
	})
	return v, e
}
func (s *PackageService) CopyTemplate(ctx context.Context, w, actor, employee, template string, r PackageCopy) (CapabilityPackage, error) {
	var out CapabilityPackage
	if r.TemplateVersion < 1 {
		return out, Fail("version_required", 400, "请提供模板版本")
	}
	e := s.Store.WithWorkspace(ctx, w, func(t PackageTx) error {
		if e := AuthorizeEmployee(ctx, t, actor, employee); e != nil {
			return e
		}
		v, e := t.Get(ctx, "", template)
		if e != nil {
			return e
		}
		if v.Version != r.TemplateVersion {
			return Fail("package_version_conflict", 409, "模板已更新，请重新查看")
		}
		if !v.Enabled {
			return Fail("template_unavailable", 409, "模板已停用")
		}
		v.ID = NewID()
		v.AgentID = employee
		v.SourceTemplateID = template
		v.Version = 1
		if r.Name != "" {
			v.Name = r.Name
		}
		if e = validatePackage(&v); e != nil {
			return e
		}
		if e = validatePackageReferences(ctx, t, v); e != nil {
			return e
		}
		if e = t.Save(ctx, v, true); e != nil {
			return e
		}
		out, e = t.Get(ctx, employee, v.ID)
		return e
	})
	return out, e
}
func applyPackagePatch(v *CapabilityPackage, r PackagePatch) {
	if r.WorkScope != nil {
		v.WorkScope = r.WorkScope
	}
	if r.ConversationScope != nil {
		v.ConversationScope = r.ConversationScope
	}
	if r.Name != nil {
		v.Name = *r.Name
	}
	if r.Description != nil {
		v.Description = *r.Description
	}
	if r.Enabled != nil {
		v.Enabled = *r.Enabled
	}
	if r.Scope != nil {
		v.Scope = *r.Scope
	}
	if r.TriggerScope != nil {
		v.TriggerScope = *r.TriggerScope
	}
	if r.Skills != nil {
		v.Skills = *r.Skills
	}
	if r.Plugins != nil {
		v.Plugins = *r.Plugins
	}
}
func validatePackage(v *CapabilityPackage) error {
	if v.WorkScope != nil {
		if err := v.WorkScope.Validate("inherit"); err != nil {
			return err
		}
	}
	if err := normalizePackageConversationScope(v); err != nil {
		return err
	}
	if err := normalizePackageTriggerScope(v); err != nil {
		return err
	}
	v.Name = strings.TrimSpace(v.Name)
	v.Description = strings.TrimSpace(v.Description)
	if v.Name == "" || utf8.RuneCountInString(v.Name) > 100 || utf8.RuneCountInString(v.Description) > 2000 || strings.ContainsRune(v.Name+v.Description, '\x00') {
		return Fail("invalid_package", 400, "能力包名称或说明无效")
	}
	if err := normalizePackageSelectors(&v.Scope); err != nil {
		return err
	}
	if v.Scope.Mode != "inherit_with_blacklist" && v.Scope.Mode != "whitelist_with_blacklist" {
		return Fail("invalid_package_scope", 400, "能力包使用范围无效")
	}
	v.Skills = slices.Clone(v.Skills)
	v.Plugins = slices.Clone(v.Plugins)
	if v.Skills == nil {
		v.Skills = []PackageSkill{}
	}
	if v.Plugins == nil {
		v.Plugins = []PackagePlugin{}
	}
	if v.Scope.AllowMemberIDs == nil {
		v.Scope.AllowMemberIDs = []string{}
	}
	if v.Scope.DenyMemberIDs == nil {
		v.Scope.DenyMemberIDs = []string{}
	}
	if len(v.Skills) > 100 || len(v.Plugins) > 100 {
		return Fail("invalid_package", 400, "能力包资源过多")
	}
	for _, ids := range [][]string{v.Scope.AllowMemberIDs, v.Scope.DenyMemberIDs} {
		if !packageIDs(ids) {
			return Fail("invalid_package_scope", 400, "成员列表无效")
		}
	}
	seen := map[string]bool{}
	for _, r := range v.Skills {
		if !agentUUID(r.SkillID) || (r.VersionID != "" && !agentUUID(r.VersionID)) || seen[r.SkillID] {
			return Fail("invalid_package_skill", 400, "技能引用无效或重复")
		}
		seen[r.SkillID] = true
	}
	return validatePackagePlugins(v)
}
func validatePackagePlugins(v *CapabilityPackage) error {
	seen := map[string]bool{}
	for _, r := range v.Plugins {
		if !agentUUID(r.PluginID) || (r.VersionID != "" && !agentUUID(r.VersionID)) || seen[r.PluginID] {
			return Fail("invalid_package_plugin", 400, "插件引用无效或重复")
		}
		seen[r.PluginID] = true
	}
	return nil
}
func packageIDs(ids []string) bool {
	if len(ids) > 100 {
		return false
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if !agentUUID(id) || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}
func validatePackageMembers(ctx context.Context, t PackageTx, s PackageScope) error {
	for _, ids := range [][]string{s.AllowMemberIDs, s.DenyMemberIDs} {
		for _, id := range ids {
			m, e := t.Member(ctx, id)
			if errors.Is(e, ErrWorkspaceMissing) || e == nil && m.Status != "active" {
				return Fail("package_member_unavailable", 409, "成员不存在或已不在当前工作区")
			}
			if e != nil {
				return e
			}
		}
	}
	return nil
}
func validatePackageReferences(ctx context.Context, t PackageTx, v CapabilityPackage, existing ...CapabilityPackage) error {
	var previous CapabilityPackage
	if len(existing) > 0 {
		previous = existing[0]
	}
	if v.WorkScope != nil {
		for _, id := range v.WorkScope.EmployeeIDs {
			if e := t.Employee(ctx, id); e != nil {
				return e
			}
		}
	}
	if err := validateConversationReferences(ctx, t, v, nil); err != nil {
		return err
	}
	if err := validatePackageTriggers(ctx, t, v); err != nil {
		return err
	}
	if v.AgentID != "" {
		if e := t.Employee(ctx, v.AgentID); e != nil {
			return e
		}
	}
	if e := validatePackageSelectors(ctx, t, v); e != nil {
		return e
	}
	if e := validatePackageMembers(ctx, t, v.Scope); e != nil {
		return e
	}
	for i, r := range v.Skills {
		state, e := t.ValidateSkill(ctx, r)
		if e != nil {
			return e
		}
		if !state.Enabled || state.Unlisted && !slices.ContainsFunc(previous.Skills, func(old PackageSkill) bool { return old.SkillID == r.SkillID }) {
			return Fail("skill_unavailable", 409, "技能已停用或下架")
		}
		v.Skills[i].VersionID = state.CurrentVersionID
	}
	for i, r := range v.Plugins {
		state, e := t.ValidatePlugin(ctx, r)
		if e != nil {
			return e
		}
		if !state.Enabled || state.Unlisted && !slices.ContainsFunc(previous.Plugins, func(old PackagePlugin) bool { return old.PluginID == r.PluginID }) {
			return Fail("plugin_unavailable", 409, "插件已停用或下架")
		}
		v.Plugins[i].VersionID = state.CurrentVersionID
	}
	return nil
}

// PackageAllowsMember applies only the package layer after employee admission.
// Deny always wins; this helper never bypasses the caller's employee admission.
func PackageAllowsMember(p CapabilityPackage, userID string, employeeAllowed bool) bool {
	if !employeeAllowed || !p.Enabled {
		return false
	}
	for _, ref := range p.Scope.Deny {
		if ref.Channel == "web" && ref.Kind == "user" && ref.ID == userID {
			return false
		}
	}
	for _, id := range p.Scope.DenyMemberIDs {
		if id == userID {
			return false
		}
	}
	if p.Scope.Mode == "inherit_with_blacklist" {
		return true
	}
	if p.Scope.Mode != "whitelist_with_blacklist" {
		return false
	}
	for _, ref := range p.Scope.Allow {
		if ref.Channel == "web" && ref.Kind == "user" && ref.ID == userID {
			return true
		}
	}
	for _, id := range p.Scope.AllowMemberIDs {
		if id == userID {
			return true
		}
	}
	return false
}

type ResourceUpgrade struct {
	Kind          string `json:"kind"`
	ResourceID    string `json:"resource_id"`
	FromVersionID string `json:"from_version_id"`
	ToVersionID   string `json:"to_version_id"`
}

func (s *PackageService) Upgrade(ctx context.Context, w, actor, employee string, r ResourceUpgrade) error {
	if (r.Kind != "skill" && r.Kind != "plugin") || !agentUUID(r.ResourceID) || !agentUUID(r.FromVersionID) || !agentUUID(r.ToVersionID) {
		return Fail("invalid_resource_upgrade", 400, "资源或版本无效")
	}
	return s.Store.WithWorkspace(ctx, w, func(t PackageTx) error {
		if err := AuthorizeEmployee(ctx, t, actor, employee); err != nil {
			return err
		}
		return t.Upgrade(ctx, employee, r)
	})
}
