-- +goose Up
-- Runtime authorization evidence only. Tool arguments/results and credentials
-- remain outside this audit table; an authorized row without completion has an
-- unknown remote outcome and must never trigger automatic replay.
CREATE TABLE manager_capability_calls (
 id uuid PRIMARY KEY,
 workspace_id uuid NOT NULL,
 session_id uuid NOT NULL,
 turn_id uuid NOT NULL,
 native_tool_call_id text NOT NULL CHECK(length(native_tool_call_id) BETWEEN 1 AND 256),
 plugin_id uuid NOT NULL,
 plugin_version_id uuid,
 account_id uuid,
 account_version bigint,
 tool_name text NOT NULL CHECK(length(tool_name) BETWEEN 1 AND 200),
 schema_digest text,
 status text NOT NULL CHECK(status IN ('denied','authorized','succeeded','failed')),
 error_code text,
 created_at timestamptz NOT NULL DEFAULT now(),
 finished_at timestamptz,
 UNIQUE(turn_id,native_tool_call_id),
 FOREIGN KEY(workspace_id,session_id,turn_id) REFERENCES manager_turns(workspace_id,session_id,id),
 CHECK((status='authorized')=(finished_at IS NULL))
);
CREATE INDEX manager_capability_calls_session ON manager_capability_calls(workspace_id,session_id,created_at,id);
CREATE INDEX manager_capability_calls_unfinished ON manager_capability_calls(session_id) WHERE status='authorized';

-- +goose Down
DROP TABLE manager_capability_calls;
