-- +goose Up
CREATE TABLE platform_timezone_settings (
 singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
 timezone text NOT NULL CHECK (length(timezone) BETWEEN 1 AND 128),
 version bigint NOT NULL DEFAULT 0 CHECK (version >= 0),
 updated_by uuid REFERENCES platform_accounts(id) ON DELETE SET NULL,
 updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO platform_timezone_settings(singleton,timezone) VALUES(true,'Asia/Shanghai');

-- Business schedules adopt the platform default. Rebase from migration time
-- so no old wall-clock occurrence is replayed under a different timezone.
UPDATE manager_triggers SET config=jsonb_set(config,'{timezone}',to_jsonb('Asia/Shanghai'::text)),
 next_due=NULL,updated_at=statement_timestamp() WHERE kind='schedule';

-- +goose Down
DROP TABLE platform_timezone_settings;
