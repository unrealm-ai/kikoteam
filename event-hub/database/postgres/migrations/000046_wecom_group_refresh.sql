-- +goose Up
CREATE TABLE platform_wecom_group_refresh_jobs(
 id uuid PRIMARY KEY,binding_id uuid NOT NULL REFERENCES platform_channel_bindings(id),requested_by uuid NOT NULL REFERENCES platform_accounts(id),
 connection_generation bigint NOT NULL,config_revision bigint NOT NULL,identity_epoch bigint NOT NULL,external_tenant_id text NOT NULL,
 status text NOT NULL DEFAULT 'pending' CHECK(status IN('pending','leased','completed','failed')),
 error_code text,lease_token uuid,lease_until timestamptz,created_at timestamptz NOT NULL DEFAULT now(),deadline timestamptz NOT NULL DEFAULT now()+interval '3 minutes'
);
CREATE INDEX platform_wecom_group_refresh_pending ON platform_wecom_group_refresh_jobs(status,created_at);
-- +goose Down
DROP TABLE platform_wecom_group_refresh_jobs;
