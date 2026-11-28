package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

// SnapshotCapabilities reads Platform facts inside the caller's short admission
// transaction. Asset and network I/O are deliberately outside that transaction.
func SnapshotCapabilities(ctx context.Context, tx *sql.Tx, w, actor, employee string) (service.ExecutionCapabilities, error) {
	out := service.ExecutionCapabilities{ID: service.NewID(), Packages: []service.CapabilityPackage{}, Accounts: []service.RuntimeAccountPin{}}
	if err := authorizeSnapshotSource(ctx, tx, w, actor, employee); err != nil {
		return out, err
	}
	defaults, err := defaultPluginPins(ctx, tx)
	if err != nil {
		return out, err
	}
	out.DefaultPlugins = defaults
	rows, err := tx.QueryContext(ctx, `SELECT v.snapshot FROM platform_capability_packages p JOIN platform_capability_package_versions v ON v.workspace_id=p.workspace_id AND v.package_id=p.id AND v.version=p.version WHERE p.workspace_id=$1 AND p.agent_id=$2 ORDER BY p.id`, w, employee)
	if err != nil {
		return out, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Iteration and explicit close errors are checked.
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return out, err
		}
		var p service.CapabilityPackage
		if err = json.Unmarshal(raw, &p); err != nil {
			return out, err
		}
		out.Packages = append(out.Packages, p)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if err = rows.Close(); err != nil {
		return out, err
	}
	selected := []service.CapabilityPackage{}
	evaluator := &packageTx{model: &modelTx{tx: tx, workspace: w}}
	for _, p := range out.Packages {
		allowed, e := evaluator.PackageScopeAllows(ctx, p, actor)
		if e != nil {
			return out, e
		}
		if allowed {
			selected = append(selected, p)
		}
	}
	out.Packages = selected
	for i, p := range out.Packages {
		filtered, e := snapshotUsableResources(ctx, evaluator, p)
		if e != nil {
			return out, e
		}
		out.Packages[i] = filtered
	}
	rows, err = tx.QueryContext(ctx, `SELECT c.plugin_id,c.id,c.version,c.plugin_version_id,c.owner_type,COALESCE(s.revision,0) FROM platform_plugin_connections c LEFT JOIN platform_agent_plugin_connection_settings s ON s.workspace_id=c.workspace_id AND s.agent_id=$2 AND s.plugin_id=c.plugin_id WHERE c.workspace_id=$1 AND c.agent_id IS NOT DISTINCT FROM platform_plugin_connection_owner($1,$2,c.plugin_id) AND c.enabled AND (c.validated OR EXISTS (SELECT FROM platform_plugin_versions v WHERE v.id=c.plugin_version_id AND v.auth_type='none')) ORDER BY c.plugin_id,c.plugin_version_id`, w, employee)
	if err != nil {
		return out, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Iteration errors are checked below.
	for rows.Next() {
		var p service.RuntimeAccountPin
		if err = rows.Scan(&p.PluginID, &p.ID, &p.Version, &p.PluginVersionID, &p.Source, &p.SourceRevision); err != nil {
			return out, err
		}
		for _, pack := range out.Packages {
			found := false
			for _, plugin := range pack.Plugins {
				if plugin.PluginID == p.PluginID && plugin.VersionID == p.PluginVersionID {
					found = true
					break
				}
			}
			if found {
				out.Accounts = append(out.Accounts, p)
				break
			}
		}
	}
	return out, rows.Err()
}

func authorizeSnapshotSource(ctx context.Context, tx *sql.Tx, w, actor, employee string) error {
	if grant, work := service.WorkExecutionFrom(ctx); work {
		if actor != "" || grant.AssignmentID == "" || grant.SourceAgentID == "" || employee == "" {
			return service.Fail("work_execution_forbidden", 403, "事项配置身份无效")
		}
		var valid bool
		err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM platform_agents a JOIN platform_agents src ON src.workspace_id=a.workspace_id WHERE a.id=$1 AND src.id=$2 AND a.workspace_id=$3 AND a.enabled AND src.enabled)`, employee, grant.SourceAgentID, w).Scan(&valid)
		if err != nil {
			return err
		}
		if !valid {
			return service.Fail("work_execution_forbidden", 403, "交接员工不可用")
		}
		return nil
	}
	tr, trigger := service.TriggerExecutionFrom(ctx)
	if trigger {
		_, channel := service.ChannelExecutionFrom(ctx)
		_, group := service.WebGroupExecutionFrom(ctx)
		_, space := service.TaskSpaceExecutionFrom(ctx)
		if channel || group || space || actor != "" {
			return service.Fail("trigger_execution_forbidden", 403, "运行身份冲突")
		}
		if err := (&modelTx{tx: tx, workspace: w}).AuthorizeTriggerExecution(ctx, tr, actor, employee, false); err != nil {
			return err
		}
	}
	g, group := service.WebGroupExecutionFrom(ctx)
	sp, space := service.TaskSpaceExecutionFrom(ctx)
	if space {
		_, channel := service.ChannelExecutionFrom(ctx)
		if channel || group {
			return service.Fail("task_space_execution_forbidden", 403, "运行身份冲突")
		}
		if err := (&modelTx{tx: tx, workspace: w}).AuthorizeTaskSpaceExecution(ctx, sp, actor, employee, false); err != nil {
			return err
		}
	}
	if group {
		if _, channel := service.ChannelExecutionFrom(ctx); channel {
			return service.Fail("web_group_execution_forbidden", 403, "运行身份冲突")
		}
		if err := (&modelTx{tx: tx, workspace: w}).AuthorizeWebGroupExecution(ctx, g, actor, employee, false); err != nil {
			return err
		}
	}
	if _, channel := service.ChannelExecutionFrom(ctx); !channel && !group && !space && !trigger {
		if e := (&modelTx{tx: tx, workspace: w}).AuthorizeWebEmployee(ctx, actor, employee); e != nil {
			return e
		}
	}
	if c, channel := service.ChannelExecutionFrom(ctx); channel {
		if c.PrincipalID != actor {
			return service.Fail("channel_execution_forbidden", 403, "渠道主体不匹配")
		}
		if err := (&modelTx{tx: tx, workspace: w}).AuthorizeChannelExecution(ctx, c, employee, false); err != nil {
			return err
		}
	}
	return nil
}

func snapshotUsableResources(ctx context.Context, t *packageTx, p service.CapabilityPackage) (service.CapabilityPackage, error) {
	if err := t.loadResources(ctx, &p); err != nil {
		return p, err
	}
	skills := []service.PackageSkill{}
	for _, pin := range p.Skills {
		state, err := t.ValidateSkill(ctx, pin)
		if snapshotUnavailable(err) {
			continue
		}
		if err != nil {
			return p, err
		}
		if state.Enabled {
			skills = append(skills, pin)
		}
	}
	plugins := []service.PackagePlugin{}
	for _, pin := range p.Plugins {
		state, err := t.ValidatePlugin(ctx, pin)
		if snapshotUnavailable(err) {
			continue
		}
		if err != nil {
			return p, err
		}
		if state.Enabled {
			if err = ensureExecutionConnection(ctx, t.model.tx, t.model.workspace, p.AgentID, pin); err != nil {
				return p, err
			}
			plugins = append(plugins, pin)
		}
	}
	p.Skills, p.Plugins = skills, plugins
	return p, nil
}

// An anonymous plugin has no credential to request. Admit its connection for
// discovery; revocation leaves a disabled row and is never undone here.
func ensureExecutionConnection(ctx context.Context, tx *sql.Tx, workspace, employee string, pin service.PackagePlugin) error {
	if err := installPublicConnection(ctx, tx, workspace, pin.PluginID, pin.VersionID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO platform_plugin_connections(id,workspace_id,agent_id,plugin_id,plugin_version_id,name,version,enabled,validated,tools,credential,validated_at,auth_method)
SELECT $1,$2,platform_plugin_connection_owner($2,$5,v.plugin_id),v.plugin_id,v.id,v.name,1,true,false,'[]',''::bytea,now(),'none' FROM platform_plugin_versions v
WHERE v.id=$3 AND v.plugin_id=$4 AND v.auth_type='none'
ON CONFLICT DO NOTHING`, service.NewID(), workspace, pin.VersionID, pin.PluginID, employee)
	return err
}

func snapshotUnavailable(err error) bool {
	var e *service.Error
	return errors.Is(err, service.ErrWorkspaceMissing) || errors.As(err, &e) && (e.Status == 404 || e.Status == 409)
}
