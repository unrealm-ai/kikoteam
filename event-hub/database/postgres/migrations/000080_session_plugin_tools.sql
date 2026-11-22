-- +goose Up
CREATE TABLE manager_session_plugin_tools (
    session_id uuid NOT NULL REFERENCES manager_sessions(id) ON DELETE CASCADE,
    plugin_version_id uuid NOT NULL REFERENCES platform_plugin_versions(id),
    tools jsonb NOT NULL CHECK (jsonb_typeof(tools) = 'array'),
    PRIMARY KEY (session_id, plugin_version_id)
);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT FROM manager_session_plugin_tools) THEN
        RAISE EXCEPTION 'cannot discard active session tool snapshots';
    END IF;
END $$;
-- +goose StatementEnd
DROP TABLE manager_session_plugin_tools;
