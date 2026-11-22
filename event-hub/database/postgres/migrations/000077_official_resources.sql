-- +goose Up
-- A null owner denotes a platform resource; workspace and employee identities remain mandatory on grants.
ALTER TABLE platform_skills ALTER COLUMN workspace_id DROP NOT NULL,
 ADD COLUMN listed boolean NOT NULL DEFAULT true, ADD COLUMN archived_at timestamptz;
ALTER TABLE platform_skill_versions ALTER COLUMN workspace_id DROP NOT NULL,
 ADD UNIQUE (skill_id,id), ADD UNIQUE (skill_id,sequence),
 ADD FOREIGN KEY (skill_id) REFERENCES platform_skills(id);
ALTER TABLE platform_skills ADD FOREIGN KEY (id,current_version_id) REFERENCES platform_skill_versions(skill_id,id) DEFERRABLE INITIALLY DEFERRED;
CREATE INDEX platform_capability_package_skills_usage ON platform_capability_package_skills(workspace_id,skill_id,package_id);
ALTER TABLE platform_plugins ALTER COLUMN workspace_id DROP NOT NULL,
 ADD COLUMN listed boolean NOT NULL DEFAULT true, ADD COLUMN archived_at timestamptz;
ALTER TABLE platform_plugin_versions ALTER COLUMN workspace_id DROP NOT NULL,
 ADD UNIQUE (plugin_id,id), ADD UNIQUE (plugin_id,version),
 ADD FOREIGN KEY (plugin_id) REFERENCES platform_plugins(id);
ALTER TABLE platform_plugins ADD FOREIGN KEY (id,current_version_id) REFERENCES platform_plugin_versions(plugin_id,id) DEFERRABLE INITIALLY DEFERRED;
CREATE INDEX platform_capability_package_plugins_usage ON platform_capability_package_plugins(workspace_id,plugin_id,package_id);
ALTER TABLE platform_capability_package_skills DROP CONSTRAINT platform_capability_package_skills_workspace_id_skill_id_fkey;
ALTER TABLE platform_capability_package_plugins DROP CONSTRAINT platform_capability_package_plugins_workspace_id_plugin_id_fkey;
ALTER TABLE platform_agent_template_version_resources ALTER COLUMN source_workspace_id DROP NOT NULL;
ALTER TABLE platform_capability_package_skills DROP CONSTRAINT platform_capability_package_s_workspace_id_skill_id_versio_fkey,
 ADD CONSTRAINT platform_capability_package_s_workspace_id_skill_id_versio_fkey FOREIGN KEY (skill_id,version_id) REFERENCES platform_skill_versions(skill_id,id);
ALTER TABLE platform_capability_package_plugins DROP CONSTRAINT platform_capability_package_p_workspace_id_plugin_id_versi_fkey,
 ADD CONSTRAINT platform_capability_package_p_workspace_id_plugin_id_versi_fkey FOREIGN KEY (plugin_id,version_id) REFERENCES platform_plugin_versions(plugin_id,id);
ALTER TABLE platform_employee_plugin_accounts DROP CONSTRAINT platform_employee_plugin_acco_workspace_id_plugin_id_plugi_fkey,
 ADD CONSTRAINT platform_employee_plugin_acco_workspace_id_plugin_id_plugi_fkey FOREIGN KEY (plugin_id,plugin_version_id) REFERENCES platform_plugin_versions(plugin_id,id);
ALTER TABLE platform_agent_template_version_resources DROP CONSTRAINT platform_agent_template_versi_source_workspace_id_skill_id_fkey,
 ADD CONSTRAINT platform_agent_template_versi_source_workspace_id_skill_id_fkey FOREIGN KEY (skill_id,skill_version_id) REFERENCES platform_skill_versions(skill_id,id);
ALTER TABLE platform_agent_template_version_resources DROP CONSTRAINT platform_agent_template_versi_source_workspace_id_plugin_i_fkey,
 ADD CONSTRAINT platform_agent_template_versi_source_workspace_id_plugin_i_fkey FOREIGN KEY (plugin_id,plugin_version_id) REFERENCES platform_plugin_versions(plugin_id,id);
-- +goose StatementBegin
CREATE FUNCTION platform_resource_owner_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.workspace_id IS DISTINCT FROM OLD.workspace_id THEN
  RAISE EXCEPTION 'resource owner is immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER immutable_owner BEFORE UPDATE OF workspace_id ON platform_skills
 FOR EACH ROW EXECUTE FUNCTION platform_resource_owner_immutable();
-- +goose StatementBegin
CREATE FUNCTION platform_check_skill_owner() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE owner_id uuid;
BEGIN
 IF NEW.skill_id IS NULL THEN RETURN NEW; END IF;
 SELECT workspace_id INTO STRICT owner_id FROM platform_skills WHERE id=NEW.skill_id;
 IF TG_TABLE_NAME='platform_agent_template_version_resources' THEN
  IF owner_id IS NOT DISTINCT FROM NEW.source_workspace_id THEN RETURN NEW; END IF;
 ELSIF owner_id IS NOT DISTINCT FROM NEW.workspace_id OR
       (owner_id IS NULL AND TG_TABLE_NAME<>'platform_skill_versions') THEN
  RETURN NEW;
 END IF;
 RAISE EXCEPTION 'skill scope mismatch' USING ERRCODE='23514';
END $$;
-- +goose StatementEnd
CREATE TRIGGER check_skill_owner BEFORE INSERT OR UPDATE ON platform_skill_versions
 FOR EACH ROW EXECUTE FUNCTION platform_check_skill_owner();
CREATE TRIGGER check_skill_owner BEFORE INSERT OR UPDATE ON platform_capability_package_skills
 FOR EACH ROW EXECUTE FUNCTION platform_check_skill_owner();
CREATE TRIGGER check_skill_owner BEFORE INSERT OR UPDATE ON platform_agent_template_version_resources
 FOR EACH ROW EXECUTE FUNCTION platform_check_skill_owner();
CREATE TRIGGER immutable_owner BEFORE UPDATE OF workspace_id ON platform_plugins
 FOR EACH ROW EXECUTE FUNCTION platform_resource_owner_immutable();
-- +goose StatementBegin
CREATE FUNCTION platform_check_plugin_owner() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE owner_id uuid;
BEGIN
 IF NEW.plugin_id IS NULL THEN RETURN NEW; END IF;
 SELECT workspace_id INTO STRICT owner_id FROM platform_plugins WHERE id=NEW.plugin_id;
 IF TG_TABLE_NAME='platform_agent_template_version_resources' THEN
  IF owner_id IS NOT DISTINCT FROM NEW.source_workspace_id THEN RETURN NEW; END IF;
 ELSIF owner_id IS NOT DISTINCT FROM NEW.workspace_id OR
       (owner_id IS NULL AND TG_TABLE_NAME<>'platform_plugin_versions') THEN
  RETURN NEW;
 END IF;
 RAISE EXCEPTION 'plugin scope mismatch' USING ERRCODE='23514';
END $$;
-- +goose StatementEnd
CREATE TRIGGER check_plugin_owner BEFORE INSERT OR UPDATE ON platform_plugin_versions
 FOR EACH ROW EXECUTE FUNCTION platform_check_plugin_owner();
CREATE TRIGGER check_plugin_owner BEFORE INSERT OR UPDATE ON platform_capability_package_plugins
 FOR EACH ROW EXECUTE FUNCTION platform_check_plugin_owner();
CREATE TRIGGER check_plugin_owner BEFORE INSERT OR UPDATE ON platform_employee_plugin_accounts
 FOR EACH ROW EXECUTE FUNCTION platform_check_plugin_owner();
CREATE TRIGGER check_plugin_owner BEFORE INSERT OR UPDATE ON platform_agent_template_version_resources
 FOR EACH ROW EXECUTE FUNCTION platform_check_plugin_owner();

-- +goose Down
DROP TRIGGER check_skill_owner ON platform_skill_versions;
DROP TRIGGER check_skill_owner ON platform_capability_package_skills;
DROP TRIGGER check_skill_owner ON platform_agent_template_version_resources;
DROP FUNCTION platform_check_skill_owner();
DROP TRIGGER immutable_owner ON platform_skills;
DROP TRIGGER check_plugin_owner ON platform_plugin_versions;
DROP TRIGGER check_plugin_owner ON platform_capability_package_plugins;
DROP TRIGGER check_plugin_owner ON platform_employee_plugin_accounts;
DROP TRIGGER check_plugin_owner ON platform_agent_template_version_resources;
DROP FUNCTION platform_check_plugin_owner();
DROP TRIGGER immutable_owner ON platform_plugins;
DROP FUNCTION platform_resource_owner_immutable();
ALTER TABLE platform_capability_package_skills DROP CONSTRAINT platform_capability_package_s_workspace_id_skill_id_versio_fkey,
 ADD CONSTRAINT platform_capability_package_s_workspace_id_skill_id_versio_fkey FOREIGN KEY (workspace_id,skill_id,version_id) REFERENCES platform_skill_versions(workspace_id,skill_id,id);
ALTER TABLE platform_capability_package_plugins DROP CONSTRAINT platform_capability_package_p_workspace_id_plugin_id_versi_fkey,
 ADD CONSTRAINT platform_capability_package_p_workspace_id_plugin_id_versi_fkey FOREIGN KEY (workspace_id,plugin_id,version_id) REFERENCES platform_plugin_versions(workspace_id,plugin_id,id);
ALTER TABLE platform_employee_plugin_accounts DROP CONSTRAINT platform_employee_plugin_acco_workspace_id_plugin_id_plugi_fkey,
 ADD CONSTRAINT platform_employee_plugin_acco_workspace_id_plugin_id_plugi_fkey FOREIGN KEY (workspace_id,plugin_id,plugin_version_id) REFERENCES platform_plugin_versions(workspace_id,plugin_id,id);
ALTER TABLE platform_agent_template_version_resources DROP CONSTRAINT platform_agent_template_versi_source_workspace_id_skill_id_fkey,
 ADD CONSTRAINT platform_agent_template_versi_source_workspace_id_skill_id_fkey FOREIGN KEY (source_workspace_id,skill_id,skill_version_id) REFERENCES platform_skill_versions(workspace_id,skill_id,id);
ALTER TABLE platform_agent_template_version_resources DROP CONSTRAINT platform_agent_template_versi_source_workspace_id_plugin_i_fkey,
 ADD CONSTRAINT platform_agent_template_versi_source_workspace_id_plugin_i_fkey FOREIGN KEY (source_workspace_id,plugin_id,plugin_version_id) REFERENCES platform_plugin_versions(workspace_id,plugin_id,id);
ALTER TABLE platform_capability_package_skills ADD FOREIGN KEY(workspace_id,skill_id) REFERENCES platform_skills(workspace_id,id);
ALTER TABLE platform_capability_package_plugins ADD FOREIGN KEY(workspace_id,plugin_id) REFERENCES platform_plugins(workspace_id,id);
ALTER TABLE platform_agent_template_version_resources ALTER COLUMN source_workspace_id SET NOT NULL;
DROP INDEX platform_capability_package_skills_usage;
ALTER TABLE platform_skills DROP CONSTRAINT platform_skills_id_current_version_id_fkey,
 ALTER COLUMN workspace_id SET NOT NULL, DROP COLUMN listed, DROP COLUMN archived_at;
ALTER TABLE platform_skill_versions ALTER COLUMN workspace_id SET NOT NULL,
 DROP CONSTRAINT platform_skill_versions_skill_id_fkey, DROP CONSTRAINT platform_skill_versions_skill_id_id_key,
 DROP CONSTRAINT platform_skill_versions_skill_id_sequence_key;
DROP INDEX platform_capability_package_plugins_usage;
ALTER TABLE platform_plugins DROP CONSTRAINT platform_plugins_id_current_version_id_fkey,
 ALTER COLUMN workspace_id SET NOT NULL, DROP COLUMN listed, DROP COLUMN archived_at;
ALTER TABLE platform_plugin_versions ALTER COLUMN workspace_id SET NOT NULL,
 DROP CONSTRAINT platform_plugin_versions_plugin_id_fkey, DROP CONSTRAINT platform_plugin_versions_plugin_id_id_key,
 DROP CONSTRAINT platform_plugin_versions_plugin_id_version_key;
