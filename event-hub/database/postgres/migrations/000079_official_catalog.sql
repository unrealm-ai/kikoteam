-- +goose Up
ALTER TABLE platform_skills ADD COLUMN builtin_key text UNIQUE CHECK (builtin_key IS NULL OR workspace_id IS NULL);
ALTER TABLE platform_plugins ADD COLUMN builtin_key text UNIQUE CHECK (builtin_key IS NULL OR workspace_id IS NULL);
-- Last known catalog of a public service. Runtime discovery checks its actual
-- schemas again; grants and revocations still belong to each workspace.
ALTER TABLE platform_plugin_versions ADD COLUMN catalog_tools text[] NOT NULL DEFAULT '{}';
ALTER TABLE platform_plugin_versions ADD COLUMN public_tools jsonb CHECK(jsonb_typeof(public_tools)='array');
-- Existing workspaces choose their own employees. Newly created workspaces
-- provision one starter after a usable model and the catalog become available.
ALTER TABLE platform_workspaces ADD COLUMN starter_pending boolean NOT NULL DEFAULT false;
ALTER TABLE platform_workspaces ALTER COLUMN starter_pending SET DEFAULT true;
CREATE INDEX platform_workspaces_pending_starter ON platform_workspaces(created_at,id) WHERE starter_pending;
ALTER TABLE platform_plugin_oauth_attempts ADD COLUMN tenant_client_id uuid;
CREATE TABLE platform_catalog_oauth_settings (
 workspace_id uuid NOT NULL REFERENCES platform_workspaces(id) ON DELETE CASCADE,
 plugin_version_id uuid NOT NULL REFERENCES platform_plugin_versions(id),
 client_id uuid NOT NULL REFERENCES platform_plugin_oauth_clients(id),
 endpoint text NOT NULL,
 scopes text NOT NULL,
 read_tools text[] NOT NULL,
 PRIMARY KEY (workspace_id,plugin_version_id)
);
-- +goose Down
DROP TABLE platform_catalog_oauth_settings;
ALTER TABLE platform_plugin_oauth_attempts DROP COLUMN tenant_client_id;
ALTER TABLE platform_workspaces DROP COLUMN starter_pending;
ALTER TABLE platform_plugin_versions DROP COLUMN public_tools, DROP COLUMN catalog_tools;
ALTER TABLE platform_plugins DROP COLUMN builtin_key;
ALTER TABLE platform_skills DROP COLUMN builtin_key;
