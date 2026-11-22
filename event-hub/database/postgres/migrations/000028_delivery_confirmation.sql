-- +goose Up
ALTER TABLE manager_channel_deliveries DROP CONSTRAINT manager_channel_deliveries_status_check;
ALTER TABLE manager_channel_deliveries ADD CONSTRAINT manager_channel_deliveries_status_check CHECK(status IN ('pending','leased','sending','accepted','sent','failed','unknown','cancelled'));
ALTER TABLE manager_channel_deliveries ADD COLUMN provider text NOT NULL DEFAULT 'feishu' CHECK(provider IN ('feishu','dingtalk','wecom'));
UPDATE manager_channel_deliveries d SET provider=b.provider FROM platform_channel_bindings b WHERE b.id=d.binding_id;
ALTER TABLE manager_channel_deliveries ADD COLUMN provider_request_id text;
ALTER TABLE manager_channel_deliveries ADD COLUMN attempt_target_digest text;
ALTER TABLE manager_channel_deliveries ADD COLUMN authorized_at timestamptz;
ALTER TABLE manager_channel_deliveries ADD COLUMN accepted_at timestamptz;
ALTER TABLE manager_channel_deliveries ADD COLUMN confirmed_at timestamptz;
ALTER TABLE manager_channel_deliveries ADD COLUMN next_query_at timestamptz;
ALTER TABLE manager_channel_deliveries ADD COLUMN query_attempts integer NOT NULL DEFAULT 0;
ALTER TABLE manager_channel_deliveries ADD COLUMN query_token uuid;
ALTER TABLE manager_channel_deliveries ADD COLUMN query_until timestamptz;
CREATE INDEX manager_channel_delivery_queries ON manager_channel_deliveries(next_query_at,id) WHERE status IN ('accepted','unknown') AND provider_request_id IS NOT NULL;
CREATE TABLE manager_channel_delivery_receipts (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 delivery_id uuid NOT NULL REFERENCES manager_channel_deliveries(id),
 attempt_token uuid NOT NULL,
 target_digest text NOT NULL,
 receipt_digest text NOT NULL,
 receipt jsonb NOT NULL,
 authorization_current boolean NOT NULL,
 observed_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(delivery_id,attempt_token,receipt_digest)
);
-- +goose Down
-- This migration adds durable evidence. Do not silently erase it on rollback.
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM manager_channel_delivery_receipts) OR EXISTS(SELECT 1 FROM manager_channel_deliveries WHERE status IN ('accepted','failed')) THEN
  RAISE EXCEPTION 'cannot discard delivery confirmation evidence';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE manager_channel_delivery_receipts;
ALTER TABLE manager_channel_deliveries DROP COLUMN provider,DROP COLUMN provider_request_id,DROP COLUMN attempt_target_digest,DROP COLUMN authorized_at,DROP COLUMN accepted_at,DROP COLUMN confirmed_at,DROP COLUMN next_query_at,DROP COLUMN query_attempts,DROP COLUMN query_token,DROP COLUMN query_until;
ALTER TABLE manager_channel_deliveries DROP CONSTRAINT manager_channel_deliveries_status_check;
ALTER TABLE manager_channel_deliveries ADD CONSTRAINT manager_channel_deliveries_status_check CHECK(status IN ('pending','leased','sending','sent','unknown','cancelled'));
