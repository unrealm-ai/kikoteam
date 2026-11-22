-- +goose Up
ALTER TABLE platform_employee_plugin_accounts RENAME TO platform_plugin_connections;
ALTER TABLE platform_plugin_connections
 DROP CONSTRAINT platform_employee_plugin_acco_workspace_id_agent_id_plugin__key,
 ALTER COLUMN agent_id DROP NOT NULL,
 ADD owner_type text GENERATED ALWAYS AS (CASE WHEN agent_id IS NULL THEN 'workspace' ELSE 'agent' END) STORED,
 ADD credential_revision bigint NOT NULL DEFAULT 1,
 ADD granted_scopes text NOT NULL DEFAULT '',
 ADD expires_at timestamptz,
 ADD external_account text NOT NULL DEFAULT '';
CREATE UNIQUE INDEX plugin_connections_workspace_version ON platform_plugin_connections(workspace_id,plugin_id,plugin_version_id) WHERE agent_id IS NULL;
CREATE UNIQUE INDEX plugin_connections_agent_version ON platform_plugin_connections(workspace_id,agent_id,plugin_id,plugin_version_id) WHERE agent_id IS NOT NULL;

CREATE TABLE platform_agent_plugin_connection_settings (
 workspace_id uuid NOT NULL, agent_id uuid NOT NULL, plugin_id uuid NOT NULL REFERENCES platform_plugins(id),
 source text NOT NULL CHECK(source IN ('workspace','agent')), revision bigint NOT NULL CHECK(revision>=0),
 updated_by uuid REFERENCES platform_accounts(id), PRIMARY KEY(workspace_id,agent_id,plugin_id),
 FOREIGN KEY(workspace_id,agent_id) REFERENCES platform_agents(workspace_id,id)
);
CREATE TRIGGER check_plugin_owner BEFORE INSERT OR UPDATE ON platform_agent_plugin_connection_settings
 FOR EACH ROW EXECUTE FUNCTION platform_check_plugin_owner();
INSERT INTO platform_agent_plugin_connection_settings(workspace_id,agent_id,plugin_id,source,revision)
 SELECT workspace_id,agent_id,plugin_id,'agent',0 FROM platform_plugin_connections;

-- All permission consumers resolve ownership using the same effective selection.
CREATE FUNCTION platform_plugin_connection_owner(w uuid,a uuid,p uuid) RETURNS uuid
 LANGUAGE sql STABLE AS 'SELECT a FROM platform_agent_plugin_connection_settings WHERE workspace_id=w AND agent_id=a AND plugin_id=p AND source=''agent''';

CREATE TABLE platform_plugin_oauth_clients (
 id uuid PRIMARY KEY, workspace_id uuid REFERENCES platform_workspaces(id),
 issuer text NOT NULL, client_id text NOT NULL, client_secret bytea NOT NULL
);
ALTER TABLE platform_plugin_versions DROP CONSTRAINT platform_plugin_versions_auth_type_check,
 ADD CHECK(auth_type IN ('none','bearer','api_key','oauth')),
 ADD oauth_client_id uuid REFERENCES platform_plugin_oauth_clients(id),
 ADD oauth_resource text NOT NULL DEFAULT '', ADD oauth_issuer text NOT NULL DEFAULT '', ADD oauth_scopes text NOT NULL DEFAULT '';

-- +goose StatementBegin
CREATE FUNCTION platform_check_oauth_client_owner() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.oauth_client_id IS NOT NULL AND NOT EXISTS (SELECT FROM platform_plugin_oauth_clients c
   WHERE c.id=NEW.oauth_client_id AND c.workspace_id IS NOT DISTINCT FROM NEW.workspace_id AND c.issuer=NEW.oauth_issuer) THEN
   RAISE EXCEPTION 'OAuth application owner or issuer mismatch' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER check_oauth_client_owner BEFORE INSERT OR UPDATE ON platform_plugin_versions
 FOR EACH ROW EXECUTE FUNCTION platform_check_oauth_client_owner();
CREATE TABLE platform_plugin_oauth_attempts (
 state_hash text PRIMARY KEY, workspace_id uuid NOT NULL REFERENCES platform_workspaces(id),
 actor_id uuid NOT NULL REFERENCES platform_accounts(id), session_hash text NOT NULL, agent_id uuid,
 plugin_id uuid NOT NULL, plugin_version_id uuid NOT NULL, account_version bigint NOT NULL,
 source_revision bigint NOT NULL, name text NOT NULL, issuer text NOT NULL, verifier bytea NOT NULL,
 expires_at timestamptz NOT NULL, FOREIGN KEY(workspace_id,agent_id) REFERENCES platform_agents(workspace_id,id),
 FOREIGN KEY(plugin_id,plugin_version_id) REFERENCES platform_plugin_versions(plugin_id,id)
);
CREATE INDEX plugin_oauth_attempts_expiry ON platform_plugin_oauth_attempts(expires_at);
CREATE TRIGGER check_plugin_owner BEFORE INSERT OR UPDATE ON platform_plugin_oauth_attempts
 FOR EACH ROW EXECUTE FUNCTION platform_check_plugin_owner();
ALTER TABLE manager_capability_calls ADD connection_source text, ADD agent_id uuid, ADD actor_ref text;

-- +goose Down
-- Rolling back after accepting shared or OAuth grants requires a data migration.
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT FROM platform_plugin_connections WHERE agent_id IS NULL) OR
 EXISTS(SELECT FROM platform_plugin_versions WHERE auth_type='oauth') OR
 EXISTS(SELECT FROM platform_agent_plugin_connection_settings WHERE source='workspace') THEN
 RAISE EXCEPTION 'plugin connections in use; migrate grants before rollback';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE manager_capability_calls DROP connection_source,DROP agent_id,DROP actor_ref;
DROP TABLE platform_plugin_oauth_attempts;
DROP TRIGGER check_oauth_client_owner ON platform_plugin_versions;
DROP FUNCTION platform_check_oauth_client_owner();
ALTER TABLE platform_plugin_versions DROP oauth_client_id,DROP oauth_resource,DROP oauth_issuer,DROP oauth_scopes,
 DROP CONSTRAINT platform_plugin_versions_auth_type_check, ADD CHECK(auth_type IN ('none','bearer','api_key'));
DROP TABLE platform_plugin_oauth_clients;
DROP FUNCTION platform_plugin_connection_owner(uuid,uuid,uuid);
DROP TABLE platform_agent_plugin_connection_settings;
DROP INDEX plugin_connections_workspace_version;
DROP INDEX plugin_connections_agent_version;
ALTER TABLE platform_plugin_connections ALTER COLUMN agent_id SET NOT NULL,
 ADD CONSTRAINT platform_employee_plugin_acco_workspace_id_agent_id_plugin__key UNIQUE(workspace_id,agent_id,plugin_id),DROP owner_type,DROP credential_revision,DROP granted_scopes,DROP expires_at,DROP external_account;
ALTER TABLE platform_plugin_connections RENAME TO platform_employee_plugin_accounts;
