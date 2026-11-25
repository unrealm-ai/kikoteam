-- +goose Up
-- NULL owner denotes an automatic job that may only enrich already observed groups.
ALTER TABLE platform_wecom_group_refresh_jobs ALTER COLUMN requested_by DROP NOT NULL;
CREATE UNIQUE INDEX platform_wecom_group_refresh_auto
 ON platform_wecom_group_refresh_jobs(binding_id) WHERE requested_by IS NULL;
CREATE INDEX platform_wecom_observed_unnamed
 ON platform_channel_observed_chats(binding_id,observed_at) WHERE btrim(display_name)='';

-- +goose Down
DROP INDEX platform_wecom_observed_unnamed;
DROP INDEX platform_wecom_group_refresh_auto;
DELETE FROM platform_wecom_group_refresh_jobs WHERE requested_by IS NULL;
ALTER TABLE platform_wecom_group_refresh_jobs ALTER COLUMN requested_by SET NOT NULL;
