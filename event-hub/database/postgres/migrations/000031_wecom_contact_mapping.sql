-- +goose Up
CREATE TABLE platform_wecom_contacts (
 binding_id uuid NOT NULL REFERENCES platform_channel_bindings(id) ON DELETE CASCADE,
 identity_epoch bigint NOT NULL, connection_generation bigint NOT NULL,
 search_user_id text NOT NULL, display_name text NOT NULL, department_paths jsonb NOT NULL DEFAULT '[]',
 expires_at timestamptz NOT NULL, PRIMARY KEY(binding_id,identity_epoch,connection_generation,search_user_id)
);
CREATE TABLE platform_wecom_verifications (
 id uuid PRIMARY KEY, binding_id uuid NOT NULL REFERENCES platform_channel_bindings(id) ON DELETE CASCADE,
 identity_epoch bigint NOT NULL, connection_generation bigint NOT NULL, search_user_id text NOT NULL,
 created_by uuid NOT NULL REFERENCES platform_accounts(id), code_hash text NOT NULL UNIQUE, code_ciphertext bytea NOT NULL,
 status text NOT NULL CHECK(status IN ('pending','verified','cancelled','expired','conflict')),
 callback_user_id text NOT NULL DEFAULT '', created_at timestamptz NOT NULL DEFAULT now(),
 expires_at timestamptz NOT NULL, completed_at timestamptz
);
CREATE INDEX platform_wecom_verifications_scope ON platform_wecom_verifications(binding_id,identity_epoch,connection_generation,search_user_id,created_at DESC);
CREATE TABLE platform_wecom_identity_mappings (
 binding_id uuid NOT NULL REFERENCES platform_channel_bindings(id) ON DELETE CASCADE,
 identity_epoch bigint NOT NULL, connection_generation bigint NOT NULL, search_user_id text NOT NULL,
 callback_user_id text NOT NULL, verification_id uuid NOT NULL REFERENCES platform_wecom_verifications(id),
 verified_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(binding_id,identity_epoch,connection_generation,search_user_id),
 UNIQUE(binding_id,identity_epoch,connection_generation,callback_user_id)
);
CREATE TABLE platform_wecom_control_events (
 binding_id uuid NOT NULL REFERENCES platform_channel_bindings(id) ON DELETE CASCADE,
 identity_epoch bigint NOT NULL, provider_event_id text NOT NULL, provider_message_id text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(binding_id,identity_epoch,provider_event_id),
 UNIQUE(binding_id,identity_epoch,provider_message_id)
);
CREATE TABLE platform_wecom_verification_attempts (
 binding_id uuid NOT NULL REFERENCES platform_channel_bindings(id) ON DELETE CASCADE,
 sender_id text NOT NULL, window_start timestamptz NOT NULL, attempts integer NOT NULL DEFAULT 0,
 PRIMARY KEY(binding_id,sender_id,window_start)
);
-- +goose Down
DROP TABLE platform_wecom_verification_attempts;
DROP TABLE platform_wecom_control_events;
DROP TABLE platform_wecom_identity_mappings;
DROP TABLE platform_wecom_verifications;
DROP TABLE platform_wecom_contacts;
