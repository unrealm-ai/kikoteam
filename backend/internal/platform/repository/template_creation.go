package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"time"
)

func (s *TemplateStore) LoadCreation(ctx context.Context, w, a string, r service.TemplateCreation) (v service.TemplateVersion, assets []service.TemplateAsset, e error) {
	assets = []service.TemplateAsset{}
	e = s.With(ctx, w, a, true, func(raw service.TemplateTx) error {
		t := raw.(*templateTx)
		var scope string
		var owner sql.NullString
		var listed bool
		if e = t.model.tx.QueryRowContext(ctx, `SELECT scope,owner_workspace_id,is_listed FROM platform_agent_templates WHERE id=$1`, r.TemplateID).Scan(&scope, &owner, &listed); e != nil {
			return workspaceDBError(e)
		}
		if scope == "workspace" && owner.String != w {
			return service.ErrWorkspaceMissing
		}
		if !listed {
			return service.Fail("template_unavailable", 409, "模板已下架")
		}
		source := &templateTx{model: &modelTx{tx: t.model.tx, workspace: owner.String}, actor: a, manage: true}
		v, e = source.Version(ctx, r.TemplateID, r.TemplateVersionID, true)
		if e != nil {
			return e
		}
		if v.PublishedAt == nil {
			return service.ErrWorkspaceMissing
		}
		if e := service.ValidateAgentModel(ctx, &agentTx{model: t.model}, &service.Agent{ModelProfileID: r.ModelProfileID, PlatformModelID: r.PlatformModelID}); e != nil {
			return e
		}
		var total int64
		files := 0
		for _, ref := range v.Content.Resources {
			x := service.TemplateAsset{Resource: ref}
			src := &modelTx{tx: t.model.tx, workspace: ref.WorkspaceID}
			if ref.Kind == "skill" {
				x.Skill, e = (&skillTx{model: src}).GetVersion(ctx, ref.ID, ref.VersionID)
			} else {
				x.Plugin, e = (&pluginTx{model: src}).GetVersion(ctx, ref.ID, ref.VersionID)
			}
			if e != nil {
				return service.Fail("template_dependency_unavailable", 409, "模板固定资源不可用")
			}
			x.Imported = scope == "workspace" || ref.WorkspaceID == ""
			if scope == "official" && ref.WorkspaceID != "" {
				x, e = findTemplateImport(ctx, t.model, r.TemplateVersionID, x)
				if e != nil {
					return e
				}
			}
			if !x.Imported {
				for _, file := range x.Skill.Manifest.Files {
					total += file.Size
					files++
				}
				x.TargetID = service.NewID()
				x.TargetVersionID = service.NewID()
				if ref.Kind == "skill" {
					x.ImportID = service.NewID()
					x.TargetPrefix = fmt.Sprintf("workspaces/%s/skills/%s/versions/%s", w, x.TargetID, x.TargetVersionID)
				}
			}
			assets = append(assets, x)
		}
		if total > 20<<20 || files > 500 {
			return service.Fail("invalid_template_content", 400, "技能资产超出模板限制")
		}
		return nil
	})
	return
}
func findTemplateImport(ctx context.Context, m *modelTx, version string, x service.TemplateAsset) (service.TemplateAsset, error) {
	var id, pin string
	e := m.tx.QueryRowContext(ctx, `SELECT COALESCE(skill_id,plugin_id)::text,COALESCE(skill_version_id,plugin_version_id)::text FROM platform_agent_template_resource_imports WHERE workspace_id=$1 AND template_version_id=$2 AND resource_key=$3 AND kind=$4`, m.workspace, version, x.Resource.Key, x.Resource.Kind).Scan(&id, &pin)
	if errors.Is(e, sql.ErrNoRows) {
		return x, nil
	}
	if e != nil {
		return x, e
	}
	x.Imported = true
	x.TargetID = id
	x.TargetVersionID = pin
	return x, nil
}
func (s *TemplateStore) Ready(ctx context.Context, w, a string, assets []service.TemplateAsset) error {
	return s.With(ctx, w, a, true, func(raw service.TemplateTx) error {
		m := raw.(*templateTx).model
		for _, x := range assets {
			if !x.Imported && x.ImportID != "" {
				if e := (&skillTx{model: m}).MarkReady(ctx, x.ImportID); e != nil {
					return e
				}
			}
		}
		return nil
	})
}
func (s *TemplateStore) Stage(ctx context.Context, w, a string, assets []service.TemplateAsset) error {
	return s.With(ctx, w, a, true, func(t service.TemplateTx) error {
		m := t.(*templateTx).model
		for _, x := range assets {
			if x.Imported || x.ImportID == "" {
				continue
			}
			imp := service.SkillImport{ID: x.ImportID, ActorID: a, TechnicalName: x.Skill.TechnicalName, Description: x.Skill.Description, Manifest: x.Skill.Manifest, Source: service.SkillSource{Type: "agent_template", ResourceKey: x.Resource.Key, CandidateID: x.TargetID, CandidateVersionID: x.TargetVersionID, CandidatePrefix: x.TargetPrefix}, StorageIdentity: x.Skill.StorageIdentity, KeyPrefix: "temp/uploads/skill-imports/" + w + "/" + x.ImportID, ExpiresAt: time.Now().Add(time.Hour)}
			if e := (&skillTx{model: m}).SaveImport(ctx, imp); e != nil {
				return e
			}
			plan, err := json.Marshal(map[string]string{"candidate_id": x.TargetID, "candidate_version_id": x.TargetVersionID, "candidate_prefix": x.TargetPrefix, "resource_key": x.Resource.Key})
			if err != nil {
				return err
			}
			if _, err = m.tx.ExecContext(ctx, `UPDATE platform_skill_imports SET source=source||$2::jsonb WHERE id=$1`, imp.ID, plan); err != nil {
				return err
			}
		}
		return nil
	})
}
func (s *TemplateStore) CommitCreation(ctx context.Context, w, a string, r service.TemplateCreation, v service.TemplateVersion, assets []service.TemplateAsset, instruction string, params map[string]any) (out service.Agent, e error) {
	e = s.With(ctx, w, a, true, func(raw service.TemplateTx) error {
		t := raw.(*templateTx)
		m := t.model
		var scope string
		var owner sql.NullString
		var listed bool
		var builtin string
		if e = m.tx.QueryRowContext(ctx, `SELECT scope,owner_workspace_id,is_listed,coalesce(builtin_key,'') FROM platform_agent_templates WHERE id=$1 FOR SHARE`, r.TemplateID).Scan(&scope, &owner, &listed, &builtin); e != nil {
			return workspaceDBError(e)
		}
		var published bool
		if e = m.tx.QueryRowContext(ctx, `SELECT published_at IS NOT NULL FROM platform_agent_template_versions WHERE template_id=$1 AND id=$2`, r.TemplateID, v.ID).Scan(&published); e != nil {
			return e
		}
		if !listed || !published || scope == "workspace" && owner.String != w {
			return service.Fail("template_unavailable", 409, "模板不可创建")
		}
		// A manually created reporter also satisfies a new workspace's starter.
		if builtin == "market-reporter" {
			result, err := m.tx.ExecContext(ctx, "UPDATE platform_workspaces SET starter_pending=false WHERE id=$1 AND starter_pending", w)
			if err != nil {
				return err
			}
			n, err := result.RowsAffected()
			if err != nil || r.Starter && n == 0 {
				return err
			}
		}
		at := &agentTx{model: m, ensureKey: s.EnsureKey}
		if err := service.ValidateAgentModel(ctx, at, &service.Agent{ModelProfileID: r.ModelProfileID, PlatformModelID: r.PlatformModelID}); err != nil {
			return err
		}
		refs, err := importTemplateResources(ctx, m, w, a, scope, v.ID, assets)
		if err != nil {
			return err
		}
		out = service.Agent{ID: service.NewID(), Name: r.Name, Role: v.Content.Role, Avatar: v.Content.Avatar, Instruction: instruction, ModelProfileID: r.ModelProfileID, PlatformModelID: r.PlatformModelID, Harness: "pi", Enabled: true, Version: 1, ConfigVersion: 1, AdmissionEpoch: 1}
		if r.Role != nil {
			out.Role = *r.Role
		}
		if r.Avatar != nil {
			out.Avatar = *r.Avatar
		}
		if e = at.Create(ctx, out, a); e != nil {
			return e
		}
		if _, e = at.EnsureModelKey(ctx, out.ID, a); e != nil {
			return e
		}
		values, err := json.Marshal(params)
		if err != nil {
			return err
		}
		if _, e = m.tx.ExecContext(ctx, `UPDATE platform_agents SET source_agent_template_version_id=$2,template_parameters=$3 WHERE id=$1`, out.ID, v.ID, values); e != nil {
			return e
		}
		for _, p := range v.Content.Packages {
			if (r.Starter || r.RequiredPackagesOnly) && p.Onboarding == "deferred" {
				continue
			}
			pkg := service.CapabilityPackage{ID: service.NewID(), AgentID: out.ID, Name: p.Name, Description: p.Description, Enabled: true, Version: 1, Scope: service.PackageScope{Mode: "inherit_with_blacklist", AllowMemberIDs: []string{}, DenyMemberIDs: []string{}}, Skills: []service.PackageSkill{}, Plugins: []service.PackagePlugin{}}
			for _, s := range p.Skills {
				ref := refs[s.ResourceKey]
				pkg.Skills = append(pkg.Skills, service.PackageSkill{SkillID: ref.ID, VersionID: ref.VersionID})
			}
			for _, s := range p.Plugins {
				ref := refs[s.ResourceKey]
				if err := installPublicConnection(ctx, m.tx, w, ref.ID, ref.VersionID); err != nil {
					return err
				}
				pkg.Plugins = append(pkg.Plugins, service.PackagePlugin{PluginID: ref.ID, VersionID: ref.VersionID})
			}
			if e = (&packageTx{model: m}).Save(ctx, pkg, true); e != nil {
				return e
			}
			if _, e = m.tx.ExecContext(ctx, `UPDATE platform_capability_packages SET source_package_key=$2 WHERE id=$1`, pkg.ID, p.Key); e != nil {
				return e
			}
		}
		out, e = at.Get(ctx, out.ID)
		return e
	})
	return
}
func (s *TemplateStore) FromAgent(ctx context.Context, w, a, id string, av, cv int64, name string) (c service.TemplateContent, e error) {
	e = s.With(ctx, w, a, true, func(raw service.TemplateTx) error {
		m := raw.(*templateTx).model
		agent, err := (&agentTx{model: m}).Get(ctx, id)
		if err != nil {
			return err
		}
		if agent.Version != av || agent.ConfigVersion != cv {
			return service.Fail("agent_version_conflict", 409, "员工配置已更新")
		}
		c = service.TemplateContent{SchemaVersion: 1, Name: name, Avatar: agent.Avatar, Role: agent.Role, Instruction: agent.Instruction, Category: "office", Parameters: []service.TemplateParameter{}, Packages: []service.TemplatePackage{}, Resources: []service.TemplateResource{}, Examples: []service.TemplateExample{}}
		packages, _, err := (&packageTx{model: m}).List(ctx, id, 1, 100)
		if err != nil {
			return err
		}
		keys := map[string]string{}
		add := func(kind, resource, pin string) string {
			k := kind + resource + pin
			if keys[k] != "" {
				return keys[k]
			}
			key := fmt.Sprintf("resource_%d", len(keys)+1)
			keys[k] = key
			c.Resources = append(c.Resources, service.TemplateResource{Key: key, Kind: kind, WorkspaceID: w, ID: resource, VersionID: pin})
			return key
		}
		for i, p := range packages {
			tp := service.TemplatePackage{Key: fmt.Sprintf("package_%d", i+1), Name: p.Name, Description: p.Description, Onboarding: "required", Skills: []service.TemplatePin{}, Plugins: []service.TemplatePluginPin{}}
			for _, r := range p.Skills {
				tp.Skills = append(tp.Skills, service.TemplatePin{ResourceKey: add("skill", r.SkillID, r.VersionID)})
			}
			for _, r := range p.Plugins {
				tp.Plugins = append(tp.Plugins, service.TemplatePluginPin{ResourceKey: add("plugin", r.PluginID, r.VersionID)})
			}
			c.Packages = append(c.Packages, tp)
		}
		return nil
	})
	return
}
func (s *TemplateStore) Setup(ctx context.Context, w, a, id string) (out map[string]any, e error) {
	e = s.With(ctx, w, a, true, func(raw service.TemplateTx) error {
		m := raw.(*templateTx).model
		agent, err := (&agentTx{model: m}).Get(ctx, id)
		if err != nil {
			return err
		}
		allowed := false
		if err = m.tx.QueryRowContext(ctx, `SELECT platform_agent_web_allowed($1,$2,$3)`, w, id, a).Scan(&allowed); err != nil {
			return err
		}
		chat := agent.ModelAvailable && (agent.PlatformModelID == "" || agent.Payer != nil)
		packages, _, err := (&packageTx{model: m}).List(ctx, id, 1, 100)
		if err != nil {
			return err
		}
		issues := []map[string]any{}
		for _, p := range packages {
			if !p.Enabled {
				issues = append(issues, map[string]any{"package_id": p.ID, "resource_id": p.ID, "code": "disabled"})
			}
			for _, pin := range p.Skills {
				state, err := (&packageTx{model: m}).ValidateSkill(ctx, pin)
				if err != nil || !state.Enabled {
					issues = append(issues, map[string]any{"package_id": p.ID, "resource_id": pin.SkillID, "code": "resource_unavailable"})
				}
			}
			for _, pin := range p.Plugins {
				plugins := &pluginTx{model: m}
				code := ""
				def, de := plugins.GetVersion(ctx, pin.PluginID, pin.VersionID)
				if de != nil || !def.Enabled {
					code = "resource_unavailable"
				} else if ready, e := plugins.executionAccountReady(ctx, id, def); e != nil {
					return e
				} else if !ready {
					code = "needs_connection"
				}
				if code != "" {
					issues = append(issues, map[string]any{"package_id": p.ID, "resource_id": pin.PluginID, "code": code})
				}
			}
		}
		examples := []service.TemplateExample{}
		if agent.TemplateOrigin != nil {
			var data []byte
			if err = m.tx.QueryRowContext(ctx, `SELECT content FROM platform_agent_template_versions WHERE id=$1`, agent.TemplateOrigin.TemplateVersionID).Scan(&data); err != nil {
				return err
			}
			var c service.TemplateContent
			if err = json.Unmarshal(data, &c); err != nil {
				return err
			}
			examples = c.Examples
		}
		checks := []map[string]any{}
		for _, ex := range examples {
			can := allowed && chat && agent.ModelAvailable
			for _, key := range ex.PackageKeys {
				var pkg string
				err = m.tx.QueryRowContext(ctx, `SELECT id FROM platform_capability_packages WHERE workspace_id=$1 AND agent_id=$2 AND source_package_key=$3 AND enabled`, w, id, key).Scan(&pkg)
				if errors.Is(err, sql.ErrNoRows) {
					can = false
				} else if err != nil {
					return err
				}
				for _, issue := range issues {
					if issue["package_id"] == pkg {
						can = false
					}
				}
			}
			checks = append(checks, map[string]any{"key": ex.Key, "title": ex.Title, "text": ex.Text, "output": ex.Output, "package_keys": ex.PackageKeys, "can_try": can})
		}
		out = map[string]any{"conversation_model": map[string]any{"available": chat}, "task_model": map[string]any{"available": agent.ModelAvailable}, "access_allowed": allowed, "packages": packages, "issues": issues, "examples": checks}
		return nil
	})
	return
}

func importTemplateResources(ctx context.Context, m *modelTx, w, a, scope, version string, assets []service.TemplateAsset) (map[string]service.TemplateResource, error) {
	var e error
	refs := map[string]service.TemplateResource{}
	for _, candidate := range assets {
		x := candidate
		if scope == "official" && x.Resource.WorkspaceID != "" {
			x, e = findTemplateImport(ctx, m, version, x)
			if e != nil {
				return nil, e
			}
			if !x.Imported {
				if x.Resource.Kind == "skill" {
					candidate, err := (&skillTx{model: m}).Import(ctx, x.ImportID)
					if err != nil {
						return nil, err
					}
					if !candidate.Ready || candidate.ActorID != a || !time.Now().Before(candidate.ExpiresAt) || candidate.StorageIdentity != x.Skill.StorageIdentity || candidate.Source.CandidateID != x.TargetID || candidate.Source.CandidateVersionID != x.TargetVersionID || candidate.Source.CandidatePrefix != x.TargetPrefix {
						return nil, service.Fail("template_dependency_unavailable", 409, "技能候选已失效，请重新创建")
					}
					skill := service.Skill{ID: x.TargetID, Name: x.Skill.TechnicalName, Description: x.Skill.Description, Version: 1, Enabled: true, CurrentVersionID: x.TargetVersionID}
					pin := x.Skill
					pin.ID = x.TargetVersionID
					pin.Sequence = 1
					pin.KeyPrefix = x.TargetPrefix
					pin.Source = service.SkillSource{Type: "agent_template"}
					if e = (&skillTx{model: m}).Save(ctx, skill, pin, true); e != nil {
						return nil, e
					}
					if e = (&skillTx{model: m}).MarkPublished(ctx, x.ImportID, skill.ID, pin.ID, ""); e != nil {
						return nil, e
					}
				} else {
					p := x.Plugin
					p.ID = x.TargetID
					p.VersionID = x.TargetVersionID
					p.Version = 1
					p.Enabled = true
					if e = (&pluginTx{model: m}).Save(ctx, p, true, true); e != nil {
						return nil, e
					}
				}
				var sk, sv, pl, pv any
				if x.Resource.Kind == "skill" {
					sk = x.TargetID
					sv = x.TargetVersionID
				} else {
					pl = x.TargetID
					pv = x.TargetVersionID
				}
				if _, e = m.tx.ExecContext(ctx, `INSERT INTO platform_agent_template_resource_imports(workspace_id,template_version_id,resource_key,kind,skill_id,skill_version_id,plugin_id,plugin_version_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, w, version, x.Resource.Key, x.Resource.Kind, sk, sv, pl, pv); e != nil {
					return nil, e
				}
			}
			x.Resource.WorkspaceID = w
			x.Resource.ID = x.TargetID
			x.Resource.VersionID = x.TargetVersionID
		}
		var enabled bool
		table := "platform_skills"
		if x.Resource.Kind == "plugin" {
			table = "platform_plugins"
		}
		if e = m.tx.QueryRowContext(ctx, `SELECT enabled AND listed AND archived_at IS NULL FROM `+table+` WHERE (workspace_id=$1 OR workspace_id IS NULL) AND id=$2 FOR SHARE`, w, x.Resource.ID).Scan(&enabled); e != nil {
			return nil, e
		}
		if !enabled {
			return nil, service.Fail("template_dependency_unavailable", 409, "目标资源已停用")
		}
		refs[x.Resource.Key] = x.Resource
	}

	return refs, nil
}
