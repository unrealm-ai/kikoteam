-- +goose Up
CREATE TABLE platform_task_link_settings (
    singleton boolean PRIMARY KEY CHECK (singleton),
    version bigint NOT NULL CHECK (version > 0),
    origin text NOT NULL DEFAULT '' CHECK (length(origin) <= 270),
    enabled boolean NOT NULL DEFAULT true,
    updated_by uuid REFERENCES platform_accounts(id) ON DELETE SET NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE manager_task_detail_grants (
    id uuid PRIMARY KEY,
    token_digest bytea NOT NULL UNIQUE CHECK (octet_length(token_digest) = 32),
    workspace_id uuid NOT NULL REFERENCES platform_workspaces(id) ON DELETE CASCADE,
    session_id uuid NOT NULL,
    task_id uuid NOT NULL,
    source_input_id uuid REFERENCES manager_inputs(id) ON DELETE CASCADE,
    source_chat_session_id uuid REFERENCES manager_sessions(id) ON DELETE CASCADE,
    trigger_notification_id uuid REFERENCES manager_trigger_notifications(id) ON DELETE CASCADE,
    destination_conversation_id uuid NOT NULL REFERENCES manager_conversations(id) ON DELETE CASCADE,
    destination_channel text NOT NULL CHECK (destination_channel IN ('feishu','dingtalk','wecom','web')),
    destination_binding_id uuid REFERENCES platform_channel_bindings(id) ON DELETE CASCADE,
    destination_chat_type text NOT NULL CHECK (destination_chat_type IN ('private','group')),
    destination_chat_id text NOT NULL,
    destination_topic_id text NOT NULL,
    destination_generation bigint NOT NULL,
    scope text NOT NULL CHECK (scope IN ('read','write')),
    issued_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    FOREIGN KEY (workspace_id, session_id) REFERENCES manager_sessions(workspace_id,id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id, task_id) REFERENCES manager_tasks(workspace_id,id) ON DELETE CASCADE,
    CHECK (expires_at > issued_at),
    CHECK ((source_input_id IS NULL) = (source_chat_session_id IS NULL)),
    CHECK ((trigger_notification_id IS NULL) = (source_input_id IS NOT NULL)),
    CHECK (scope <> 'write' OR (destination_chat_type = 'private' AND source_input_id IS NOT NULL)),
    CHECK ((destination_channel = 'web' AND destination_binding_id IS NULL AND destination_generation = 0)
           OR (destination_channel <> 'web' AND destination_binding_id IS NOT NULL AND destination_generation > 0 AND destination_chat_id <> ''))
);
CREATE INDEX manager_task_detail_grants_session_idx ON manager_task_detail_grants(session_id, expires_at);
CREATE INDEX manager_task_detail_grants_expiry_idx ON manager_task_detail_grants(expires_at) WHERE revoked_at IS NULL;

CREATE TABLE manager_task_detail_operations (
    grant_id uuid NOT NULL REFERENCES manager_task_detail_grants(id) ON DELETE CASCADE,
    action text NOT NULL,
    request_key text NOT NULL,
    input_id uuid REFERENCES manager_inputs(id) ON DELETE SET NULL,
    outcome text NOT NULL DEFAULT 'accepted',
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (grant_id, action, request_key)
);

-- +goose Down
DROP TABLE manager_task_detail_operations;
DROP TABLE manager_task_detail_grants;
DROP TABLE platform_task_link_settings;
