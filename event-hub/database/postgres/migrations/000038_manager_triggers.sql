-- +goose Up
CREATE TABLE manager_triggers (
 id uuid PRIMARY KEY, workspace_id uuid NOT NULL, agent_id uuid NOT NULL,
 name text NOT NULL DEFAULT '触发器' CHECK(length(name) BETWEEN 1 AND 100),
 kind text NOT NULL CHECK(kind IN ('schedule','webhook')), enabled boolean NOT NULL DEFAULT false,
 version bigint NOT NULL DEFAULT 1 CHECK(version>0), config jsonb NOT NULL,
 allowed_actions jsonb NOT NULL, notification_targets jsonb NOT NULL DEFAULT '[]'::jsonb, created_by uuid NOT NULL REFERENCES platform_accounts(id),
 next_due timestamptz, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY(workspace_id,agent_id) REFERENCES platform_agents(workspace_id,id),
 UNIQUE(workspace_id,agent_id,id), UNIQUE(workspace_id,id)
);
CREATE INDEX manager_trigger_schedule_due ON manager_triggers(next_due,id) WHERE enabled AND kind='schedule';
CREATE TABLE manager_trigger_credentials (
 trigger_id uuid NOT NULL REFERENCES manager_triggers(id), key_id uuid NOT NULL,
 ciphertext bytea NOT NULL, status text NOT NULL DEFAULT 'active' CHECK(status IN ('active','revoked')),
 created_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(trigger_id,key_id)
);
CREATE TABLE manager_trigger_events (
 id uuid PRIMARY KEY, trigger_id uuid NOT NULL, workspace_id uuid NOT NULL, agent_id uuid NOT NULL,
 binding_version bigint NOT NULL CHECK(binding_version>0), event_id text NOT NULL CHECK(length(event_id) BETWEEN 1 AND 160),
 body_hash text NOT NULL CHECK(body_hash ~ '^[a-f0-9]{64}$'), action text NOT NULL CHECK(action IN ('create_task','supplement_task','cancel_task')),
 payload jsonb NOT NULL, occurred_at timestamptz NOT NULL, received_at timestamptz NOT NULL DEFAULT now(),
 task_id uuid, session_id uuid, input_id uuid, receipt jsonb,
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','accepted','rejected')),
 FOREIGN KEY(workspace_id,agent_id,trigger_id) REFERENCES manager_triggers(workspace_id,agent_id,id),
 FOREIGN KEY(workspace_id,task_id) REFERENCES manager_tasks(workspace_id,id),
 UNIQUE(trigger_id,event_id), UNIQUE(workspace_id,id)
);
CREATE INDEX manager_trigger_event_history ON manager_trigger_events(trigger_id,received_at DESC,id DESC);
ALTER TABLE manager_tasks ADD COLUMN trigger_id uuid;
ALTER TABLE manager_tasks ADD COLUMN trigger_version bigint;
ALTER TABLE manager_tasks ADD FOREIGN KEY(workspace_id,agent_id,trigger_id) REFERENCES manager_triggers(workspace_id,agent_id,id);
ALTER TABLE manager_tasks ADD CONSTRAINT manager_task_trigger CHECK((trigger_id IS NULL)=(trigger_version IS NULL) AND (trigger_id IS NULL OR (task_space_id IS NOT NULL AND trigger_version>0)));
ALTER TABLE manager_sessions ADD COLUMN trigger_id uuid;
ALTER TABLE manager_sessions ADD COLUMN trigger_version bigint;
ALTER TABLE manager_sessions ADD FOREIGN KEY(workspace_id,agent_id,trigger_id) REFERENCES manager_triggers(workspace_id,agent_id,id);
ALTER TABLE manager_sessions DROP CONSTRAINT manager_session_creator;
ALTER TABLE manager_sessions ADD CONSTRAINT manager_session_creator CHECK((created_by IS NOT NULL)::integer+(channel_created_by IS NOT NULL)::integer+(trigger_id IS NOT NULL)::integer=1);
ALTER TABLE manager_sessions DROP CONSTRAINT manager_session_space_mode;
ALTER TABLE manager_sessions ADD CONSTRAINT manager_session_space_mode CHECK(task_space_id IS NULL OR (mode='task' AND channel_created_by IS NULL AND (trigger_id IS NOT NULL OR (task_space_agent_epoch IS NOT NULL AND task_space_agent_epoch>0))));
-- Trusted trigger sessions do not silently create manual employee grants.
ALTER TABLE manager_sessions DROP CONSTRAINT manager_session_space_agent_fk;
ALTER TABLE manager_sessions ADD CONSTRAINT manager_session_trigger CHECK((trigger_id IS NULL)=(trigger_version IS NULL) AND (trigger_id IS NULL OR (task_space_id IS NOT NULL AND trigger_version>0 AND task_space_agent_epoch IS NULL)));
ALTER TABLE manager_inputs ADD COLUMN trigger_event_id uuid;
ALTER TABLE manager_inputs ADD FOREIGN KEY(workspace_id,trigger_event_id) REFERENCES manager_trigger_events(workspace_id,id);
ALTER TABLE manager_inputs DROP CONSTRAINT manager_input_actor;
ALTER TABLE manager_inputs ADD CONSTRAINT manager_input_actor CHECK((submitted_by IS NOT NULL)::integer+(channel_principal_id IS NOT NULL)::integer+(trigger_event_id IS NOT NULL)::integer=1 AND (channel_principal_id IS NULL)=(channel_generation IS NULL));
-- +goose StatementBegin
CREATE FUNCTION manager_trigger_admin_allowed(trigger_id uuid,actor text) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM manager_triggers t JOIN platform_members m ON m.workspace_id=t.workspace_id AND m.user_id=t.created_by WHERE t.id=trigger_id AND t.created_by::text=actor AND m.status='active' AND m.role IN ('owner','admin'));
$$;
CREATE FUNCTION manager_trigger_input_allowed(input_id uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM manager_inputs i JOIN manager_trigger_events e ON e.id=i.trigger_event_id
 JOIN manager_sessions s ON s.id=i.session_id JOIN manager_tasks k ON k.id=s.task_id
 JOIN manager_triggers t ON t.id=e.trigger_id JOIN platform_agents a ON a.id=t.agent_id
 JOIN manager_task_spaces p ON p.id=s.task_space_id
 WHERE i.id=input_id AND t.enabled AND a.enabled AND p.lifecycle='active'
 AND e.workspace_id=i.workspace_id AND s.workspace_id=t.workspace_id AND s.agent_id=t.agent_id
 AND e.binding_version=t.version AND s.trigger_id=t.id AND s.trigger_version=t.version
 AND k.trigger_id=t.id AND k.trigger_version=t.version AND e.task_id=k.id AND e.session_id=s.id
 AND e.input_id=i.id AND e.status='accepted' AND i.submitted_by IS NULL AND i.channel_principal_id IS NULL);
$$;
ALTER FUNCTION manager_input_actor_allowed(uuid) RENAME TO manager_input_actor_before_triggers;
CREATE FUNCTION manager_input_actor_allowed(input_id uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT CASE WHEN i.trigger_event_id IS NULL THEN manager_input_actor_before_triggers(i.id) ELSE manager_trigger_input_allowed(i.id) END FROM manager_inputs i WHERE i.id=input_id;
$$;
ALTER FUNCTION manager_input_admission_allowed(uuid) RENAME TO manager_input_admission_before_triggers;
CREATE FUNCTION manager_input_admission_allowed(input_id uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT CASE WHEN i.trigger_event_id IS NULL THEN manager_input_admission_before_triggers(i.id) ELSE manager_trigger_input_allowed(i.id) END FROM manager_inputs i WHERE i.id=input_id;
$$;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Trigger rollback requires explicit data migration'; END $$;
-- +goose StatementEnd
