-- +goose Up
-- Employee-level policy source; intentionally do not infer grants from old
-- channel-specific settings or erase existing local records.
ALTER TABLE platform_agents ADD COLUMN usage_config jsonb NOT NULL DEFAULT '{"access":{"mode":"allowlist","group_members_enabled":true,"allow":[],"deny":[]},"trigger":{"mention":true,"keywords":[],"regexes":[]},"history":{"enabled":false,"limit":20,"channels":[]}}'::jsonb CHECK(jsonb_typeof(usage_config)='object');
ALTER TABLE platform_agents ADD COLUMN usage_version bigint NOT NULL DEFAULT 1 CHECK(usage_version>0);
-- +goose Down
ALTER TABLE platform_agents DROP COLUMN usage_version,DROP COLUMN usage_config;
