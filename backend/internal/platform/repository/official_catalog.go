package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func stageCatalog(ctx context.Context, catalog service.TemplateCatalog, assets service.SkillAssets) ([]service.SkillVersion, error) {
	versions := make([]service.SkillVersion, 0, len(catalog.Skills))
	for _, s := range catalog.Skills {
		ref := s.Ref()
		b, err := s.Prepare(ctx, assets)
		if err != nil {
			return nil, err
		}
		v := service.SkillVersion{ID: ref.VersionID, VersionLabel: s.SHA256[:12], TechnicalName: b.TechnicalName, Description: b.Description, Manifest: b.Manifest, Source: b.Source, StorageIdentity: assets.Identity(), KeyPrefix: fmt.Sprintf("official/skills/%s/versions/%s", ref.ID, ref.VersionID)}
		if err := assets.Write(ctx, v.KeyPrefix, b); err != nil {
			return nil, err
		}
		for _, file := range v.Manifest.Files {
			if _, err := assets.Read(ctx, v.KeyPrefix, file); err != nil {
				return nil, err
			}
		}
		versions = append(versions, v)
	}
	return versions, nil
}

func catalogResourceState(ctx context.Context, tx *sql.Tx, table string, ref service.TemplateResource, key string) (int64, string, error) {
	var revision int64
	var current string
	err := tx.QueryRowContext(ctx, "SELECT version,current_version_id FROM "+table+" WHERE id=$1 AND builtin_key=$2 AND workspace_id IS NULL FOR UPDATE", ref.ID, key).Scan(&revision, &current)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", nil
	}
	return revision, current, err
}
func syncCatalogResources(ctx context.Context, tx *sql.Tx, c service.TemplateCatalog, versions []service.SkillVersion) error {
	model := &modelTx{tx: tx}
	for i, s := range c.Skills {
		r := s.Ref()
		revision, prior, err := catalogResourceState(ctx, tx, "platform_skills", r, s.Key)
		if err != nil {
			return err
		}
		if prior == r.VersionID {
			if _, err := tx.ExecContext(ctx, "UPDATE platform_skills SET name=$2,version=version+1,updated_at=now() WHERE id=$1 AND name<>$2", r.ID, s.Name); err != nil {
				return err
			}
			continue
		}
		v := versions[i]
		v.Sequence = revision + 1
		_, err = tx.ExecContext(ctx, "UPDATE platform_skills SET name=$2,description=$3,current_version_id=$4,version=version+1,updated_at=now() WHERE id=$1 AND EXISTS (SELECT FROM platform_skill_versions WHERE id=$4 AND skill_id=$1)", r.ID, s.Name, v.Description, v.ID)
		if err != nil {
			return err
		}
		var exists bool
		if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT FROM platform_skill_versions WHERE skill_id=$1 AND id=$2)", r.ID, v.ID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			row := service.Skill{ID: r.ID, Name: s.Name, CurrentVersionID: v.ID, Version: revision + 1, Enabled: true, Description: v.Description}
			if err = (&skillTx{model}).Save(ctx, row, v, revision == 0); err != nil {
				return err
			}
		}
		if _, err = tx.ExecContext(ctx, "UPDATE platform_skills SET builtin_key=$2,name=$3 WHERE id=$1", r.ID, s.Key, s.Name); err != nil {
			return err
		}
	}
	for _, p := range c.Plugins {
		r := p.Ref()
		revision, prior, err := catalogResourceState(ctx, tx, "platform_plugins", r, p.Key)
		if err != nil {
			return err
		}
		if prior == r.VersionID {
			continue
		}
		row := p.Definition()
		if revision != 0 {
			if err = tx.QueryRowContext(ctx, "SELECT enabled FROM platform_plugins WHERE id=$1", r.ID).Scan(&row.Enabled); err != nil {
				return err
			}
		}
		row.Version = revision + 1
		var exists bool
		if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT FROM platform_plugin_versions WHERE plugin_id=$1 AND id=$2)", r.ID, r.VersionID).Scan(&exists); err != nil {
			return err
		}
		if err = (&pluginTx{model}).Save(ctx, row, revision == 0, !exists); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE platform_plugins SET builtin_key=$2 WHERE id=$1", r.ID, p.Key); err != nil {
			return err
		}
		if len(p.PublicTools) > 0 {
			data, err := json.Marshal(p.PublicTools)
			if err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, "UPDATE platform_plugin_versions SET public_tools=$2 WHERE id=$1", r.VersionID, data); err != nil {
				return err
			}
			if p.ExecutionKind == "platform_managed" {
				scenes, err := json.Marshal(p.DefaultScenes)
				if err != nil {
					return err
				}
				if _, err = tx.ExecContext(ctx, `UPDATE platform_plugin_versions SET execution_kind=$2,distribution=$3,handler_key=$4,
				 default_tools=$5,default_scenes=$6 WHERE id=$1 AND workspace_id IS NULL`, r.VersionID, p.ExecutionKind, p.Distribution, p.HandlerKey, data, scenes); err != nil {
					return err
				}
				if _, err = tx.ExecContext(ctx, `INSERT INTO platform_default_plugin_settings(plugin_id,enabled) VALUES ($1,$2) ON CONFLICT DO NOTHING`, r.ID, !p.RequiresConfiguration); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func refreshDefaultPluginListings(ctx context.Context, tx *sql.Tx, plugins []service.CatalogPlugin) error {
	managedKeys := []string{}
	for _, p := range plugins {
		if p.Distribution == "all_agents" {
			managedKeys = append(managedKeys, p.Key)
		}
	}
	// Retired defaults disappear at activation. Keep admin configuration and old pins
	// for a later re-release, authorized replay, and billing reconciliation.
	_, err := tx.ExecContext(ctx, `UPDATE platform_plugins p SET listed=p.builtin_key=ANY($1::text[]),version=p.version+1,updated_at=now()
	FROM platform_plugin_versions v WHERE v.id=p.current_version_id AND v.distribution='all_agents'
	AND p.listed IS DISTINCT FROM (p.builtin_key=ANY($1::text[]))`, managedKeys)
	return err
}

func saveTemplateRefs(ctx context.Context, tx *sql.Tx, version string, refs []service.TemplateResource) error {
	for _, r := range refs {
		var sk, sv, pl, pv any
		if r.Kind == "skill" {
			sk, sv = r.ID, r.VersionID
		} else {
			pl, pv = r.ID, r.VersionID
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO platform_agent_template_version_resources(template_version_id,resource_key,kind,source_workspace_id,skill_id,skill_version_id,plugin_id,plugin_version_id) VALUES($1,$2,$3,NULLIF($4,'')::uuid,$5,$6,$7,$8)`, version, r.Key, r.Kind, r.WorkspaceID, sk, sv, pl, pv); err != nil {
			return err
		}
	}
	return nil
}

// Revoked or exclusively scoped connections are never replaced. Only resource
// versions with a reviewed public tool set can bootstrap a shared connection.
func installPublicConnection(ctx context.Context, tx *sql.Tx, w, plugin, version string) error {
	if plugin != service.CatalogID("plugin", "stock") && plugin != service.CatalogID("plugin", "exa") {
		return nil
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO platform_plugin_connections(id,workspace_id,plugin_id,plugin_version_id,name,version,enabled,validated,tools,credential,validated_at,auth_method)
 SELECT $4,$1,p.id,v.id,p.name,1,true,true,v.public_tools,''::bytea,now(),'none'
 FROM platform_plugin_versions v JOIN platform_plugins p ON p.id=v.plugin_id
 WHERE p.id=$2 AND v.id=$3 AND p.workspace_id IS NULL AND p.builtin_key IS NOT NULL AND p.enabled AND v.auth_type='none' AND v.public_tools IS NOT NULL
 ON CONFLICT (workspace_id,plugin_id,plugin_version_id) WHERE agent_id IS NULL DO NOTHING`, w, plugin, version, service.NewID())
	return err
}
