-- +goose Up
CREATE TABLE platform_video_generation_configs (
 plugin_id uuid NOT NULL REFERENCES platform_default_plugin_settings(plugin_id),
 version bigint NOT NULL CHECK(version>0), base_url text NOT NULL, upstream_model_id text NOT NULL,
 credential_ciphertext bytea NOT NULL, unit_price_units bigint NOT NULL CHECK(unit_price_units BETWEEN 1 AND 614891469123651720),
 max_concurrent_generations integer NOT NULL CHECK(max_concurrent_generations>0),
 workspace_daily_seconds integer NOT NULL CHECK(workspace_daily_seconds>0),
 created_at timestamptz NOT NULL DEFAULT now(), created_by uuid NOT NULL REFERENCES platform_accounts(id),
 PRIMARY KEY(plugin_id,version)
);
CREATE TABLE platform_video_generations (
 id uuid PRIMARY KEY, workspace_id uuid NOT NULL, agent_id uuid NOT NULL,
 payer_account_id uuid NOT NULL REFERENCES platform_credit_wallets(account_id),
 task_id uuid NOT NULL REFERENCES manager_tasks(id), session_id uuid NOT NULL REFERENCES manager_sessions(id),
 input_id uuid NOT NULL REFERENCES manager_inputs(id), stop_revision bigint NOT NULL,
 request_key text NOT NULL, request_fingerprint bytea NOT NULL, plugin_id uuid NOT NULL, config_version bigint NOT NULL,
 unit_price_units bigint NOT NULL CHECK(unit_price_units BETWEEN 1 AND 614891469123651720),
 seconds integer NOT NULL CHECK(seconds BETWEEN 1 AND 15), aspect_ratio text NOT NULL,
 quota_date date NOT NULL DEFAULT ((now() AT TIME ZONE 'UTC')::date),
 status text NOT NULL CHECK(status IN ('submitting','queued','in_progress','saving','pending_review','ready','failed')),
 upstream_id text, started_at timestamptz NOT NULL DEFAULT now(),
 next_check_at timestamptz NOT NULL DEFAULT now(), check_token uuid,
 billing_status text NOT NULL DEFAULT 'pending' CHECK(billing_status IN ('pending','settled','no_charge')),
 reviewed_by uuid REFERENCES platform_accounts(id),review_reason text,reviewed_at timestamptz,
 asset_version text, error_code text, notified boolean NOT NULL DEFAULT false,
 UNIQUE(workspace_id,task_id,request_key), FOREIGN KEY(workspace_id,agent_id) REFERENCES platform_agents(workspace_id,id),
 FOREIGN KEY(plugin_id,config_version) REFERENCES platform_video_generation_configs(plugin_id,version),
 CHECK ((status='ready')=(asset_version IS NOT NULL)), CHECK ((status='failed')=(billing_status='no_charge')),
 CHECK (billing_status<>'settled' OR status='ready')
);
-- Keep the original actor and trigger event on a generated continuation. Trigger
-- authorization normally admits only the event's first input, so resolve the
-- original input again at admission and at tool use. Later stops revoke it.
ALTER TABLE manager_inputs ADD video_generation_id uuid REFERENCES platform_video_generations(id),
 ADD CHECK(video_generation_id IS NULL OR (source_kind='system' AND delivery_mode='queue' AND trigger_agent_loop));
CREATE UNIQUE INDEX manager_video_completion_input ON manager_inputs(video_generation_id) WHERE video_generation_id IS NOT NULL;
ALTER FUNCTION manager_input_actor_allowed(uuid) RENAME TO manager_input_actor_before_video;
ALTER FUNCTION manager_input_admission_allowed(uuid) RENAME TO manager_input_admission_before_video;

-- +goose StatementBegin
CREATE FUNCTION manager_input_actor_allowed(input_id uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT CASE WHEN i.video_generation_id IS NULL THEN manager_input_actor_before_video(i.id) ELSE EXISTS(
   SELECT FROM platform_video_generations g JOIN manager_inputs src ON src.id=g.input_id
   JOIN manager_sessions s ON s.id=g.session_id JOIN platform_agents a ON a.id=s.agent_id
   WHERE g.id=i.video_generation_id AND g.status IN ('ready','failed') AND g.session_id=i.session_id AND g.workspace_id=i.workspace_id
   AND src.session_id=i.session_id AND src.seq<i.seq AND src.admission_epoch=i.admission_epoch AND a.admission_epoch=i.admission_epoch AND a.enabled
   AND s.lifecycle='active' AND s.stop_revision=g.stop_revision AND s.applied_stop_revision=g.stop_revision
   AND src.submitted_by IS NOT DISTINCT FROM i.submitted_by AND src.channel_principal_id IS NOT DISTINCT FROM i.channel_principal_id
   AND src.channel_generation IS NOT DISTINCT FROM i.channel_generation AND src.trigger_event_id IS NOT DISTINCT FROM i.trigger_event_id
   AND src.web_group_member_epoch IS NOT DISTINCT FROM i.web_group_member_epoch
   AND NOT EXISTS(SELECT FROM manager_human_requests h WHERE h.id=src.human_request_id AND h.state='revised')
   AND manager_input_actor_allowed(src.id)) END FROM manager_inputs i WHERE i.id=input_id;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION manager_input_admission_allowed(input_id uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT CASE WHEN i.video_generation_id IS NULL THEN manager_input_admission_before_video(i.id)
 ELSE manager_input_actor_allowed(i.id) END FROM manager_inputs i WHERE i.id=input_id;
$$;
-- +goose StatementEnd

CREATE INDEX platform_videos_pending ON platform_video_generations(next_check_at,id) WHERE NOT notified;
CREATE INDEX platform_videos_task_started ON platform_video_generations(task_id,started_at DESC,id DESC);
CREATE INDEX platform_videos_inflight ON platform_video_generations(id) WHERE status IN ('submitting','queued','in_progress','saving');
CREATE INDEX platform_videos_quota ON platform_video_generations(workspace_id,quota_date,status);
ALTER TABLE platform_credit_ledger ADD video_generation_id uuid REFERENCES platform_video_generations(id),
 DROP CONSTRAINT platform_credit_ledger_check,
 ADD CHECK (
 (kind='signup_bonus' AND amount_units>=0 AND usage_id IS NULL AND agent_id IS NULL AND image_generation_id IS NULL AND video_generation_id IS NULL)
 OR (kind='admin_grant' AND amount_units>0 AND operator_id IS NOT NULL AND reason IS NOT NULL AND usage_id IS NULL AND agent_id IS NULL AND image_generation_id IS NULL AND video_generation_id IS NULL)
 OR (kind='model_charge' AND amount_units<=0 AND usage_id IS NOT NULL AND agent_id IS NOT NULL AND image_generation_id IS NULL AND video_generation_id IS NULL)
 OR (kind='referral_bonus' AND amount_units=100000000 AND usage_id IS NULL AND agent_id IS NULL AND operator_id IS NULL AND image_generation_id IS NULL AND video_generation_id IS NULL)
 OR (kind='image_charge' AND amount_units<0 AND usage_id IS NULL AND agent_id IS NOT NULL AND image_generation_id IS NOT NULL AND video_generation_id IS NULL)
 OR (kind='video_charge' AND amount_units<0 AND usage_id IS NULL AND agent_id IS NOT NULL AND image_generation_id IS NULL AND video_generation_id IS NOT NULL)
 );

CREATE UNIQUE INDEX platform_ledger_video_charge ON platform_credit_ledger(video_generation_id) WHERE video_generation_id IS NOT NULL;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'preserve video billing and artifact history; use a forward migration'; END $$;
-- +goose StatementEnd
