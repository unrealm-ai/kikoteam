package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type PackageStore struct{ DB *sql.DB }

func NewPackageStore(db *sql.DB) *PackageStore { return &PackageStore{DB: db} }
func (s *PackageStore) WithWorkspace(ctx context.Context, w string, fn func(service.PackageTx) error) error {
	return (&ModelStore{DB: s.DB}).WithWorkspace(ctx, w, func(t service.ModelTx) error { return fn(&packageTx{model: t.(*modelTx)}) })
}

type packageTx struct{ model *modelTx }

func (t *packageTx) Member(ctx context.Context, id string) (service.Member, error) {
	return t.model.Member(ctx, id)
}
func (t *packageTx) Employee(ctx context.Context, id string) error {
	var found string
	e := t.model.tx.QueryRowContext(ctx, `SELECT id FROM platform_agents WHERE workspace_id=$1 AND id=$2`, t.model.workspace, id).Scan(&found)
	return workspaceDBError(e)
}

const packageColumns = `id,COALESCE(agent_id::text,''),COALESCE(source_template_id::text,''),name,description,enabled,version,scope,trigger_scope,conversation_scope,work_scope,created_at,updated_at`

func scanPackage(row scanner) (service.CapabilityPackage, error) {
	var p service.CapabilityPackage
	var scope, triggerScope, conversationScope, workScope []byte
	e := row.Scan(&p.ID, &p.AgentID, &p.SourceTemplateID, &p.Name, &p.Description, &p.Enabled, &p.Version, &scope, &triggerScope, &conversationScope, &workScope, &p.CreatedAt, &p.UpdatedAt)
	if e != nil {
		return p, workspaceDBError(e)
	}
	e = json.Unmarshal(scope, &p.Scope)
	if e == nil {
		e = json.Unmarshal(triggerScope, &p.TriggerScope)
	}
	if e == nil && len(conversationScope) > 0 {
		e = json.Unmarshal(conversationScope, &p.ConversationScope)
	}
	if e == nil && len(workScope) > 0 {
		e = json.Unmarshal(workScope, &p.WorkScope)
	}
	return p, e
}
func (t *packageTx) Get(ctx context.Context, agent, id string) (service.CapabilityPackage, error) {
	p, e := scanPackage(t.model.tx.QueryRowContext(ctx, `SELECT `+packageColumns+` FROM platform_capability_packages WHERE workspace_id=$1 AND id=$2 AND agent_id IS NOT DISTINCT FROM NULLIF($3,'')::uuid`, t.model.workspace, id, agent))
	if e != nil {
		return p, e
	}
	e = t.loadResources(ctx, &p)
	return p, e
}
func (t *packageTx) List(ctx context.Context, agent string, page, size int) (result []service.CapabilityPackage, count int, resultErr error) {
	out := []service.CapabilityPackage{}
	var total int
	if e := t.model.tx.QueryRowContext(ctx, `SELECT count(*) FROM platform_capability_packages WHERE workspace_id=$1 AND agent_id IS NOT DISTINCT FROM NULLIF($2,'')::uuid`, t.model.workspace, agent).Scan(&total); e != nil {
		return out, 0, e
	}
	rows, e := t.model.tx.QueryContext(ctx, `SELECT `+packageColumns+` FROM platform_capability_packages WHERE workspace_id=$1 AND agent_id IS NOT DISTINCT FROM NULLIF($2,'')::uuid ORDER BY created_at DESC,id DESC LIMIT $3 OFFSET $4`, t.model.workspace, agent, size, (page-1)*size)
	if e != nil {
		return out, 0, e
	}
	defer func() { resultErr = errors.Join(resultErr, rows.Close()) }()
	for rows.Next() {
		p, e := scanPackage(rows)
		if e != nil {
			return out, 0, e
		}
		out = append(out, p)
	}
	e = rows.Err()
	if e != nil {
		return out, 0, e
	}
	for i := range out {
		if e = t.loadResources(ctx, &out[i]); e != nil {
			return out, 0, e
		}
	}
	return out, total, nil
}
func (t *packageTx) loadResources(ctx context.Context, p *service.CapabilityPackage) (resultErr error) {
	p.Skills = []service.PackageSkill{}
	p.Plugins = []service.PackagePlugin{}
	skillRows, e := t.model.tx.QueryContext(ctx, `SELECT b.skill_id,b.version_id FROM platform_capability_package_skills b WHERE b.workspace_id=$1 AND b.package_id=$2 ORDER BY b.skill_id`, t.model.workspace, p.ID)
	if e != nil {
		return e
	}
	defer func() { resultErr = errors.Join(resultErr, skillRows.Close()) }()
	for skillRows.Next() {
		var r service.PackageSkill
		if e = skillRows.Scan(&r.SkillID, &r.VersionID); e != nil {
			return e
		}
		p.Skills = append(p.Skills, r)
	}
	e = skillRows.Err()
	if e != nil {
		return e
	}
	rows, e := t.model.tx.QueryContext(ctx, `SELECT b.plugin_id,b.version_id FROM platform_capability_package_plugins b WHERE b.workspace_id=$1 AND b.package_id=$2 ORDER BY b.plugin_id`, t.model.workspace, p.ID)
	if e != nil {
		return e
	}
	defer rows.Close() //nolint:errcheck // Iteration errors checked below.
	for rows.Next() {
		var r service.PackagePlugin
		if e = rows.Scan(&r.PluginID, &r.VersionID); e != nil {
			return e
		}
		p.Plugins = append(p.Plugins, r)
	}
	return rows.Err()
}
func (t *packageTx) ValidateSkill(ctx context.Context, r service.PackageSkill) (service.PackageResource, error) {
	var out service.PackageResource
	e := t.model.tx.QueryRowContext(ctx, `SELECT s.enabled, NOT s.listed, s.current_version_id FROM platform_skills s JOIN platform_skill_versions v ON v.skill_id=s.id WHERE (s.workspace_id=$1 OR s.workspace_id IS NULL) AND s.archived_at IS NULL AND s.id=$2 AND v.id=coalesce(nullif($3,'')::uuid,s.current_version_id) FOR SHARE OF s`, t.model.workspace, r.SkillID, r.VersionID).Scan(&out.Enabled, &out.Unlisted, &out.CurrentVersionID)
	if errors.Is(e, sql.ErrNoRows) {
		return out, service.Fail("skill_unavailable", 409, "技能版本不存在或不属于当前工作区")
	}
	return out, e
}
func (t *packageTx) ValidatePlugin(ctx context.Context, r service.PackagePlugin) (service.PackageResource, error) {
	var out service.PackageResource
	e := t.model.tx.QueryRowContext(ctx, `SELECT p.enabled, NOT p.listed, p.current_version_id FROM platform_plugins p JOIN platform_plugin_versions v ON v.plugin_id=p.id WHERE (p.workspace_id=$1 OR p.workspace_id IS NULL) AND p.archived_at IS NULL AND p.id=$2 AND v.id=coalesce(nullif($3,'')::uuid,p.current_version_id) FOR SHARE OF p`, t.model.workspace, r.PluginID, r.VersionID).Scan(&out.Enabled, &out.Unlisted, &out.CurrentVersionID)
	if errors.Is(e, sql.ErrNoRows) {
		return out, service.Fail("plugin_unavailable", 409, "插件版本不存在或不属于当前工作区")
	}
	return out, e
}
func (t *packageTx) Save(ctx context.Context, p service.CapabilityPackage, create bool) error {
	if p.TriggerScope.Mode == "" {
		if len(p.TriggerScope.AllowTriggerIDs) > 0 || len(p.TriggerScope.DenyTriggerIDs) > 0 {
			return service.Fail("invalid_package_trigger_scope", 400, "触发器范围缺少模式")
		}
		enabled := p.TriggerScope.Enabled
		p.TriggerScope = service.DefaultPackageTriggerScope()
		p.TriggerScope.Enabled = enabled
	}
	scope, e := json.Marshal(p.Scope)
	if e != nil {
		return e
	}
	triggerScope, e := json.Marshal(p.TriggerScope)
	if e != nil {
		return e
	}
	conversationScope, e := json.Marshal(p.ConversationScope)
	if e != nil {
		return e
	}
	workScope, e := json.Marshal(p.WorkScope)
	if e != nil {
		return e
	}
	args := []any{p.ID, t.model.workspace, p.AgentID, p.SourceTemplateID, p.Name, p.Description, p.Enabled, p.Version, scope, triggerScope, conversationScope, workScope}
	if create {
		_, e = t.model.tx.ExecContext(ctx, `INSERT INTO platform_capability_packages(id,workspace_id,agent_id,source_template_id,name,description,enabled,version,scope,trigger_scope,conversation_scope,work_scope) VALUES($1,$2,NULLIF($3,'')::uuid,NULLIF($4,'')::uuid,$5,$6,$7,$8,$9,$10,NULLIF($11::jsonb,'null'::jsonb),NULLIF($12::jsonb,'null'::jsonb))`, args...)
	} else {
		var result sql.Result
		result, e = t.model.tx.ExecContext(ctx, `UPDATE platform_capability_packages SET name=$5,description=$6,enabled=$7,version=$8,scope=$9,trigger_scope=$10,conversation_scope=NULLIF($11::jsonb,'null'::jsonb),work_scope=NULLIF($12::jsonb,'null'::jsonb),updated_at=now() WHERE id=$1 AND workspace_id=$2 AND agent_id IS NOT DISTINCT FROM NULLIF($3,'')::uuid AND source_template_id IS NOT DISTINCT FROM NULLIF($4,'')::uuid AND version=$8-1`, args...)
		if e == nil {
			var n int64
			n, e = result.RowsAffected()
			if e == nil && n != 1 {
				return service.Fail("package_version_conflict", 409, "能力包已更新，请刷新后重试")
			}
		}
	}
	if e != nil {
		return fmt.Errorf("save capability package: %w", e)
	}
	if e = t.saveResources(ctx, p); e != nil {
		return e
	}
	return t.recordVersion(ctx, p.AgentID, p.ID)
}

func (t *packageTx) recordVersion(ctx context.Context, agent, id string) error {
	current, e := t.Get(ctx, agent, id)
	if e != nil {
		return e
	}
	snapshot, e := json.Marshal(current)
	if e != nil {
		return e
	}
	_, e = t.model.tx.ExecContext(ctx, `INSERT INTO platform_capability_package_versions(workspace_id,package_id,version,snapshot) VALUES($1,$2,$3,$4)`, t.model.workspace, current.ID, current.Version, snapshot)
	return e
}
func (t *packageTx) saveResources(ctx context.Context, p service.CapabilityPackage) error {
	// Resolve before replacing links so editing the last package preserves its pin.
	for i, r := range p.Skills {
		version, err := t.bindingVersion(ctx, p.AgentID, "skill", r.SkillID)
		if err != nil {
			return err
		}
		p.Skills[i].VersionID = version
	}
	for i, r := range p.Plugins {
		version, err := t.bindingVersion(ctx, p.AgentID, "plugin", r.PluginID)
		if err != nil {
			return err
		}
		p.Plugins[i].VersionID = version
	}
	for _, table := range []string{"platform_capability_package_skills", "platform_capability_package_plugins"} {
		if _, e := t.model.tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE workspace_id=$1 AND package_id=$2`, t.model.workspace, p.ID); e != nil {
			return e
		}
	}
	for _, r := range p.Skills {
		if _, e := t.model.tx.ExecContext(ctx, `INSERT INTO platform_capability_package_skills(workspace_id,package_id,skill_id,version_id) VALUES($1,$2,$3,$4)`, t.model.workspace, p.ID, r.SkillID, r.VersionID); e != nil {
			return e
		}
	}
	for _, r := range p.Plugins {
		if _, e := t.model.tx.ExecContext(ctx, `INSERT INTO platform_capability_package_plugins(workspace_id,package_id,plugin_id,version_id) VALUES($1,$2,$3,$4)`, t.model.workspace, p.ID, r.PluginID, r.VersionID); e != nil {
			return e
		}
	}
	return nil
}

func (t *packageTx) ValidateTrigger(ctx context.Context, agent, id string) error {
	var exists bool
	err := t.model.tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM manager_triggers WHERE id=$1 AND workspace_id=$2 AND agent_id=$3)`, id, t.model.workspace, agent).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return service.Fail("package_trigger_unavailable", 409, "触发器不属于当前员工")
	}
	return nil
}

func (t *packageTx) bindingVersion(ctx context.Context, agent, kind, id string) (string, error) {
	var version string
	query := `SELECT COALESCE((SELECT b.version_id FROM platform_capability_package_` + kind + `s b
JOIN platform_capability_packages pkg ON pkg.id=b.package_id WHERE b.workspace_id=$1 AND pkg.agent_id=NULLIF($2,'')::uuid AND b.` + kind + `_id=r.id LIMIT 1),r.current_version_id)
FROM platform_` + kind + `s r WHERE (r.workspace_id=$1 OR r.workspace_id IS NULL) AND r.id=$3`
	err := t.model.tx.QueryRowContext(ctx, query, t.model.workspace, agent, id).Scan(&version)
	return version, workspaceDBError(err)
}

func (t *packageTx) Upgrade(ctx context.Context, agent string, r service.ResourceUpgrade) error {
	kind := r.Kind // validated by the service
	selected, err := t.bindingVersion(ctx, agent, kind, r.ResourceID)
	if err != nil {
		return err
	}
	var state service.PackageResource
	if kind == "skill" {
		state, err = t.ValidateSkill(ctx, service.PackageSkill{SkillID: r.ResourceID})
	} else {
		state, err = t.ValidatePlugin(ctx, service.PackagePlugin{PluginID: r.ResourceID})
	}
	if err != nil {
		return err
	}
	if !state.Enabled || state.CurrentVersionID != r.ToVersionID || (selected != r.FromVersionID && selected != r.ToVersionID) {
		return service.Fail("capability_version_conflict", 409, "资源版本已变更，请刷新后再升级")
	}
	links := "platform_capability_package_" + kind + "s"
	rows, err := t.model.tx.QueryContext(ctx, `UPDATE `+links+` b SET version_id=$4 FROM platform_capability_packages p
WHERE b.package_id=p.id AND p.workspace_id=$1 AND p.agent_id=$2 AND b.`+kind+`_id=$3 RETURNING p.id`, t.model.workspace, agent, r.ResourceID, r.ToVersionID)
	if err != nil {
		return err
	}
	defer rows.Close() //nolint:errcheck // Iteration errors are checked below.
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if len(ids) == 0 {
		return service.Fail("resource_unbound", 409, "该员工尚未使用此资源")
	}
	if selected == r.ToVersionID {
		return nil
	}
	if kind == "plugin" {
		plugins := &pluginTx{model: t.model}
		p, e := plugins.GetVersion(ctx, r.ResourceID, r.ToVersionID)
		if e != nil {
			return e
		}
		ready, e := plugins.executionAccountReady(ctx, agent, p)
		if e != nil {
			return e
		}
		if !ready {
			return service.Fail("plugin_account_required", 409, "请先为新版本连接可用账号，再升级")
		}
	}
	for _, id := range ids {
		if _, err = t.model.tx.ExecContext(ctx, `UPDATE platform_capability_packages SET version=version+1,updated_at=now() WHERE id=$1 AND agent_id=$2 AND workspace_id=$3`, id, agent, t.model.workspace); err != nil {
			return err
		}
		if err = t.recordVersion(ctx, agent, id); err != nil {
			return err
		}
	}
	return nil
}
