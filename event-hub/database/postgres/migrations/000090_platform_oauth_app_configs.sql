-- +goose Up
CREATE TABLE platform_oauth_app_configs (
 provider text NOT NULL,
 purpose text NOT NULL,
 version bigint NOT NULL CHECK(version>0),
 enabled boolean NOT NULL,
 client_id text NOT NULL CHECK(length(client_id)<=256),
 client_secret bytea NOT NULL,
 legacy_grant_pin text NOT NULL CHECK(legacy_grant_pin='' OR legacy_grant_pin ~ '^[0-9a-f]{64}$'),
 updated_by uuid REFERENCES platform_accounts(id) ON DELETE SET NULL,
 updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(provider,purpose),
 CHECK(provider='github' AND purpose='plugin'),
 CHECK(NOT enabled OR (client_id<>'' AND octet_length(client_secret)>0))
);

ALTER TABLE platform_plugin_oauth_attempts ADD COLUMN github_app_pin text NOT NULL DEFAULT ''
 CHECK(github_app_pin='' OR github_app_pin ~ '^[0-9a-f]{64}$');

-- +goose Down
ALTER TABLE platform_plugin_oauth_attempts DROP COLUMN github_app_pin;
DROP TABLE platform_oauth_app_configs;
