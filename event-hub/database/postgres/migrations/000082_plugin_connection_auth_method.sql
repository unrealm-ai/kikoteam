-- +goose Up
ALTER TABLE platform_plugin_connections ADD COLUMN auth_method text;
UPDATE platform_plugin_connections c SET auth_method = v.auth_type
FROM platform_plugin_versions v WHERE v.id=c.plugin_version_id AND v.plugin_id=c.plugin_id;
ALTER TABLE platform_plugin_connections ALTER COLUMN auth_method SET NOT NULL,
 ADD CHECK (auth_method IN ('none','bearer','api_key','oauth'));

-- A preview catalog exposed a separate PAT listing. Keep its pinned resource
-- routable for existing employees while offering one GitHub entry to new users.
UPDATE platform_plugins SET listed=false,version=version+1,updated_at=now()
WHERE workspace_id IS NULL AND builtin_key='github_pat' AND listed;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT FROM platform_plugin_connections c JOIN platform_plugin_versions v ON v.id=c.plugin_version_id
            WHERE c.auth_method<>v.auth_type AND c.enabled) THEN
   RAISE EXCEPTION 'Active connections use a different authentication method from their plugin version';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE platform_plugin_connections DROP COLUMN auth_method;
