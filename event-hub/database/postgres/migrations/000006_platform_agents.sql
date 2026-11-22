-- +goose Up
-- Platform owns employee configuration. Runtime execution is not created here.
CREATE TABLE platform_agents (
 id uuid PRIMARY KEY,
 workspace_id uuid NOT NULL REFERENCES platform_workspaces(id),
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 100),
 role text NOT NULL DEFAULT '' CHECK(length(role)<=2000),
 avatar text NOT NULL DEFAULT '' CHECK(length(avatar)<=100),
 instruction text NOT NULL DEFAULT '' CHECK(length(instruction)<=8000),
 model_profile_id uuid NOT NULL,
 enabled boolean NOT NULL DEFAULT true,
 version bigint NOT NULL CHECK(version>0),
 config_version bigint NOT NULL CHECK(config_version>0),
 admission_epoch bigint NOT NULL CHECK(admission_epoch>0),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(workspace_id,id),
 FOREIGN KEY(workspace_id,model_profile_id) REFERENCES platform_model_profiles(workspace_id,id)
);
CREATE INDEX platform_agents_workspace_idx ON platform_agents(workspace_id,created_at DESC,id DESC);
CREATE TABLE platform_agent_versions (
 workspace_id uuid NOT NULL,
 agent_id uuid NOT NULL,
 version bigint NOT NULL CHECK(version>0),
 name text NOT NULL,
 role text NOT NULL,
 avatar text NOT NULL,
 instruction text NOT NULL,
 model_profile_id uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(agent_id,version),
 FOREIGN KEY(workspace_id,agent_id) REFERENCES platform_agents(workspace_id,id),
 FOREIGN KEY(workspace_id,model_profile_id) REFERENCES platform_model_profiles(workspace_id,id)
);
-- +goose Down
DROP TABLE platform_agent_versions;
DROP TABLE platform_agents;
