-- +goose Up
CREATE TABLE platform_group_sharing_policies (
 id uuid PRIMARY KEY,
 workspace_id uuid NOT NULL REFERENCES platform_workspaces(id),
 agent_id uuid NOT NULL REFERENCES platform_agents(id),
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 100),
 mode text NOT NULL CHECK(mode IN ('directed','mutual')),
 enabled boolean NOT NULL,
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 sources jsonb NOT NULL CHECK(jsonb_typeof(sources)='array' AND jsonb_array_length(sources) BETWEEN 1 AND 20),
 targets jsonb NOT NULL CHECK(jsonb_typeof(targets)='array' AND jsonb_array_length(targets)<=20),
 created_by uuid NOT NULL REFERENCES platform_accounts(id),
 updated_by uuid NOT NULL REFERENCES platform_accounts(id),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 CHECK ((mode='directed' AND jsonb_array_length(targets)>0) OR (mode='mutual' AND jsonb_array_length(sources)>1 AND jsonb_array_length(targets)=0))
);
CREATE INDEX platform_group_sharing_policies_employee ON platform_group_sharing_policies(workspace_id,agent_id,id);
CREATE TABLE platform_group_sharing_edges (
 policy_id uuid NOT NULL REFERENCES platform_group_sharing_policies(id) ON DELETE CASCADE,
 ordinal integer NOT NULL CHECK(ordinal>=0 AND ordinal<400),
 source_ref jsonb NOT NULL CHECK(jsonb_typeof(source_ref)='object' AND source_ref->>'kind'='group'),
 target_ref jsonb NOT NULL CHECK(jsonb_typeof(target_ref)='object' AND target_ref->>'kind' IN ('group','user')),
 PRIMARY KEY(policy_id,ordinal)
);
CREATE TABLE platform_group_sharing_policy_events (
 id uuid PRIMARY KEY,
 workspace_id uuid NOT NULL REFERENCES platform_workspaces(id),
 agent_id uuid NOT NULL REFERENCES platform_agents(id),
 policy_id uuid NOT NULL,
 actor_id uuid NOT NULL REFERENCES platform_accounts(id),
 action text NOT NULL CHECK(action IN ('created','updated','deleted')),
 version bigint NOT NULL CHECK(version>0),
 snapshot jsonb NOT NULL CHECK(jsonb_typeof(snapshot)='object'),
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX platform_group_sharing_policy_events_policy ON platform_group_sharing_policy_events(policy_id,version);
-- +goose Down
DROP TABLE platform_group_sharing_policy_events;
DROP TABLE platform_group_sharing_edges;
DROP TABLE platform_group_sharing_policies;
