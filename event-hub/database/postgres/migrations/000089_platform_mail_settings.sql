-- +goose Up
CREATE TABLE platform_mail_settings (
    singleton boolean PRIMARY KEY CHECK (singleton),
    version bigint NOT NULL CHECK (version > 0),
    enabled boolean NOT NULL,
    verification_required boolean NOT NULL CHECK (NOT verification_required OR enabled),
    address text NOT NULL,
    host text NOT NULL,
    username text NOT NULL,
    sender text NOT NULL,
    implicit_tls boolean NOT NULL,
    password_ciphertext bytea,
    updated_by uuid NOT NULL REFERENCES platform_accounts(id),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE platform_mail_settings;
