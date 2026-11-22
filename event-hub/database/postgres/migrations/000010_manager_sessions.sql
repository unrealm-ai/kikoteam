-- +goose Up
-- Business ownership stays in Manager. Engine confirms execution facts under
-- the current assignment; native Pi history remains on its persistent host.
CREATE TABLE manager_conversations (
 id uuid PRIMARY KEY,
 workspace_id uuid NOT NULL,
 agent_id uuid NOT NULL,
 created_by uuid NOT NULL REFERENCES platform_accounts(id),
 title text CHECK(length(title) BETWEEN 1 AND 200),
 lifecycle text NOT NULL DEFAULT 'active' CHECK(lifecycle IN ('active','archived')),
 created_at timestamptz NOT NULL DEFAULT now(),
 archived_at timestamptz,
 UNIQUE(workspace_id,id),
 UNIQUE(workspace_id,id,agent_id),
 FOREIGN KEY(workspace_id,agent_id) REFERENCES platform_agents(workspace_id,id),
 CHECK((lifecycle='archived')=(archived_at IS NOT NULL))
);
CREATE TABLE manager_tasks (
 id uuid PRIMARY KEY,
 workspace_id uuid NOT NULL,
 conversation_id uuid NOT NULL,
 source_chat_session_id uuid,
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(workspace_id,conversation_id,id),
 FOREIGN KEY(workspace_id,conversation_id) REFERENCES manager_conversations(workspace_id,id)
);
CREATE TABLE manager_sessions (
 id uuid PRIMARY KEY,
 workspace_id uuid NOT NULL,
 agent_id uuid NOT NULL,
 conversation_id uuid NOT NULL,
 task_id uuid UNIQUE,
 created_by uuid NOT NULL REFERENCES platform_accounts(id),
 mode text NOT NULL CHECK(mode IN ('chat','task')),
 title text CHECK(length(title) BETWEEN 1 AND 200),
 lifecycle text NOT NULL DEFAULT 'active' CHECK(lifecycle IN ('active','archived')),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision>0),
 configuration jsonb NOT NULL,
 initial_input_id uuid,
 last_input_seq bigint NOT NULL DEFAULT 0,
 last_turn_no bigint NOT NULL DEFAULT 0,
 last_event_seq bigint NOT NULL DEFAULT 0,
 stop_input_seq bigint NOT NULL DEFAULT 0,
 stop_revision bigint NOT NULL DEFAULT 0,
 applied_stop_revision bigint NOT NULL DEFAULT 0,
 engine_id uuid,
 assignment_id uuid,
 generation bigint NOT NULL DEFAULT 0,
 native_session_id text,
 native_host_id text,
 active_turn_id uuid,
 background_process_count integer DEFAULT 0 CHECK(background_process_count>=0),
 block_reason text,
 created_at timestamptz NOT NULL DEFAULT now(),
 archived_at timestamptz,
 UNIQUE(workspace_id,id),
 UNIQUE(workspace_id,conversation_id,id),
 FOREIGN KEY(workspace_id,conversation_id,agent_id) REFERENCES manager_conversations(workspace_id,id,agent_id),
 FOREIGN KEY(workspace_id,conversation_id,task_id) REFERENCES manager_tasks(workspace_id,conversation_id,id),
 CHECK((mode='task')=(task_id IS NOT NULL)),
 CHECK((lifecycle='archived')=(archived_at IS NOT NULL)),
 CHECK(stop_input_seq<=last_input_seq),
 CHECK(applied_stop_revision<=stop_revision),
 CHECK((engine_id IS NULL)=(assignment_id IS NULL))
);
CREATE UNIQUE INDEX manager_chat_unique ON manager_sessions(conversation_id) WHERE mode='chat';
ALTER TABLE manager_tasks ADD CONSTRAINT manager_task_source_chat_fk FOREIGN KEY(workspace_id,conversation_id,source_chat_session_id) REFERENCES manager_sessions(workspace_id,conversation_id,id);
CREATE TABLE manager_inputs (
 id uuid PRIMARY KEY,
 workspace_id uuid NOT NULL,
 session_id uuid NOT NULL,
 submitted_by uuid NOT NULL REFERENCES platform_accounts(id),
 admission_epoch bigint NOT NULL,
 seq bigint NOT NULL CHECK(seq>0),
 text text NOT NULL CHECK(length(text) BETWEEN 1 AND 65536),
 status text NOT NULL DEFAULT 'queued' CHECK(status IN ('queued','started','processed','canceled','rejected')),
 turn_id uuid,
 cancel_reason text,
 rejection_reason text,
 created_at timestamptz NOT NULL DEFAULT now(),
 started_at timestamptz,
 finished_at timestamptz,
 UNIQUE(workspace_id,session_id,id),
 UNIQUE(session_id,seq),
 FOREIGN KEY(workspace_id,session_id) REFERENCES manager_sessions(workspace_id,id)
);
CREATE INDEX manager_inputs_queued ON manager_inputs(session_id,seq) WHERE status='queued';
CREATE TABLE manager_turns (
 id uuid PRIMARY KEY,
 workspace_id uuid NOT NULL,
 session_id uuid NOT NULL,
 input_id uuid NOT NULL UNIQUE,
 turn_no bigint NOT NULL CHECK(turn_no>0),
 assignment_id uuid NOT NULL,
 generation bigint NOT NULL,
 status text NOT NULL CHECK(status IN ('running','stopping','completed','failed','canceled','interrupted')),
 result_summary text,
 error jsonb,
 usage jsonb,
 has_unknown_tool_results boolean NOT NULL DEFAULT false,
 started_at timestamptz NOT NULL DEFAULT now(),
 finished_at timestamptz,
 UNIQUE(workspace_id,session_id,id),
 UNIQUE(session_id,turn_no),
 FOREIGN KEY(workspace_id,session_id,input_id) REFERENCES manager_inputs(workspace_id,session_id,id),
 CHECK((status IN ('running','stopping'))=(finished_at IS NULL))
);
CREATE UNIQUE INDEX manager_one_active_turn ON manager_turns(session_id) WHERE status IN ('running','stopping');
ALTER TABLE manager_sessions ADD CONSTRAINT manager_session_active_turn_fk FOREIGN KEY(workspace_id,id,active_turn_id) REFERENCES manager_turns(workspace_id,session_id,id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE manager_sessions ADD CONSTRAINT manager_session_initial_input_fk FOREIGN KEY(workspace_id,id,initial_input_id) REFERENCES manager_inputs(workspace_id,session_id,id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE manager_inputs ADD CONSTRAINT manager_input_turn_fk FOREIGN KEY(workspace_id,session_id,turn_id) REFERENCES manager_turns(workspace_id,session_id,id) DEFERRABLE INITIALLY DEFERRED;
CREATE TABLE manager_session_events (
 workspace_id uuid NOT NULL,
 session_id uuid NOT NULL,
 seq bigint NOT NULL CHECK(seq>0),
 event jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(session_id,seq),
 FOREIGN KEY(workspace_id,session_id) REFERENCES manager_sessions(workspace_id,id)
);
CREATE TABLE manager_input_dedup (
 workspace_id uuid NOT NULL,
 actor_id uuid NOT NULL,
 operation_kind text NOT NULL,
 target text NOT NULL,
 request_key text NOT NULL CHECK(length(request_key) BETWEEN 1 AND 128),
 fingerprint text NOT NULL,
 session_id uuid NOT NULL,
 input_id uuid NOT NULL,
 PRIMARY KEY(workspace_id,actor_id,operation_kind,target,request_key),
 FOREIGN KEY(workspace_id,session_id,input_id) REFERENCES manager_inputs(workspace_id,session_id,id)
);
CREATE TABLE manager_engines (
 id uuid PRIMARY KEY,
 host_id text NOT NULL,
 endpoint text NOT NULL,
 capacity integer NOT NULL CHECK(capacity>0),
 accepting boolean NOT NULL DEFAULT true,
 heartbeat_seq bigint NOT NULL DEFAULT 0,
 last_seen_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE manager_conversation_sandboxes (
 id uuid PRIMARY KEY,
 workspace_id uuid NOT NULL,
 conversation_id uuid NOT NULL UNIQUE,
 manager_engine_id uuid NOT NULL REFERENCES manager_engines(id),
 host_id text NOT NULL,
 state text NOT NULL DEFAULT 'preparing' CHECK(state IN ('preparing','ready','stopping','stopped','unknown')),
 created_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY(workspace_id,conversation_id) REFERENCES manager_conversations(workspace_id,id)
);
CREATE INDEX manager_conversations_list ON manager_conversations(workspace_id,created_at DESC,id DESC);
CREATE INDEX manager_sessions_list ON manager_sessions(workspace_id,created_at DESC,id DESC);

-- +goose Down
DROP TABLE manager_conversation_sandboxes;
DROP TABLE manager_engines;
DROP TABLE manager_input_dedup;
DROP TABLE manager_session_events;
ALTER TABLE manager_inputs DROP CONSTRAINT manager_input_turn_fk;
ALTER TABLE manager_sessions DROP CONSTRAINT manager_session_active_turn_fk;
ALTER TABLE manager_sessions DROP CONSTRAINT manager_session_initial_input_fk;
DROP TABLE manager_turns;
DROP TABLE manager_inputs;
ALTER TABLE manager_tasks DROP CONSTRAINT manager_task_source_chat_fk;
DROP TABLE manager_sessions;
DROP TABLE manager_tasks;
DROP TABLE manager_conversations;
