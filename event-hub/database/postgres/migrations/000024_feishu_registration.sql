-- +goose Up
CREATE TABLE platform_feishu_registrations (
 id uuid PRIMARY KEY,
 workspace_id uuid NOT NULL,
 agent_id uuid NOT NULL,
 actor_id uuid NOT NULL REFERENCES platform_accounts(id),
 operation_key text NOT NULL CHECK(length(operation_key) BETWEEN 1 AND 128),
 state text NOT NULL CHECK(state IN ('starting','awaiting_confirmation','polling','credentials_saved','checking','needs_attention','connected','cancelled','expired','denied','failed','interrupted')),
 version bigint NOT NULL DEFAULT 1,
 record jsonb NOT NULL,
 device_ciphertext bytea,
 secret_ciphertext bytea,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(workspace_id,agent_id,operation_key),
 FOREIGN KEY(workspace_id,agent_id) REFERENCES platform_agents(workspace_id,id)
);
CREATE UNIQUE INDEX platform_feishu_registrations_active ON platform_feishu_registrations(workspace_id,agent_id) WHERE state IN ('starting','awaiting_confirmation','polling','credentials_saved','checking','needs_attention');
CREATE INDEX platform_feishu_registrations_latest ON platform_feishu_registrations(workspace_id,agent_id,actor_id,created_at DESC,id DESC);
-- +goose Down
DROP TABLE platform_feishu_registrations;
