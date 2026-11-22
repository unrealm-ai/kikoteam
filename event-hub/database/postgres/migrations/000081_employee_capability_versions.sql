-- +goose Up
-- The previous release resolved every binding to the current catalog version.
UPDATE platform_capability_package_skills b SET version_id=s.current_version_id
FROM platform_skills s WHERE s.id=b.skill_id AND b.version_id<>s.current_version_id;
UPDATE platform_capability_package_plugins b SET version_id=p.current_version_id
FROM platform_plugins p WHERE p.id=b.plugin_id AND b.version_id<>p.current_version_id;
ALTER TABLE platform_capability_package_plugins DROP COLUMN tools;

ALTER TABLE platform_plugin_versions DROP COLUMN catalog_tools;
ALTER TABLE manager_turns ADD COLUMN capability_snapshot jsonb;
DELETE FROM manager_session_plugin_tools;
ALTER TABLE manager_session_plugin_tools DROP CONSTRAINT manager_session_plugin_tools_pkey;
ALTER TABLE manager_session_plugin_tools ADD COLUMN snapshot_id uuid NOT NULL;
ALTER TABLE manager_session_plugin_tools ADD PRIMARY KEY(session_id,snapshot_id,plugin_version_id);

-- +goose Down
ALTER TABLE platform_plugin_versions ADD COLUMN catalog_tools text[] NOT NULL DEFAULT '{}';
DELETE FROM manager_session_plugin_tools;
ALTER TABLE manager_session_plugin_tools DROP COLUMN snapshot_id;
ALTER TABLE manager_session_plugin_tools ADD PRIMARY KEY(session_id,plugin_version_id);
ALTER TABLE manager_turns DROP COLUMN capability_snapshot;
ALTER TABLE platform_capability_package_plugins ADD COLUMN tools jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(tools)='array');
