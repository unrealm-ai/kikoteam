-- +goose Up
CREATE TABLE platform_plugins (
 id uuid PRIMARY KEY, workspace_id uuid NOT NULL REFERENCES platform_workspaces(id),
 name text NOT NULL, description text NOT NULL DEFAULT '', enabled boolean NOT NULL DEFAULT true,
 version bigint NOT NULL CHECK(version>0), current_version_id uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(workspace_id,id)
);
CREATE TABLE platform_plugin_versions (
 id uuid PRIMARY KEY, workspace_id uuid NOT NULL, plugin_id uuid NOT NULL,
 version bigint NOT NULL CHECK(version>0), name text NOT NULL, description text NOT NULL,
 endpoint text NOT NULL, auth_type text NOT NULL CHECK(auth_type IN ('none','bearer','api_key')), header_name text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(workspace_id,plugin_id,id), UNIQUE(workspace_id,plugin_id,version),
 FOREIGN KEY(workspace_id,plugin_id) REFERENCES platform_plugins(workspace_id,id)
);
ALTER TABLE platform_plugins ADD CONSTRAINT platform_plugins_current_version_fk FOREIGN KEY(workspace_id,id,current_version_id) REFERENCES platform_plugin_versions(workspace_id,plugin_id,id) DEFERRABLE INITIALLY DEFERRED;
CREATE TABLE platform_employee_plugin_accounts (
 id uuid PRIMARY KEY, workspace_id uuid NOT NULL, agent_id uuid NOT NULL, plugin_id uuid NOT NULL, plugin_version_id uuid NOT NULL,
 name text NOT NULL, version bigint NOT NULL CHECK(version>0), enabled boolean NOT NULL, validated boolean NOT NULL,
 tools jsonb NOT NULL DEFAULT '[]', credential bytea NOT NULL, validated_at timestamptz NOT NULL,
 UNIQUE(workspace_id,agent_id,plugin_id), UNIQUE(workspace_id,id),
 FOREIGN KEY(workspace_id,agent_id) REFERENCES platform_agents(workspace_id,id),
 FOREIGN KEY(workspace_id,plugin_id,plugin_version_id) REFERENCES platform_plugin_versions(workspace_id,plugin_id,id)
);
CREATE INDEX platform_plugins_list_idx ON platform_plugins(workspace_id,created_at DESC,id DESC);
-- +goose Down
DROP TABLE platform_employee_plugin_accounts;
ALTER TABLE platform_plugins DROP CONSTRAINT platform_plugins_current_version_fk;
DROP TABLE platform_plugin_versions;
DROP TABLE platform_plugins;
