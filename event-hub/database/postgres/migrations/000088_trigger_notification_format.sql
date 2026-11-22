-- +goose Up
ALTER TABLE manager_trigger_notifications ADD COLUMN content_format text NOT NULL DEFAULT 'text'
 CHECK (content_format IN ('text', 'markdown'));

-- +goose Down
ALTER TABLE manager_trigger_notifications DROP COLUMN content_format;
