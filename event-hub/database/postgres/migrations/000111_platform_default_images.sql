-- +goose Up
ALTER TABLE platform_plugin_versions
 ADD execution_kind text NOT NULL DEFAULT 'remote',
 ADD distribution text NOT NULL DEFAULT 'manual',
 ADD handler_key text,
 ADD default_tools jsonb NOT NULL DEFAULT '[]',
 ADD default_scenes jsonb NOT NULL DEFAULT '[]',
 ADD CHECK ((execution_kind='remote' AND distribution='manual' AND handler_key IS NULL)
 OR (execution_kind='platform_managed' AND distribution='all_agents' AND workspace_id IS NULL AND handler_key IS NOT NULL));

CREATE TABLE platform_default_plugin_settings (
 plugin_id uuid PRIMARY KEY REFERENCES platform_plugins(id),
 enabled boolean NOT NULL DEFAULT false,
 version bigint NOT NULL DEFAULT 0 CHECK(version >= 0),
 active_config_version bigint,
 updated_by uuid REFERENCES platform_accounts(id),
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE platform_image_generation_config_versions (
 plugin_id uuid NOT NULL REFERENCES platform_default_plugin_settings(plugin_id),
 version bigint NOT NULL CHECK(version > 0),
 provider text NOT NULL, base_url text NOT NULL, upstream_model_id text NOT NULL,
 credential_ciphertext bytea NOT NULL,
 unit_price_units bigint NOT NULL CHECK(unit_price_units > 0),
 supported_sizes jsonb NOT NULL,
 max_concurrent_generations integer NOT NULL CHECK(max_concurrent_generations > 0),
 workspace_daily_image_limit integer NOT NULL CHECK(workspace_daily_image_limit > 0),
 timeout_seconds integer NOT NULL CHECK(timeout_seconds BETWEEN 1 AND 300),
 validated_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(), created_by uuid NOT NULL REFERENCES platform_accounts(id),
 PRIMARY KEY(plugin_id,version)
);
CREATE TABLE platform_image_generations (
 id uuid PRIMARY KEY, workspace_id uuid NOT NULL, agent_id uuid NOT NULL,
 payer_account_id uuid NOT NULL REFERENCES platform_credit_wallets(account_id),
 task_id uuid NOT NULL REFERENCES manager_tasks(id), session_id uuid NOT NULL REFERENCES manager_sessions(id),
 input_id uuid NOT NULL REFERENCES manager_inputs(id), turn_id uuid NOT NULL REFERENCES manager_turns(id),
 request_key text NOT NULL, request_fingerprint bytea NOT NULL,
 plugin_id uuid NOT NULL, plugin_version_id uuid NOT NULL, config_version bigint NOT NULL,
 unit_price_units bigint NOT NULL CHECK(unit_price_units > 0), aspect_ratio text NOT NULL,
 quota_date date NOT NULL DEFAULT ((now() AT TIME ZONE 'UTC')::date),
 generation_status text NOT NULL CHECK(generation_status IN ('admitted','dispatched','pending_review','ready','failed')),
 billing_status text NOT NULL CHECK(billing_status IN ('pending','settled','no_charge')),
 image_count smallint, upstream_request_id text, error_code text,
 started_at timestamptz NOT NULL DEFAULT now(), dispatched_at timestamptz, deadline_at timestamptz NOT NULL, finished_at timestamptz,
 asset_version text, output_path text, mime_type text, size_bytes bigint, width integer, height integer,
 settled_at timestamptz, resolved_by uuid REFERENCES platform_accounts(id), resolution_reason text, resolved_at timestamptz,
 UNIQUE(workspace_id,task_id,request_key),
 FOREIGN KEY(workspace_id,agent_id) REFERENCES platform_agents(workspace_id,id),
 FOREIGN KEY(plugin_id,plugin_version_id) REFERENCES platform_plugin_versions(plugin_id,id),
 FOREIGN KEY(plugin_id,config_version) REFERENCES platform_image_generation_config_versions(plugin_id,version),
 CHECK ((generation_status='ready' AND image_count IS NOT DISTINCT FROM 1 AND asset_version IS NOT NULL AND output_path IS NOT NULL
  AND COALESCE(width,0)>0 AND COALESCE(height,0)>0 AND COALESCE(size_bytes,0)>0 AND mime_type IS NOT DISTINCT FROM 'image/png' AND billing_status IN ('pending','settled'))
 OR (generation_status='failed' AND image_count IS NOT DISTINCT FROM 0 AND billing_status='no_charge')
 OR (generation_status IN ('admitted','dispatched','pending_review') AND image_count IS NULL AND billing_status='pending')),
 CHECK ((billing_status='settled')=(settled_at IS NOT NULL))
);
CREATE INDEX platform_images_payer ON platform_image_generations(payer_account_id,started_at DESC,id DESC);
CREATE INDEX platform_images_quota ON platform_image_generations(workspace_id,quota_date,generation_status);
CREATE INDEX platform_images_pending ON platform_image_generations(deadline_at,id) WHERE billing_status='pending';
CREATE INDEX platform_images_running ON platform_image_generations(id) WHERE generation_status IN ('admitted','dispatched');
ALTER TABLE platform_credit_ledger ADD image_generation_id uuid REFERENCES platform_image_generations(id),
 DROP CONSTRAINT platform_credit_ledger_check,
 ADD CHECK (
 (kind='signup_bonus' AND amount_units>=0 AND usage_id IS NULL AND agent_id IS NULL AND image_generation_id IS NULL)
 OR (kind='admin_grant' AND amount_units>0 AND operator_id IS NOT NULL AND reason IS NOT NULL AND usage_id IS NULL AND agent_id IS NULL AND image_generation_id IS NULL)
 OR (kind='model_charge' AND amount_units<=0 AND usage_id IS NOT NULL AND agent_id IS NOT NULL AND image_generation_id IS NULL)
 OR (kind='referral_bonus' AND amount_units=100000000 AND usage_id IS NULL AND agent_id IS NULL AND operator_id IS NULL AND image_generation_id IS NULL)
 OR (kind='image_charge' AND amount_units<0 AND usage_id IS NULL AND agent_id IS NOT NULL AND image_generation_id IS NOT NULL)
 );

CREATE UNIQUE INDEX platform_ledger_image_charge ON platform_credit_ledger(image_generation_id) WHERE image_generation_id IS NOT NULL;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'preserve image billing and artifact history; use a forward migration'; END $$;
-- +goose StatementEnd
