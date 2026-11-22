-- +goose Up
ALTER TABLE platform_accounts ADD COLUMN is_platform_admin boolean NOT NULL DEFAULT false, ADD COLUMN platform_admin_version bigint NOT NULL DEFAULT 1 CHECK(platform_admin_version>0);
CREATE TABLE platform_admin_state(singleton boolean PRIMARY KEY CHECK(singleton),initialized_by uuid REFERENCES platform_accounts(id),initialized_at timestamptz,CHECK((initialized_by IS NULL)=(initialized_at IS NULL)));
INSERT INTO platform_admin_state(singleton) VALUES(true);
CREATE TABLE platform_agent_templates(
 id uuid PRIMARY KEY,scope text NOT NULL CHECK(scope IN ('official','workspace')),owner_workspace_id uuid REFERENCES platform_workspaces(id),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),latest_version_id uuid NOT NULL,published_version_id uuid,is_listed boolean NOT NULL DEFAULT false,
 recommended_rank integer NOT NULL DEFAULT 0,created_by uuid NOT NULL REFERENCES platform_accounts(id),created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),
 CHECK((scope='workspace')=(owner_workspace_id IS NOT NULL)),CHECK(NOT is_listed OR published_version_id IS NOT NULL));
CREATE TABLE platform_agent_template_versions(
 id uuid PRIMARY KEY,template_id uuid NOT NULL REFERENCES platform_agent_templates(id) DEFERRABLE INITIALLY DEFERRED,sequence bigint NOT NULL CHECK(sequence>0),
 content jsonb NOT NULL CHECK(jsonb_typeof(content)='object'),change_note text NOT NULL DEFAULT '',created_by uuid NOT NULL REFERENCES platform_accounts(id),
 created_at timestamptz NOT NULL DEFAULT now(),published_by uuid REFERENCES platform_accounts(id),published_at timestamptz,
 UNIQUE(template_id,id),UNIQUE(template_id,sequence),CHECK((published_by IS NULL)=(published_at IS NULL)));
ALTER TABLE platform_agent_templates ADD FOREIGN KEY(id,latest_version_id) REFERENCES platform_agent_template_versions(template_id,id) DEFERRABLE INITIALLY DEFERRED,
 ADD FOREIGN KEY(id,published_version_id) REFERENCES platform_agent_template_versions(template_id,id) DEFERRABLE INITIALLY DEFERRED;
CREATE TABLE platform_agent_template_version_resources(
 template_version_id uuid NOT NULL REFERENCES platform_agent_template_versions(id),resource_key text NOT NULL,kind text NOT NULL CHECK(kind IN ('skill','plugin')),
 source_workspace_id uuid NOT NULL REFERENCES platform_workspaces(id),skill_id uuid,skill_version_id uuid,plugin_id uuid,plugin_version_id uuid,
 PRIMARY KEY(template_version_id,resource_key),
 CHECK((kind='skill' AND skill_id IS NOT NULL AND skill_version_id IS NOT NULL AND plugin_id IS NULL AND plugin_version_id IS NULL) OR (kind='plugin' AND plugin_id IS NOT NULL AND plugin_version_id IS NOT NULL AND skill_id IS NULL AND skill_version_id IS NULL)),
 FOREIGN KEY(source_workspace_id,skill_id,skill_version_id) REFERENCES platform_skill_versions(workspace_id,skill_id,id),
 FOREIGN KEY(source_workspace_id,plugin_id,plugin_version_id) REFERENCES platform_plugin_versions(workspace_id,plugin_id,id));
CREATE TABLE platform_agent_template_resource_imports(
 workspace_id uuid NOT NULL REFERENCES platform_workspaces(id),template_version_id uuid NOT NULL,resource_key text NOT NULL,kind text NOT NULL CHECK(kind IN ('skill','plugin')),
 skill_id uuid,skill_version_id uuid,plugin_id uuid,plugin_version_id uuid,created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(workspace_id,template_version_id,resource_key),FOREIGN KEY(template_version_id,resource_key) REFERENCES platform_agent_template_version_resources(template_version_id,resource_key),
 CHECK((kind='skill' AND skill_id IS NOT NULL AND skill_version_id IS NOT NULL AND plugin_id IS NULL AND plugin_version_id IS NULL) OR (kind='plugin' AND plugin_id IS NOT NULL AND plugin_version_id IS NOT NULL AND skill_id IS NULL AND skill_version_id IS NULL)),
 FOREIGN KEY(workspace_id,skill_id,skill_version_id) REFERENCES platform_skill_versions(workspace_id,skill_id,id),
 FOREIGN KEY(workspace_id,plugin_id,plugin_version_id) REFERENCES platform_plugin_versions(workspace_id,plugin_id,id));
ALTER TABLE platform_agents ADD COLUMN source_agent_template_version_id uuid REFERENCES platform_agent_template_versions(id),
 ADD COLUMN template_parameters jsonb NOT NULL DEFAULT '{}' CHECK(jsonb_typeof(template_parameters)='object'),
 ADD COLUMN web_access jsonb NOT NULL DEFAULT '{"mode":"workspace","allow_member_ids":[],"deny_member_ids":[]}' CHECK(COALESCE(jsonb_typeof(web_access)='object' AND web_access->>'mode' IN ('workspace','allowlist') AND jsonb_typeof(web_access->'allow_member_ids')='array' AND jsonb_typeof(web_access->'deny_member_ids')='array',false)),
 ADD CHECK(source_agent_template_version_id IS NOT NULL OR template_parameters='{}');
ALTER TABLE platform_capability_packages ADD COLUMN source_package_key text;
CREATE UNIQUE INDEX platform_package_source_key_idx ON platform_capability_packages(agent_id,source_package_key) WHERE source_package_key IS NOT NULL;
CREATE INDEX platform_agents_template_source_idx ON platform_agents(source_agent_template_version_id) WHERE source_agent_template_version_id IS NOT NULL;
CREATE INDEX platform_agent_templates_market_idx ON platform_agent_templates(scope,owner_workspace_id,is_listed,recommended_rank DESC,id);
-- +goose StatementBegin
CREATE FUNCTION platform_agent_web_allowed(workspace uuid,agent uuid,actor uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM platform_agents a JOIN platform_members m ON m.workspace_id=a.workspace_id AND m.user_id=actor AND m.status='active'
 WHERE a.workspace_id=workspace AND a.id=agent AND a.enabled
 AND NOT (a.web_access->'deny_member_ids' ? actor::text)
 AND (a.web_access->>'mode'='workspace' OR a.web_access->'allow_member_ids' ? actor::text));
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION manager_input_actor_allowed(input uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM manager_inputs i JOIN manager_sessions s ON s.id=i.session_id WHERE i.id=input AND
 ((i.submitted_by IS NOT NULL AND platform_agent_web_allowed(i.workspace_id,s.agent_id,i.submitted_by))
 OR (i.channel_principal_id IS NOT NULL AND manager_channel_actor_allowed(i.channel_principal_id,s.conversation_id,i.channel_generation) AND (i.channel_history_count=0 OR EXISTS(SELECT 1 FROM manager_channel_principals p JOIN platform_channel_bindings b ON b.id=p.binding_id WHERE p.id=i.channel_principal_id AND COALESCE((b.history->>'enabled')::boolean,false) AND COALESCE((b.history->>'limit')::integer,0)>=i.channel_history_count)))));
$$;
-- +goose StatementEnd
-- +goose Down
-- Role and template provenance must be preserved on application rollback.
