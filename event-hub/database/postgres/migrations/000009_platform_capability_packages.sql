-- +goose Up
-- NULL agent_id denotes a workspace template; copying creates an independent employee package.
CREATE TABLE platform_capability_packages (
 id uuid PRIMARY KEY,
 workspace_id uuid NOT NULL REFERENCES platform_workspaces(id),
 agent_id uuid,
 source_template_id uuid,
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 100),
 description text NOT NULL DEFAULT '' CHECK(length(description)<=2000),
 enabled boolean NOT NULL DEFAULT true,
 version bigint NOT NULL CHECK(version>0),
 scope jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(workspace_id,id),
 FOREIGN KEY(workspace_id,agent_id) REFERENCES platform_agents(workspace_id,id),
 FOREIGN KEY(workspace_id,source_template_id) REFERENCES platform_capability_packages(workspace_id,id)
);
CREATE INDEX platform_packages_employee_idx ON platform_capability_packages(workspace_id,agent_id,created_at DESC,id DESC);
CREATE TABLE platform_capability_package_skills (
 workspace_id uuid NOT NULL,
 package_id uuid NOT NULL,
 skill_id uuid NOT NULL,
 version_id uuid NOT NULL,
 PRIMARY KEY(package_id,skill_id),
 FOREIGN KEY(workspace_id,package_id) REFERENCES platform_capability_packages(workspace_id,id),
 FOREIGN KEY(workspace_id,skill_id) REFERENCES platform_skills(workspace_id,id),
 FOREIGN KEY(workspace_id,skill_id,version_id) REFERENCES platform_skill_versions(workspace_id,skill_id,id)
);
CREATE TABLE platform_capability_package_plugins (
 workspace_id uuid NOT NULL,
 package_id uuid NOT NULL,
 plugin_id uuid NOT NULL,
 version_id uuid NOT NULL,
 tools jsonb NOT NULL CHECK(jsonb_typeof(tools)='array'),
 PRIMARY KEY(package_id,plugin_id),
 FOREIGN KEY(workspace_id,package_id) REFERENCES platform_capability_packages(workspace_id,id),
 FOREIGN KEY(workspace_id,plugin_id) REFERENCES platform_plugins(workspace_id,id),
 FOREIGN KEY(workspace_id,plugin_id,version_id) REFERENCES platform_plugin_versions(workspace_id,plugin_id,id)
);
-- Immutable package configuration revisions, not a separately published package registry.
CREATE TABLE platform_capability_package_versions (
 workspace_id uuid NOT NULL,
 package_id uuid NOT NULL,
 version bigint NOT NULL CHECK(version>0),
 snapshot jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(package_id,version),
 FOREIGN KEY(workspace_id,package_id) REFERENCES platform_capability_packages(workspace_id,id)
);
-- +goose Down
DROP TABLE platform_capability_package_versions;
DROP TABLE platform_capability_package_plugins;
DROP TABLE platform_capability_package_skills;
DROP TABLE platform_capability_packages;
