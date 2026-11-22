-- +goose Up
ALTER TABLE manager_triggers ADD COLUMN last_verified_at timestamptz;
CREATE TABLE manager_webhook_payloads (
 event_id uuid PRIMARY KEY REFERENCES manager_trigger_events(id) ON DELETE CASCADE,
 source text NOT NULL, event_name text NOT NULL, content_type text NOT NULL,
 body bytea NOT NULL CHECK(octet_length(body)<=262144), as_file boolean NOT NULL
);
CREATE INDEX manager_trigger_event_digest ON manager_trigger_events(trigger_id,body_hash,received_at DESC);
CREATE TABLE manager_webhook_deliveries (
 trigger_id uuid NOT NULL REFERENCES manager_triggers(id),
 delivery_id text NOT NULL, event_id uuid NOT NULL REFERENCES manager_trigger_events(id) ON DELETE CASCADE,
 PRIMARY KEY(trigger_id,delivery_id)
);
-- +goose Down
DROP TABLE manager_webhook_deliveries;
DROP INDEX manager_trigger_event_digest;
DROP TABLE manager_webhook_payloads;
ALTER TABLE manager_triggers DROP COLUMN last_verified_at;
