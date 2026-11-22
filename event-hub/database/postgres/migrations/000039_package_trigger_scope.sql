-- +goose Up
ALTER TABLE platform_capability_packages ADD COLUMN trigger_scope jsonb NOT NULL DEFAULT '{"mode":"all","allow_trigger_ids":[],"deny_trigger_ids":[]}'::jsonb;
-- +goose Down
ALTER TABLE platform_capability_packages DROP COLUMN trigger_scope;
