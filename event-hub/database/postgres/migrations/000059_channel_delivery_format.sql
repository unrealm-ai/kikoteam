-- +goose Up
ALTER TABLE manager_channel_deliveries
 ADD COLUMN content_format text NOT NULL DEFAULT 'text'
 CONSTRAINT manager_channel_delivery_format_check CHECK (content_format IN ('text','markdown'));

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM manager_channel_deliveries WHERE content_format='markdown') THEN
  RAISE EXCEPTION 'cannot discard stored markdown delivery formats';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE manager_channel_deliveries DROP COLUMN content_format;
