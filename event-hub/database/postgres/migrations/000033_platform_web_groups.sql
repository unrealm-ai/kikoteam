-- +goose Up
CREATE TABLE platform_web_groups (
 id uuid PRIMARY KEY,workspace_id uuid NOT NULL REFERENCES platform_workspaces(id),title text NOT NULL CHECK(length(title) BETWEEN 1 AND 200),
 status text NOT NULL DEFAULT 'active' CHECK(status IN ('active','archived')),version bigint NOT NULL DEFAULT 1,
 admission_epoch bigint NOT NULL DEFAULT 1,
 created_by uuid NOT NULL REFERENCES platform_accounts(id),created_at timestamptz NOT NULL DEFAULT now(),UNIQUE(workspace_id,id)
);
CREATE TABLE platform_web_group_members (
 group_id uuid NOT NULL,workspace_id uuid NOT NULL,user_id uuid NOT NULL,
 role text NOT NULL CHECK(role IN ('owner','admin','member')),status text NOT NULL CHECK(status IN ('active','removed')),
 admission_epoch bigint NOT NULL DEFAULT 1,
 PRIMARY KEY(group_id,user_id),FOREIGN KEY(workspace_id,group_id) REFERENCES platform_web_groups(workspace_id,id),
 FOREIGN KEY(workspace_id,user_id) REFERENCES platform_members(workspace_id,user_id)
);
CREATE TABLE platform_web_group_agents (
 group_id uuid NOT NULL,workspace_id uuid NOT NULL,agent_id uuid NOT NULL,
 status text NOT NULL CHECK(status IN ('active','removed')),admission_epoch bigint NOT NULL DEFAULT 1,
 PRIMARY KEY(group_id,agent_id),FOREIGN KEY(workspace_id,group_id) REFERENCES platform_web_groups(workspace_id,id),
 FOREIGN KEY(workspace_id,agent_id) REFERENCES platform_agents(workspace_id,id)
);
CREATE UNIQUE INDEX platform_web_group_one_owner ON platform_web_group_members(group_id) WHERE role='owner' AND status='active';
-- +goose StatementBegin
CREATE FUNCTION platform_web_group_member_allowed(workspace uuid,group_id uuid,actor uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM platform_web_groups g JOIN platform_web_group_members gm ON gm.group_id=g.id AND gm.workspace_id=g.workspace_id JOIN platform_members m ON m.workspace_id=g.workspace_id AND m.user_id=gm.user_id WHERE g.id=$2 AND g.workspace_id=$1 AND g.status='active' AND gm.user_id=$3 AND gm.status='active' AND m.status='active');
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION platform_web_group_agent_allowed(workspace uuid,group_id uuid,agent uuid,actor uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT platform_web_group_member_allowed($1,$2,$4) AND platform_agent_web_allowed($1,$3,$4) AND EXISTS(SELECT 1 FROM platform_web_group_agents ga JOIN platform_agents a ON a.id=ga.agent_id AND a.workspace_id=ga.workspace_id WHERE ga.group_id=$2 AND ga.workspace_id=$1 AND ga.agent_id=$3 AND ga.status='active' AND a.enabled);
$$;
-- +goose StatementEnd
-- +goose Down
DROP FUNCTION platform_web_group_agent_allowed(uuid,uuid,uuid,uuid);
DROP FUNCTION platform_web_group_member_allowed(uuid,uuid,uuid);
DROP TABLE platform_web_group_agents,platform_web_group_members,platform_web_groups;
