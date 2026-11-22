-- +goose Up
CREATE TABLE platform_proxy_models (
 id uuid PRIMARY KEY,
 name text NOT NULL,
 protocol text NOT NULL CHECK (protocol IN ('chat_completions','openai_responses','anthropic_messages')),
 base_url text NOT NULL,
 upstream_model_id text NOT NULL,
 credential_ciphertext bytea,
 input_price_units bigint NOT NULL CHECK (input_price_units >= 0),
 output_price_units bigint NOT NULL CHECK (output_price_units >= 0),
 cache_read_price_units bigint CHECK (cache_read_price_units >= 0),
 cache_write_price_units bigint CHECK (cache_write_price_units >= 0),
 enabled boolean NOT NULL DEFAULT false,
 is_default boolean NOT NULL DEFAULT false,
 validated boolean NOT NULL DEFAULT false,
 version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
 tested_at timestamptz,
 updated_by uuid NOT NULL REFERENCES platform_accounts(id),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 CHECK (NOT enabled OR validated),
 CHECK (NOT is_default OR enabled)
);
CREATE UNIQUE INDEX platform_proxy_models_default ON platform_proxy_models (is_default) WHERE is_default;

ALTER TABLE platform_agents ALTER COLUMN model_profile_id DROP NOT NULL,
 ADD COLUMN platform_model_id uuid REFERENCES platform_proxy_models(id),
 ADD CHECK (num_nonnulls(model_profile_id,platform_model_id)=1);
ALTER TABLE platform_agent_versions ALTER COLUMN model_profile_id DROP NOT NULL,
 ADD COLUMN platform_model_id uuid REFERENCES platform_proxy_models(id),
 ADD CHECK (num_nonnulls(model_profile_id,platform_model_id)=1);

CREATE TABLE platform_credit_wallets (
 account_id uuid PRIMARY KEY REFERENCES platform_accounts(id),
 balance_units bigint NOT NULL DEFAULT 0,
 version bigint NOT NULL DEFAULT 0 CHECK (version >= 0),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO platform_credit_wallets(account_id) SELECT id FROM platform_accounts;

CREATE TABLE platform_credit_settings (
 id smallint PRIMARY KEY CHECK (id=1),
 signup_bonus_units bigint NOT NULL DEFAULT 1000000000 CHECK (signup_bonus_units >= 0),
 version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
 updated_by uuid REFERENCES platform_accounts(id),
 updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO platform_credit_settings(id) VALUES(1);

CREATE TABLE platform_agent_model_keys (
 agent_id uuid PRIMARY KEY,
 workspace_id uuid NOT NULL,
 payer_account_id uuid NOT NULL REFERENCES platform_credit_wallets(account_id),
 key_hash bytea NOT NULL UNIQUE,
 key_ciphertext bytea NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY (workspace_id,agent_id) REFERENCES platform_agents(workspace_id,id)
);
CREATE INDEX platform_agent_model_keys_payer ON platform_agent_model_keys(payer_account_id,workspace_id);

CREATE TABLE platform_model_usage (
 id uuid PRIMARY KEY,
 workspace_id uuid NOT NULL,
 agent_id uuid NOT NULL,
 payer_account_id uuid NOT NULL REFERENCES platform_credit_wallets(account_id),
 model_id uuid NOT NULL REFERENCES platform_proxy_models(id),
 price_snapshot jsonb NOT NULL,
 input_tokens bigint CHECK (input_tokens >= 0),
 output_tokens bigint CHECK (output_tokens >= 0),
 cache_read_tokens bigint CHECK (cache_read_tokens >= 0),
 cache_write_tokens bigint CHECK (cache_write_tokens >= 0),
 status text NOT NULL CHECK (status IN ('running','usage_recorded','settled','pending_review','no_charge')),
 upstream_request_id text,
 error_code text,
 resolution_reason text,
 resolved_by uuid REFERENCES platform_accounts(id),
 started_at timestamptz NOT NULL DEFAULT now(),
 deadline_at timestamptz NOT NULL,
 finished_at timestamptz,
 settled_at timestamptz,
 FOREIGN KEY (workspace_id,agent_id) REFERENCES platform_agents(workspace_id,id),
 CHECK (status NOT IN ('usage_recorded','settled') OR num_nonnulls(input_tokens,output_tokens,cache_read_tokens,cache_write_tokens)=4),
 CHECK ((status='settled') = (settled_at IS NOT NULL))
);
CREATE INDEX platform_model_usage_payer ON platform_model_usage(payer_account_id,started_at DESC,id DESC);
CREATE INDEX platform_model_usage_agent ON platform_model_usage(agent_id,started_at DESC,id DESC);
CREATE INDEX platform_model_usage_pending ON platform_model_usage(status,deadline_at,id) WHERE status IN ('running','usage_recorded');

CREATE TABLE platform_credit_ledger (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES platform_credit_wallets(account_id),
 wallet_version bigint NOT NULL CHECK (wallet_version > 0),
 kind text NOT NULL,
 amount_units bigint NOT NULL,
 balance_after_units bigint NOT NULL,
 agent_id uuid REFERENCES platform_agents(id),
 usage_id uuid UNIQUE REFERENCES platform_model_usage(id),
 biz_key text NOT NULL UNIQUE,
 request_fingerprint text,
 operator_id uuid REFERENCES platform_accounts(id),
 reason text,
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE (account_id,wallet_version),
 CHECK (
  (kind='signup_bonus' AND amount_units >= 0 AND usage_id IS NULL AND agent_id IS NULL)
  OR (kind='admin_grant' AND amount_units > 0 AND operator_id IS NOT NULL AND reason IS NOT NULL AND usage_id IS NULL AND agent_id IS NULL)
  OR (kind='model_charge' AND amount_units <= 0 AND usage_id IS NOT NULL AND agent_id IS NOT NULL)
 )
);
CREATE INDEX platform_credit_ledger_account ON platform_credit_ledger(account_id,created_at DESC,id DESC);
CREATE INDEX platform_credit_ledger_agent ON platform_credit_ledger(account_id,agent_id,created_at DESC,id DESC);

-- +goose Down
-- Removing employee references would destroy configuration history.
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'platform model proxy migration is forward only'; END $$;
-- +goose StatementEnd
