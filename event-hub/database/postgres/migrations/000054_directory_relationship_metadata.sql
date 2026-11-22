-- +goose Up
-- Additive descriptive facts. Stable selection IDs and authorization are unchanged.
ALTER TABLE platform_channel_directory_users
 ADD COLUMN direct_manager_id text,
 ADD COLUMN direct_manager_known boolean NOT NULL DEFAULT false,
 ADD COLUMN aliases jsonb NOT NULL DEFAULT '[]' CHECK(jsonb_typeof(aliases)='array'),
 ADD CONSTRAINT directory_manager_known CHECK(direct_manager_known OR direct_manager_id IS NULL);
ALTER TABLE platform_channel_directory_departments
 ADD COLUMN leader_ids jsonb NOT NULL DEFAULT '[]' CHECK(jsonb_typeof(leader_ids)='array'),
 ADD COLUMN leaders_known boolean NOT NULL DEFAULT false;
-- +goose Down
ALTER TABLE platform_channel_directory_users DROP CONSTRAINT directory_manager_known,
 DROP COLUMN aliases,DROP COLUMN direct_manager_known,DROP COLUMN direct_manager_id;
ALTER TABLE platform_channel_directory_departments DROP COLUMN leaders_known,DROP COLUMN leader_ids;
