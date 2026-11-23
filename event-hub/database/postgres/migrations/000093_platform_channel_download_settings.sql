-- +goose Up
CREATE TABLE platform_channel_download_settings (
    singleton boolean PRIMARY KEY CHECK (singleton),
    version bigint NOT NULL CHECK (version > 0),
    validate_public_ip boolean NOT NULL DEFAULT true,
    updated_by uuid REFERENCES platform_accounts(id) ON DELETE SET NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE platform_channel_download_settings;
