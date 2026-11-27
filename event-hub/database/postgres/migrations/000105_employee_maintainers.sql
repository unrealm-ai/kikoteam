-- +goose Up
ALTER TABLE platform_agents ADD COLUMN creator_account_id uuid REFERENCES platform_accounts(id);

-- Before creator identity was stored, the workspace owner is assigned as creator.
-- Fail transactionally if an employee belongs to a workspace without an owner.
UPDATE platform_agents a SET creator_account_id=m.user_id
FROM platform_members m WHERE m.workspace_id=a.workspace_id AND m.role='owner';
ALTER TABLE platform_agents ALTER COLUMN creator_account_id SET NOT NULL;
ALTER TABLE platform_agents ADD COLUMN maintainers_version bigint NOT NULL DEFAULT 1 CHECK (maintainers_version > 0);

CREATE TABLE platform_agent_maintainers (
    workspace_id uuid NOT NULL,
    agent_id uuid NOT NULL,
    user_id uuid NOT NULL,
    granted_by uuid NOT NULL REFERENCES platform_accounts(id),
    granted_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (agent_id, user_id),
    FOREIGN KEY (workspace_id, agent_id) REFERENCES platform_agents(workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id, user_id) REFERENCES platform_members(workspace_id, user_id) ON DELETE CASCADE
);

CREATE TABLE platform_agent_channel_maintainers (
    workspace_id uuid NOT NULL,
    agent_id uuid NOT NULL,
    binding_id uuid NOT NULL REFERENCES platform_channel_bindings(id) ON DELETE CASCADE,
    identity_epoch bigint NOT NULL CHECK (identity_epoch > 0),
    external_id text NOT NULL CHECK (length(external_id) BETWEEN 1 AND 512),
    ref jsonb NOT NULL CHECK (jsonb_typeof(ref) = 'object'),
    display_name text NOT NULL,
    granted_by uuid NOT NULL REFERENCES platform_accounts(id),
    granted_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (agent_id, binding_id, identity_epoch, external_id),
    FOREIGN KEY (workspace_id, agent_id) REFERENCES platform_agents(workspace_id, id) ON DELETE CASCADE
);

CREATE TABLE platform_agent_maintainer_audit (
    workspace_id uuid NOT NULL,
    agent_id uuid NOT NULL,
    version bigint NOT NULL CHECK (version > 1),
    actor_account_id uuid NOT NULL REFERENCES platform_accounts(id),
    added jsonb NOT NULL CHECK (jsonb_typeof(added) = 'array'),
    removed jsonb NOT NULL CHECK (jsonb_typeof(removed) = 'array'),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (agent_id, version),
    FOREIGN KEY (workspace_id, agent_id) REFERENCES platform_agents(workspace_id, id) ON DELETE CASCADE
);

-- +goose StatementBegin
CREATE FUNCTION platform_can_manage_agent(workspace uuid, employee uuid, actor uuid)
RETURNS boolean LANGUAGE sql STABLE AS $$
    SELECT EXISTS (
        SELECT FROM platform_agents a
        JOIN platform_members m ON m.workspace_id=a.workspace_id
        WHERE a.workspace_id=workspace AND a.id=employee AND m.user_id=actor AND m.status='active'
        AND (m.role IN ('owner', 'admin') OR a.creator_account_id=m.user_id OR EXISTS (
            SELECT FROM platform_agent_maintainers g
            WHERE g.workspace_id=a.workspace_id AND g.agent_id=a.id AND g.user_id=m.user_id
        ))
    )
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION platform_can_manage_agent(uuid,uuid,uuid);
DROP TABLE platform_agent_maintainer_audit;
DROP TABLE platform_agent_channel_maintainers;
DROP TABLE platform_agent_maintainers;
ALTER TABLE platform_agents DROP COLUMN maintainers_version, DROP COLUMN creator_account_id;
