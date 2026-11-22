-- +goose Up
CREATE TABLE manager_task_artifacts (
 id uuid PRIMARY KEY, turn_id uuid NOT NULL REFERENCES manager_turns(id),
 path text NOT NULL, name text NOT NULL, media_type text NOT NULL, version text NOT NULL,
 size_bytes bigint NOT NULL CHECK(size_bytes BETWEEN 0 AND 33554432),
 created_at timestamptz NOT NULL DEFAULT now(), UNIQUE(turn_id,path,version)
);
CREATE TABLE manager_report_artifacts (
 report_id uuid NOT NULL REFERENCES manager_task_reports(id), artifact_id uuid NOT NULL REFERENCES manager_task_artifacts(id),
 PRIMARY KEY(report_id,artifact_id)
);
CREATE TABLE manager_artifact_offers (
 chat_turn_id uuid NOT NULL REFERENCES manager_turns(id), input_id uuid NOT NULL REFERENCES manager_inputs(id),
 artifact_id uuid NOT NULL REFERENCES manager_task_artifacts(id),PRIMARY KEY(chat_turn_id,input_id,artifact_id)
);
CREATE TABLE manager_artifact_selections (
 chat_turn_id uuid NOT NULL REFERENCES manager_turns(id), artifact_id uuid NOT NULL REFERENCES manager_task_artifacts(id),
 inbox_id uuid NOT NULL REFERENCES manager_channel_inbox(id), report_id uuid REFERENCES manager_task_reports(id),
 position bigint GENERATED ALWAYS AS IDENTITY, PRIMARY KEY(chat_turn_id,artifact_id,inbox_id)
);
ALTER TABLE manager_channel_deliveries ADD COLUMN artifact_id uuid REFERENCES manager_task_artifacts(id);
ALTER TABLE manager_channel_deliveries DROP CONSTRAINT manager_channel_delivery_format_check,
 ADD CONSTRAINT manager_channel_delivery_format_check CHECK
 ((content_format IN ('text','markdown') AND artifact_id IS NULL) OR (content_format IN ('image','file') AND artifact_id IS NOT NULL));
CREATE TABLE manager_channel_media (
 inbox_id uuid NOT NULL REFERENCES manager_channel_inbox(id), ordinal integer NOT NULL CHECK(ordinal BETWEEN 0 AND 31),
 locator jsonb NOT NULL, name text NOT NULL, path text, error_code text, attempts integer NOT NULL DEFAULT 0,
 lease_token uuid, lease_until timestamptz, PRIMARY KEY(inbox_id,ordinal)
);
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT FROM manager_task_artifacts) OR EXISTS(SELECT FROM manager_channel_media) THEN
 RAISE EXCEPTION 'cannot discard channel media and published artifacts'; END IF;
END $$;
-- +goose StatementEnd
DROP TABLE manager_channel_media;
ALTER TABLE manager_channel_deliveries DROP CONSTRAINT manager_channel_delivery_format_check, DROP COLUMN artifact_id,
 ADD CONSTRAINT manager_channel_delivery_format_check CHECK(content_format IN ('text','markdown'));
DROP TABLE manager_artifact_selections,manager_artifact_offers,manager_report_artifacts,manager_task_artifacts;
