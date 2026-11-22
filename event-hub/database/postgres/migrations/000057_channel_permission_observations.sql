-- +goose Up
CREATE TABLE platform_feishu_permission_checks (
 binding_id uuid PRIMARY KEY REFERENCES platform_channel_bindings(id) ON DELETE CASCADE,
 connection_generation bigint NOT NULL,
 diagnostics jsonb,
 next_check_at timestamptz NOT NULL DEFAULT now(),
 lease_token uuid,
 lease_until timestamptz
);
CREATE INDEX platform_feishu_permission_checks_due ON platform_feishu_permission_checks(next_check_at,binding_id);
INSERT INTO platform_feishu_permission_checks(binding_id,connection_generation)
SELECT id,connection_generation FROM platform_channel_bindings WHERE provider='feishu';

-- Older QR bindings could bypass the employee usage projection. Reconcile
-- existing bindings with the policy already displayed on the employee page.
WITH projected AS (
 SELECT b.id,platform_agent_usage_projection(a.usage_config,b.provider,b.id,b.identity_epoch) AS value
 FROM platform_channel_bindings b JOIN platform_agents a ON a.workspace_id=b.workspace_id AND a.id=b.agent_id
 WHERE b.provider IN ('feishu','dingtalk')
), changed AS (
 UPDATE platform_channel_bindings b SET access=p.value->'access',
 config_revision=b.config_revision+1,applied_status='pending',applied_error=''
 FROM projected p WHERE b.id=p.id AND b.access IS DISTINCT FROM p.value->'access'
 RETURNING b.id
)
UPDATE platform_channel_config_clock SET revision=revision+1 WHERE id=true AND EXISTS (SELECT 1 FROM changed);

-- +goose Down
DROP TABLE platform_feishu_permission_checks;
