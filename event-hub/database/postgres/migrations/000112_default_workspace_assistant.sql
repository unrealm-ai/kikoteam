-- +goose Up
ALTER TABLE platform_agents ADD COLUMN follows_default_model boolean NOT NULL DEFAULT false,
    DROP CONSTRAINT platform_agents_check1,
    ADD CONSTRAINT platform_agents_model_selection_check CHECK (
        num_nonnulls(model_profile_id, platform_model_id) = CASE WHEN follows_default_model THEN 0 ELSE 1 END
    );
ALTER TABLE platform_agent_versions ADD COLUMN follows_default_model boolean NOT NULL DEFAULT false,
    DROP CONSTRAINT platform_agent_versions_check,
    ADD CONSTRAINT platform_agent_versions_model_selection_check CHECK (
        num_nonnulls(model_profile_id, platform_model_id) = CASE WHEN follows_default_model THEN 0 ELSE 1 END
    );

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION platform_queue_new_assistant() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO platform_assistant_provisioning(workspace_id) VALUES(NEW.id);
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- Do not apply the new automatic policy to workspaces created before this migration.
UPDATE platform_assistant_provisioning SET status='skipped' WHERE status='pending';

-- +goose StatementBegin
CREATE FUNCTION platform_assistant_default_model(w uuid)
RETURNS TABLE(model_profile_id uuid, platform_model_id uuid) LANGUAGE sql STABLE AS $$
    SELECT candidate.local_id, candidate.official_id
    FROM (
        SELECT id AS local_id, NULL::uuid AS official_id, 0 AS priority
        FROM platform_model_profiles
        WHERE workspace_id=w AND is_default AND enabled AND validated
        UNION ALL
        SELECT NULL::uuid, id, 1
        FROM platform_proxy_models WHERE is_default AND enabled AND validated
    ) candidate
    ORDER BY candidate.priority, COALESCE(candidate.local_id,candidate.official_id) LIMIT 1
$$;
-- +goose StatementEnd

-- +goose Down
-- Refuse rollback after creating unbound assistants rather than silently losing them.
ALTER TABLE platform_agents DROP CONSTRAINT platform_agents_model_selection_check,
    ADD CONSTRAINT platform_agents_check1 CHECK (num_nonnulls(model_profile_id,platform_model_id)=1),
    DROP COLUMN follows_default_model;
ALTER TABLE platform_agent_versions DROP CONSTRAINT platform_agent_versions_model_selection_check,
    ADD CONSTRAINT platform_agent_versions_check CHECK (num_nonnulls(model_profile_id,platform_model_id)=1),
    DROP COLUMN follows_default_model;
DROP FUNCTION platform_assistant_default_model(uuid);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION platform_queue_new_assistant() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF (SELECT enabled FROM platform_assistant_settings WHERE singleton) THEN
        INSERT INTO platform_assistant_provisioning(workspace_id) VALUES(NEW.id);
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
