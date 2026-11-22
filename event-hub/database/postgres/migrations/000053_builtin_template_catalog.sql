-- +goose Up
ALTER TABLE platform_agent_templates ADD COLUMN builtin_key text UNIQUE,
 ALTER COLUMN created_by DROP NOT NULL,
 ADD CHECK ((builtin_key IS NULL) = (created_by IS NOT NULL)),
 ADD CHECK (builtin_key IS NULL OR scope='official');
ALTER TABLE platform_agent_template_versions ADD COLUMN catalog_revision bigint CHECK (catalog_revision > 0),
 ALTER COLUMN created_by DROP NOT NULL,
 DROP CONSTRAINT platform_agent_template_versions_check,
 ADD CHECK ((catalog_revision IS NULL) = (created_by IS NOT NULL)),
 ADD CHECK (CASE WHEN catalog_revision IS NULL THEN (published_by IS NULL)=(published_at IS NULL)
                 ELSE published_by IS NULL AND published_at IS NOT NULL END);
CREATE TABLE platform_template_catalog_state (
 singleton boolean PRIMARY KEY CHECK (singleton), revision bigint NOT NULL, digest text NOT NULL);
INSERT INTO platform_template_catalog_state VALUES(true,0,'');

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'preserve system-published template history; forward migration required'; END $$;
-- +goose StatementEnd
