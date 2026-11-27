-- +goose Up
CREATE TABLE platform_assistant_settings (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    enabled boolean NOT NULL DEFAULT false,
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    updated_by uuid REFERENCES platform_accounts(id),
    updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO platform_assistant_settings(singleton) VALUES(true);
ALTER TABLE platform_workspaces ADD COLUMN assistant_agent_id uuid;
ALTER TABLE platform_workspaces ADD FOREIGN KEY (id,assistant_agent_id)
    REFERENCES platform_agents(workspace_id,id) ON DELETE SET NULL (assistant_agent_id);
ALTER TABLE platform_capability_packages ADD COLUMN assistant_tool_set text
    CHECK (assistant_tool_set IS NULL OR (assistant_tool_set IN ('platform','demo') AND agent_id IS NOT NULL));
CREATE UNIQUE INDEX platform_assistant_package_unique ON platform_capability_packages(agent_id,assistant_tool_set)
    WHERE assistant_tool_set IS NOT NULL;
CREATE TABLE platform_assistant_provisioning (
    workspace_id uuid PRIMARY KEY REFERENCES platform_workspaces(id) ON DELETE CASCADE,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','provisioned','skipped','failed')),
    attempts int NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX platform_assistant_provisioning_due ON platform_assistant_provisioning(next_attempt_at,workspace_id) WHERE status='pending';

-- +goose StatementBegin
CREATE FUNCTION platform_queue_new_assistant() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF (SELECT enabled FROM platform_assistant_settings WHERE singleton) THEN
        INSERT INTO platform_assistant_provisioning(workspace_id) VALUES(NEW.id);
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER platform_new_workspace_assistant AFTER INSERT ON platform_workspaces
    FOR EACH ROW EXECUTE FUNCTION platform_queue_new_assistant();

-- +goose Down
DROP TRIGGER platform_new_workspace_assistant ON platform_workspaces;
DROP FUNCTION platform_queue_new_assistant();
DROP TABLE platform_assistant_provisioning;
ALTER TABLE platform_workspaces DROP COLUMN assistant_agent_id;
ALTER TABLE platform_capability_packages DROP COLUMN assistant_tool_set;
DROP TABLE platform_assistant_settings;
