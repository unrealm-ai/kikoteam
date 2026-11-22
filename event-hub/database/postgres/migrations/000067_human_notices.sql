-- +goose Up
-- Preserve existing pending requests for manual lookup, but do not silently
-- send a historical notification backlog on the first rollout.
ALTER TABLE manager_human_requests ADD COLUMN proactive_notice_enabled boolean NOT NULL DEFAULT false;
ALTER TABLE manager_human_requests ALTER COLUMN proactive_notice_enabled SET DEFAULT true;

CREATE TABLE manager_human_notices (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 request_id uuid NOT NULL REFERENCES manager_human_requests(id),
 request_version bigint NOT NULL,
 recipient jsonb NOT NULL,
 workspace_id uuid NOT NULL REFERENCES platform_workspaces(id),
 agent_id uuid NOT NULL REFERENCES platform_agents(id),
 binding_id uuid NOT NULL REFERENCES platform_channel_bindings(id),
 provider text NOT NULL CHECK (provider IN ('feishu','dingtalk','wecom')),
 tenant_id text NOT NULL,
 connection_generation bigint NOT NULL,
 config_revision bigint NOT NULL,
 identity_epoch bigint NOT NULL,
 status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','leased','sending','accepted','sent','failed','unknown','cancelled')),
 lease_token uuid, lease_until timestamptz, attempt_id uuid,
 attempt_target_digest text, authorized_at timestamptz, query_token uuid, query_until timestamptz, query_attempts integer NOT NULL DEFAULT 0, next_query_at timestamptz,
 provider_request_id text, provider_message_id text, provider_chat_id text,
 text text, evidence_type text, evidence_id text, error_code text, receipt jsonb,
 created_at timestamptz NOT NULL DEFAULT now(), sent_at timestamptz,
 UNIQUE (request_id,request_version,recipient)
);
CREATE INDEX manager_human_notices_pending ON manager_human_notices(created_at,id) WHERE status IN ('pending','leased','sending','accepted');
CREATE INDEX manager_human_notices_quote ON manager_human_notices(binding_id,provider_chat_id,provider_message_id)
 WHERE status='sent' AND provider_chat_id IS NOT NULL AND provider_message_id IS NOT NULL;

-- +goose Down
DROP TABLE manager_human_notices;
ALTER TABLE manager_human_requests DROP COLUMN proactive_notice_enabled;
