-- +goose Up
-- Presentation metadata never replaces authenticated sender or mention IDs.
ALTER TABLE manager_channel_inbox ADD COLUMN message_metadata jsonb NOT NULL DEFAULT '{}';
ALTER TABLE manager_channel_inbox ADD CONSTRAINT manager_channel_metadata_object CHECK (jsonb_typeof(message_metadata)='object');
CREATE TABLE manager_channel_speaker_aliases (
 conversation_id uuid NOT NULL REFERENCES manager_conversations(id),
 identity_key text NOT NULL,
 ordinal integer NOT NULL CHECK (ordinal>0),
 PRIMARY KEY (conversation_id,identity_key),
 UNIQUE (conversation_id,ordinal)
);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM manager_channel_speaker_aliases) OR EXISTS(SELECT 1 FROM manager_channel_inbox WHERE message_metadata<>'{}'::jsonb) THEN
  RAISE EXCEPTION 'cannot discard persisted channel presentation metadata';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE manager_channel_speaker_aliases;
ALTER TABLE manager_channel_inbox DROP COLUMN message_metadata;
