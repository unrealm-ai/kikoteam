package service

import (
	"context"
	"errors"
	"slices"
	"sort"
	"strings"
	"time"
)

// ExecutionCapabilities contains immutable resource pins and no credentials.
// Package provenance is retained for intersection with current authorization.
type ExecutionCapabilities struct {
	DefaultPlugins []DefaultPluginPin  `json:"default_plugins,omitempty"`
	ID             string              `json:"id"`
	Packages       []CapabilityPackage `json:"packages"`
	Accounts       []RuntimeAccountPin `json:"accounts"`
}

type DefaultPluginPin struct {
	PluginID  string    `json:"plugin_id"`
	VersionID string    `json:"version_id"`
	Handler   string    `json:"handler_key"`
	Tools     []MCPTool `json:"tools"`
}

func (c *ExecutionCapabilities) ForPlugin(id string) *ExecutionCapabilities {
	if c == nil {
		return nil
	}
	out := &ExecutionCapabilities{ID: c.ID, Packages: []CapabilityPackage{}, Accounts: []RuntimeAccountPin{}}
	for _, p := range c.Packages {
		p.Skills = nil
		plugins := []PackagePlugin{}
		for _, pin := range p.Plugins {
			if pin.PluginID == id {
				plugins = append(plugins, pin)
			}
		}
		p.Plugins = plugins
		if len(plugins) > 0 {
			out.Packages = append(out.Packages, p)
		}
	}
	for _, a := range c.Accounts {
		if a.PluginID == id {
			out.Accounts = append(out.Accounts, a)
		}
	}
	return out
}

type RuntimeAccountPin struct {
	Source          string `json:"source,omitempty"`
	SourceRevision  int64  `json:"source_revision,omitempty"`
	PluginID        string `json:"plugin_id"`
	ID              string `json:"id"`
	Version         int64  `json:"version"`
	PluginVersionID string `json:"plugin_version_id"`
}
type RuntimeSkill struct {
	SkillID     string `json:"skill_id"`
	VersionID   string `json:"version_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Scope       string `json:"scope"`
}
type RuntimePlugin struct {
	SourceRevision int64             `json:"-"`
	Source         string            `json:"source,omitempty"`
	PluginID       string            `json:"plugin_id"`
	VersionID      string            `json:"version_id"`
	AccountID      string            `json:"account_id"`
	AccountVersion int64             `json:"account_version"`
	Endpoint       string            `json:"endpoint"`
	Headers        map[string]string `json:"headers"`
	Tools          []MCPTool         `json:"tools"`
}
type RuntimeService struct {
	Packages *PackageService
	Skills   *SkillService
	Plugins  *PluginService
}

type RuntimePackageSummary struct {
	Name        string
	Description string
}

type RuntimeResources struct {
	Skills           []RuntimeSkill          `json:"skills"`
	Plugins          []RuntimePlugin         `json:"plugins"`
	Problems         []string                `json:"problems"`
	PackageSummaries []RuntimePackageSummary `json:"-"`
}

// Effective intersects each original package with that same package's current
// grant. It never flattens caller scopes across packages or adds new resources.
func (s *RuntimeService) Effective(ctx context.Context, w, actor, employee string, snapshot *ExecutionCapabilities) ([]CapabilityPackage, error) {
	out := []CapabilityPackage{}
	_, group := WebGroupExecutionFrom(ctx)
	_, space := TaskSpaceExecutionFrom(ctx)
	_, trigger := TriggerExecutionFrom(ctx)
	if _, channel := ChannelExecutionFrom(ctx); channel || group || space || trigger {
		if s.Packages == nil || s.Packages.Store == nil {
			return nil, Fail("channel_execution_forbidden", 403, "渠道运行身份不可验证")
		}
		if err := s.Packages.Store.WithWorkspace(ctx, w, func(t PackageTx) error {
			return authorizeRuntime(ctx, t, actor, employee, func() error {
				if group || space {
					return packageAuthorize(ctx, t, actor, false)
				}
				return Fail("channel_execution_forbidden", 403, "渠道身份缺失")
			})
		}); err != nil {
			return nil, err
		}
	}
	if snapshot == nil {
		return out, nil
	}
	for _, old := range snapshot.Packages {
		allowed, err := s.Packages.runtimeScopeAllows(ctx, w, actor, employee, old)
		if err != nil {
			return nil, err
		}
		if !allowed {
			continue
		}
		current, err := s.Packages.runtimePackage(ctx, w, actor, employee, old.ID)
		if errors.Is(err, ErrWorkspaceMissing) {
			continue
		}
		if err != nil {
			return nil, err
		}
		allowed, err = s.Packages.runtimeScopeAllows(ctx, w, actor, employee, current)
		if err != nil {
			return nil, err
		}
		if !allowed {
			continue
		}
		p := old
		p.Skills = []PackageSkill{}
		p.Plugins = []PackagePlugin{}
		for _, skill := range old.Skills {
			for _, live := range current.Skills {
				if live.SkillID == skill.SkillID {
					p.Skills = append(p.Skills, skill)
					break
				}
			}
		}
		for _, plugin := range old.Plugins {
			if slices.ContainsFunc(current.Plugins, func(live PackagePlugin) bool { return live.PluginID == plugin.PluginID }) {
				p.Plugins = append(p.Plugins, plugin)
			}
		}
		out = append(out, p)
	}
	return out, nil
}

func (s *RuntimeService) Resolve(ctx context.Context, w, actor, employee string, snapshot *ExecutionCapabilities, session string, probe bool) (RuntimeResources, error) {
	packs, err := s.Effective(ctx, w, actor, employee, snapshot)
	if err != nil {
		return RuntimeResources{}, err
	}
	out, err := s.resolveResources(ctx, w, actor, employee, snapshot, session, packs, probe)
	if err != nil {
		return out, err
	}
	if probe {
		// Re-evaluate package provenance as well as resources after discovery.
		// A shared tool can remain usable after revocation of one of its packages.
		packs, err = s.Effective(ctx, w, actor, employee, snapshot)
		if err != nil {
			return RuntimeResources{}, err
		}
		current, e := s.resolveResources(ctx, w, actor, employee, snapshot, session, packs, false)
		if e != nil {
			return RuntimeResources{}, e
		}
		out = intersectRuntime(out, current)
	}
	for _, pack := range packs {
		if out.hasPackageResource(pack) {
			out.PackageSummaries = append(out.PackageSummaries, RuntimePackageSummary{Name: pack.Name, Description: pack.Description})
		}
	}
	return out, nil
}

func (r RuntimeResources) hasPackageResource(pack CapabilityPackage) bool {
	for _, skill := range r.Skills {
		if slices.Contains(pack.Skills, PackageSkill{SkillID: skill.SkillID, VersionID: skill.VersionID}) {
			return true
		}
	}
	for _, plugin := range r.Plugins {
		for _, pin := range pack.Plugins {
			if plugin.PluginID != pin.PluginID || plugin.VersionID != pin.VersionID {
				continue
			}
			return true
		}
	}
	return false
}

func (s *RuntimeService) resolveResources(ctx context.Context, w, actor, employee string, snapshot *ExecutionCapabilities, session string, packs []CapabilityPackage, probe bool) (RuntimeResources, error) {
	out := RuntimeResources{Skills: []RuntimeSkill{}, Plugins: []RuntimePlugin{}, Problems: []string{}}
	probeCtx, cancel := context.WithTimeout(ctx, 18*time.Second)
	defer cancel()
	skills := map[string]PackageSkill{}
	plugins := map[string]PackagePlugin{}
	for _, pack := range packs {
		for _, pin := range pack.Skills {
			if old, ok := skills[pin.SkillID]; ok && old.VersionID != pin.VersionID {
				return out, Fail("capability_version_conflict", 422, "同一技能绑定了不同版本")
			}
			skills[pin.SkillID] = pin
		}
		for _, pin := range pack.Plugins {
			key := pin.PluginID + ":" + pin.VersionID
			plugins[key] = pin
		}
	}
	ids := []string{}
	for id := range skills {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	names := map[string]string{}
	for _, id := range ids {
		v, e := s.Skills.RuntimeSkill(ctx, w, actor, skills[id])
		if e != nil {
			if !runtimeUnavailable(e) {
				return out, e
			}
			out.Problems = append(out.Problems, "skill_unavailable:"+id)
			continue
		}
		if other := names[v.Name]; other != "" && other != id {
			return out, Fail("skill_name_conflict", 422, "获准技能名称重复，请调整能力包")
		}
		names[v.Name] = id
		out.Skills = append(out.Skills, v)
	}
	ids = []string{}
	for id := range plugins {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		pin := plugins[id]
		var account RuntimeAccountPin
		if snapshot != nil {
			for _, a := range snapshot.Accounts {
				if a.PluginID == pin.PluginID && a.PluginVersionID == pin.VersionID {
					account = a
					break
				}
			}
		}
		if account.ID == "" {
			out.Problems = append(out.Problems, "plugin_authorization_missing:"+pin.PluginID)
			continue
		}
		v, e := s.Plugins.RuntimePlugin(ctx, w, actor, employee, account, pin.VersionID)
		if e != nil {
			if !runtimeUnavailable(e) {
				return out, e
			}
			out.Problems = append(out.Problems, "plugin_authorization_changed:"+pin.PluginID)
			continue
		}
		v.Tools, e = s.Plugins.sessionTools(probeCtx, w, actor, employee, session, snapshot.ID, account, v, probe)
		if e != nil {
			out.Problems = append(out.Problems, "plugin_probe_failed:"+pin.PluginID)
			continue
		}
		if len(v.Tools) > 0 {
			out.Plugins = append(out.Plugins, v)
		}
	}
	return out, nil
}

func intersectRuntime(discovered, current RuntimeResources) RuntimeResources {
	out := RuntimeResources{Skills: current.Skills, Plugins: []RuntimePlugin{}, Problems: append(discovered.Problems, current.Problems...)}
	for _, p := range discovered.Plugins {
		for _, now := range current.Plugins {
			if p.PluginID != now.PluginID || p.VersionID != now.VersionID || p.AccountID != now.AccountID || p.AccountVersion != now.AccountVersion {
				continue
			}
			tools := []MCPTool{}
			for _, tool := range p.Tools {
				for _, live := range now.Tools {
					if tool.Name == live.Name && tool.SchemaDigest == live.SchemaDigest {
						tools = append(tools, tool)
						break
					}
				}
			}
			now.Tools = tools
			if len(tools) > 0 {
				out.Plugins = append(out.Plugins, now)
			}
		}
	}
	return out
}

func runtimeUnavailable(err error) bool {
	var e *Error
	return errors.Is(err, ErrWorkspaceMissing) || errors.As(err, &e) && (e.Status == 403 || e.Status == 404 || e.Status == 409 || e.Status == 422)
}

func (s *PluginService) sessionTools(ctx context.Context, w, actor, employee, session, snapshot string, pin RuntimeAccountPin, p RuntimePlugin, probe bool) ([]MCPTool, error) {
	var live []MCPTool
	if probe {
		var err error
		live, err = s.discoverRuntime(ctx, w, actor, employee, pin, p)
		if err != nil {
			return nil, err
		}
		if live == nil {
			live = []MCPTool{}
		}
	}
	var frozen []MCPTool
	err := s.Store.WithWorkspace(ctx, w, func(t PluginTx) error {
		var err error
		if probe {
			if err = t.NoteDiscoveredTools(ctx, pin, live); err != nil {
				return err
			}
		}
		frozen, err = t.SessionTools(ctx, session, snapshot, p.VersionID, live)
		return err
	})
	if err != nil || !probe {
		return frozen, err
	}
	return intersectTools(frozen, live), nil
}

func intersectTools(frozen, live []MCPTool) []MCPTool {
	out := []MCPTool{}
	for _, tool := range frozen {
		if slices.ContainsFunc(live, func(now MCPTool) bool { return tool.Name == now.Name && tool.SchemaDigest == now.SchemaDigest }) {
			out = append(out, tool)
		}
	}
	return out
}

func (s *PluginService) discoverRuntime(ctx context.Context, w, actor, employee string, pin RuntimeAccountPin, p RuntimePlugin) ([]MCPTool, error) {
	if s.Probe == nil {
		return nil, Fail("mcp_unavailable", 503, "MCP 未配置")
	}
	var definition Plugin
	var secret string
	err := s.Store.WithWorkspace(ctx, w, func(t PluginTx) error {
		if err := authorizeRuntime(ctx, t, actor, employee, func() error { return pluginAuthorize(ctx, t, actor, false) }); err != nil {
			return err
		}
		var e error
		definition, e = t.GetVersion(ctx, p.PluginID, p.VersionID)
		if e != nil {
			return e
		}
		a, e := pinnedConnection(ctx, t, employee, pin)
		if e != nil {
			return e
		}
		definition, e = definition.forAuthMethod(a.AuthMethod)
		if e != nil {
			return e
		}
		if a.ID != pin.ID || a.Version != pin.Version || !a.Enabled || (!a.Validated && definition.AuthType != "none") {
			return Fail("plugin_authorization_changed", 403, "插件账号已变更")
		}
		if definition.AuthType == "bearer" || definition.AuthType == "oauth" {
			secret = strings.TrimPrefix(p.Headers["Authorization"], "Bearer ")
		} else {
			secret = p.Headers[definition.HeaderName]
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	live, err := s.Probe.Probe(ctx, definition, secret)
	if err != nil {
		return nil, err
	}
	// Recheck after network I/O; a concurrent account change cannot issue a
	// configuration containing stale credentials to the original target.
	_, err = s.RuntimePlugin(ctx, w, actor, employee, pin, p.VersionID)
	return live, err
}

func (s *SkillService) RuntimeSkill(ctx context.Context, w, actor string, pin PackageSkill) (RuntimeSkill, error) {
	var out RuntimeSkill
	err := s.Store.WithWorkspace(ctx, w, func(t SkillTx) error {
		if err := authorizeRuntime(ctx, t, actor, "", func() error { return skillAuthorize(ctx, t, actor, false) }); err != nil {
			return err
		}
		current, err := t.Get(ctx, pin.SkillID)
		if err != nil {
			return err
		}
		if !current.Enabled {
			return Fail("skill_unavailable", 403, "技能已停用")
		}
		v, err := t.GetVersion(ctx, pin.SkillID, pin.VersionID)
		if err != nil {
			return err
		}
		if s.Assets == nil {
			return Fail("skill_storage_unavailable", 503, "技能资产存储不可用")
		}
		base := "workspaces/" + w + "/skills/"
		if current.Scope == "official" {
			base = "official/skills/"
		}
		if !strings.HasPrefix(v.StorageIdentity, "local:") || v.StorageIdentity != s.Assets.Identity() || v.KeyPrefix != base+pin.SkillID+"/versions/"+pin.VersionID {
			return Fail("skill_unavailable", 409, "此技能版本在当前挂载目录不可用")
		}
		out = RuntimeSkill{SkillID: pin.SkillID, VersionID: pin.VersionID, Name: v.TechnicalName, Description: v.Description, Scope: current.Scope}
		return nil
	})
	return out, err
}

func (s *PluginService) RuntimePlugin(ctx context.Context, w, actor, employee string, pin RuntimeAccountPin, version string) (RuntimePlugin, error) {
	var out RuntimePlugin
	var expiryError error
	err := s.Store.WithConnection(ctx, w, pin.ID, func(t PluginTx) error {
		if err := authorizeRuntime(ctx, t, actor, employee, func() error { return pluginAuthorize(ctx, t, actor, false) }); err != nil {
			return err
		}
		current, err := t.Get(ctx, pin.PluginID)
		if err != nil {
			return err
		}
		if !current.Enabled {
			return Fail("plugin_unavailable", 403, "插件已停用")
		}
		p, err := t.GetVersion(ctx, pin.PluginID, version)
		if err != nil {
			return err
		}
		a, err := pinnedConnection(ctx, t, employee, pin)
		if err != nil {
			return err
		}
		p, err = p.forAuthMethod(a.AuthMethod)
		if err != nil {
			return err
		}
		if !a.Enabled || (!a.Validated && p.AuthType != "none") || a.ID != pin.ID || a.Version != pin.Version || a.PluginVersionID != version || pin.PluginVersionID != version {
			return Fail("plugin_authorization_changed", 403, "插件账号已变更或撤销，原会话不会切换账号")
		}
		out = RuntimePlugin{SourceRevision: pin.SourceRevision, Source: a.Source, PluginID: pin.PluginID, VersionID: version, AccountID: a.ID, AccountVersion: a.Version, Endpoint: p.Endpoint, Headers: map[string]string{}, Tools: []MCPTool{}}
		secret, err := s.accessToken(ctx, t, w, p, a)
		var issue *Error
		if errors.As(err, &issue) && issue.Code == "oauth_reconnect" {
			expiryError = err
			a.Validated = false
			a.Version++
			return t.RefreshAccount(ctx, a)
		}
		if err != nil {
			return err
		}
		switch p.AuthType {
		case "bearer", "oauth":
			out.Headers["Authorization"] = "Bearer " + secret
		case "api_key":
			out.Headers[p.HeaderName] = secret
		case "none":
		default:
			return Fail("plugin_unavailable", 422, "当前认证方式尚不支持执行")
		}
		return nil
	})
	if err == nil && expiryError != nil {
		return RuntimePlugin{}, expiryError
	}
	return out, err
}
