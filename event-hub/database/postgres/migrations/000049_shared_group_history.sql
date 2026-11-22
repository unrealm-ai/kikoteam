-- +goose Up
CREATE TABLE manager_group_history_queries(
 id uuid PRIMARY KEY,workspace_id uuid NOT NULL REFERENCES platform_workspaces(id),agent_id uuid NOT NULL REFERENCES platform_agents(id),
 session_id uuid NOT NULL REFERENCES manager_sessions(id),input_id uuid NOT NULL REFERENCES manager_inputs(id),
 policy_id uuid NOT NULL,policy_version bigint NOT NULL,edge_ordinal integer NOT NULL,
 source_ref jsonb NOT NULL,target_ref jsonb NOT NULL,start_ms bigint NOT NULL,end_ms bigint NOT NULL,page_limit integer NOT NULL CHECK(page_limit BETWEEN 1 AND 50),
 page_number integer NOT NULL DEFAULT 1 CHECK(page_number BETWEEN 1 AND 10),bytes_before integer NOT NULL DEFAULT 0,
 pending_items jsonb NOT NULL DEFAULT '[]',page_bytes integer NOT NULL DEFAULT 0,provider_cursor text NOT NULL DEFAULT '',parent_id uuid UNIQUE REFERENCES manager_group_history_queries(id),next_query_id uuid REFERENCES manager_group_history_queries(id),
 status text NOT NULL DEFAULT 'pending' CHECK(status IN('pending','leased','completed','failed')),error_code text,
 lease_token uuid,lease_until timestamptz,snapshot jsonb,next_provider_cursor text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(),expires_at timestamptz NOT NULL DEFAULT now()+interval '10 minutes',
 CHECK(start_ms>=0 AND end_ms>start_ms AND (source_ref->>'provider'<>'wecom' OR end_ms-start_ms<=604800000))
);
CREATE INDEX manager_group_history_pending ON manager_group_history_queries(status,created_at);
-- Fail closed until runtime/lineage authorization migration is installed.
-- +goose StatementBegin
CREATE FUNCTION manager_shared_history_allowed(query_id uuid) RETURNS boolean LANGUAGE sql STABLE AS $$ SELECT false $$;
CREATE FUNCTION manager_shared_history_record_read(query_id uuid) RETURNS boolean LANGUAGE sql VOLATILE AS $$ SELECT false $$;
-- +goose StatementEnd
-- +goose Down
DROP FUNCTION manager_shared_history_record_read(uuid);
DROP FUNCTION manager_shared_history_allowed(uuid);
DROP TABLE manager_group_history_queries;
