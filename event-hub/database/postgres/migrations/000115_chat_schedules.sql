-- +goose Up
CREATE TABLE manager_chat_schedules (
 id uuid PRIMARY KEY,
 workspace_id uuid NOT NULL REFERENCES platform_workspaces(id) ON DELETE CASCADE,
 source_session_id uuid NOT NULL REFERENCES manager_sessions(id) ON DELETE CASCADE,
 source_input_id uuid NOT NULL REFERENCES manager_inputs(id),
 actor_id uuid NOT NULL,
 title text,
 instruction text NOT NULL,
 rule jsonb NOT NULL,
 attachment_ids jsonb NOT NULL DEFAULT '[]'::jsonb,
 attachment_paths jsonb NOT NULL DEFAULT '[]'::jsonb,
 status text NOT NULL CHECK (status IN ('active','paused','canceled','completed')),
 version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
 next_due timestamptz,
 created_at timestamptz NOT NULL DEFAULT now(),
 CHECK ((status='active')=(next_due IS NOT NULL))
);
CREATE INDEX manager_chat_schedules_due ON manager_chat_schedules(next_due,id) WHERE status='active';
CREATE INDEX manager_chat_schedules_owner ON manager_chat_schedules(workspace_id,source_session_id,actor_id,created_at DESC,id DESC);

CREATE TABLE manager_chat_schedule_operations (
 schedule_id uuid NOT NULL REFERENCES manager_chat_schedules(id) ON DELETE CASCADE,
 source_session_id uuid NOT NULL REFERENCES manager_sessions(id) ON DELETE CASCADE,
 actor_id uuid NOT NULL,
 request_hash text NOT NULL,
 PRIMARY KEY (source_session_id,actor_id,request_hash)
);

CREATE TABLE manager_chat_schedule_runs (
 schedule_id uuid NOT NULL REFERENCES manager_chat_schedules(id) ON DELETE CASCADE,
 due timestamptz NOT NULL,
 task_id uuid UNIQUE REFERENCES manager_tasks(id) ON DELETE SET NULL,
 status text NOT NULL CHECK (status IN ('accepted','skipped')),
 reason text NOT NULL DEFAULT '',
 PRIMARY KEY (schedule_id,due)
);

-- +goose Down
DROP TABLE manager_chat_schedule_runs;
DROP TABLE manager_chat_schedule_operations;
DROP TABLE manager_chat_schedules;
