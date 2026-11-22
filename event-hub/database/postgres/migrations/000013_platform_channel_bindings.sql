-- +goose Up
CREATE TABLE platform_channel_config_clock (id boolean PRIMARY KEY DEFAULT true CHECK(id), revision bigint NOT NULL DEFAULT 0, notified_revision bigint NOT NULL DEFAULT 0);
INSERT INTO platform_channel_config_clock(id) VALUES(true);
CREATE TABLE platform_channel_bindings (
 id uuid PRIMARY KEY, workspace_id uuid NOT NULL, agent_id uuid NOT NULL,
 provider text NOT NULL CHECK(provider='feishu'), identity_epoch bigint NOT NULL DEFAULT 1 CHECK(identity_epoch>0), connection_generation bigint NOT NULL CHECK(connection_generation>0),
 rules_version bigint NOT NULL CHECK(rules_version>0), config_revision bigint NOT NULL CHECK(config_revision>0),
 enabled boolean NOT NULL DEFAULT false, app_id text NOT NULL, external_tenant_id text NOT NULL DEFAULT '', bot_open_id text NOT NULL DEFAULT '',
 credential_ciphertext bytea NOT NULL, trigger jsonb NOT NULL, history jsonb NOT NULL, access jsonb NOT NULL,
 applied_generation bigint NOT NULL DEFAULT 0, applied_rules_version bigint NOT NULL DEFAULT 0, applied_revision bigint NOT NULL DEFAULT 0,
 applied_status text NOT NULL DEFAULT 'pending', applied_error text NOT NULL DEFAULT '', updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(workspace_id,agent_id,provider), FOREIGN KEY(workspace_id,agent_id) REFERENCES platform_agents(workspace_id,id)
);
-- +goose Down
DROP TABLE platform_channel_bindings;
DROP TABLE platform_channel_config_clock;
