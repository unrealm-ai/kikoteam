-- +goose Up
CREATE TABLE manager_task_spaces (
 id uuid PRIMARY KEY,
 workspace_id uuid NOT NULL REFERENCES platform_workspaces(id),
 business_key text CHECK(length(business_key) BETWEEN 1 AND 200),
 created_by uuid NOT NULL REFERENCES platform_accounts(id),
 lifecycle text NOT NULL DEFAULT 'active' CHECK(lifecycle IN ('active','archived')),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(workspace_id,id), UNIQUE(workspace_id,business_key)
);
CREATE TABLE manager_task_space_agents (
 task_space_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 agent_id uuid NOT NULL,
 status text NOT NULL DEFAULT 'active' CHECK(status IN ('active','removed')),
 admission_epoch bigint NOT NULL DEFAULT 1 CHECK(admission_epoch>0),
 PRIMARY KEY(task_space_id,agent_id),
 FOREIGN KEY(workspace_id,task_space_id) REFERENCES manager_task_spaces(workspace_id,id),
 FOREIGN KEY(workspace_id,agent_id) REFERENCES platform_agents(workspace_id,id)
);
CREATE TABLE manager_task_space_requests (
 workspace_id uuid NOT NULL,
 actor_id uuid NOT NULL REFERENCES platform_accounts(id),
 request_key text NOT NULL CHECK(length(request_key) BETWEEN 1 AND 128),
 fingerprint text NOT NULL,
 task_space_id uuid NOT NULL,
 PRIMARY KEY(workspace_id,actor_id,request_key),
 FOREIGN KEY(workspace_id,task_space_id) REFERENCES manager_task_spaces(workspace_id,id)
);
ALTER TABLE manager_tasks ALTER COLUMN conversation_id DROP NOT NULL;
ALTER TABLE manager_tasks ADD COLUMN task_space_id uuid;
ALTER TABLE manager_tasks ADD COLUMN agent_id uuid;
UPDATE manager_tasks k SET agent_id=c.agent_id FROM manager_conversations c WHERE c.id=k.conversation_id;
ALTER TABLE manager_tasks ALTER COLUMN agent_id SET NOT NULL;
ALTER TABLE manager_tasks ADD FOREIGN KEY(workspace_id,agent_id) REFERENCES platform_agents(workspace_id,id);
ALTER TABLE manager_tasks ADD CONSTRAINT manager_task_space_owner CHECK ((conversation_id IS NULL)<>(task_space_id IS NULL));
ALTER TABLE manager_tasks ADD CONSTRAINT manager_task_space_source CHECK (task_space_id IS NULL OR (source_chat_session_id IS NULL AND source_chat_input_id IS NULL));
ALTER TABLE manager_tasks ADD CONSTRAINT manager_task_space_fk FOREIGN KEY(workspace_id,task_space_id) REFERENCES manager_task_spaces(workspace_id,id);
ALTER TABLE manager_tasks ADD UNIQUE(workspace_id,id);
ALTER TABLE manager_tasks ADD UNIQUE(workspace_id,task_space_id,agent_id,id);
ALTER TABLE manager_sessions ALTER COLUMN conversation_id DROP NOT NULL;
ALTER TABLE manager_sessions ADD COLUMN task_space_id uuid;
ALTER TABLE manager_sessions ADD COLUMN task_space_agent_epoch bigint;
ALTER TABLE manager_sessions ADD CONSTRAINT manager_session_space_owner CHECK ((conversation_id IS NULL)<>(task_space_id IS NULL));
ALTER TABLE manager_sessions ADD CONSTRAINT manager_session_space_mode CHECK (task_space_id IS NULL OR (mode='task' AND channel_created_by IS NULL AND task_space_agent_epoch IS NOT NULL AND task_space_agent_epoch>0));
ALTER TABLE manager_sessions ADD CONSTRAINT manager_session_space_fk FOREIGN KEY(workspace_id,task_space_id,agent_id,task_id) REFERENCES manager_tasks(workspace_id,task_space_id,agent_id,id);
ALTER TABLE manager_sessions ADD CONSTRAINT manager_session_space_agent_fk FOREIGN KEY(task_space_id,agent_id) REFERENCES manager_task_space_agents(task_space_id,agent_id);
ALTER TABLE manager_conversation_sandboxes ALTER COLUMN conversation_id DROP NOT NULL;
ALTER TABLE manager_conversation_sandboxes ADD COLUMN task_id uuid UNIQUE;
ALTER TABLE manager_conversation_sandboxes ADD CONSTRAINT manager_sandbox_owner CHECK ((conversation_id IS NULL)<>(task_id IS NULL));
ALTER TABLE manager_conversation_sandboxes ADD CONSTRAINT manager_sandbox_task_fk FOREIGN KEY(workspace_id,task_id) REFERENCES manager_tasks(workspace_id,id);
CREATE INDEX manager_task_space_sessions ON manager_sessions(task_space_id,created_at,id) WHERE task_space_id IS NOT NULL;
-- +goose StatementBegin
CREATE FUNCTION manager_resolve_task_agent() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.conversation_id IS NOT NULL THEN
   SELECT agent_id INTO NEW.agent_id FROM manager_conversations WHERE id=NEW.conversation_id AND workspace_id=NEW.workspace_id;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER manager_task_agent_owner BEFORE INSERT OR UPDATE ON manager_tasks FOR EACH ROW EXECUTE FUNCTION manager_resolve_task_agent();
CREATE FUNCTION manager_check_task_sandbox() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.task_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM manager_tasks WHERE id=NEW.task_id AND workspace_id=NEW.workspace_id AND task_space_id IS NOT NULL) THEN RAISE EXCEPTION 'sandbox task must belong to TaskSpace'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER manager_task_sandbox_owner BEFORE INSERT OR UPDATE ON manager_conversation_sandboxes FOR EACH ROW EXECUTE FUNCTION manager_check_task_sandbox();
CREATE FUNCTION manager_task_space_actor_allowed(space uuid,actor text,employee uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM manager_task_spaces p JOIN manager_task_space_agents g ON g.task_space_id=p.id
 JOIN platform_members m ON m.workspace_id=p.workspace_id AND m.user_id::text=actor AND m.status='active'
 WHERE p.id=space AND p.created_by::text=actor AND p.lifecycle='active' AND g.agent_id=employee AND g.status='active'
 AND platform_agent_web_allowed(p.workspace_id,employee,m.user_id));
$$;
ALTER FUNCTION manager_input_actor_allowed(uuid) RENAME TO manager_input_actor_before_task_spaces;
CREATE FUNCTION manager_input_actor_allowed(input_id uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT CASE WHEN s.task_space_id IS NULL THEN manager_input_actor_before_task_spaces(i.id)
 ELSE i.channel_principal_id IS NULL AND manager_task_space_actor_allowed(s.task_space_id,i.submitted_by::text,s.agent_id)
 AND s.task_space_agent_epoch=g.admission_epoch END
 FROM manager_inputs i JOIN manager_sessions s ON s.id=i.session_id
 LEFT JOIN manager_task_space_agents g ON g.task_space_id=s.task_space_id AND g.agent_id=s.agent_id WHERE i.id=input_id;
$$;
ALTER FUNCTION manager_input_admission_allowed(uuid) RENAME TO manager_input_admission_before_task_spaces;
CREATE FUNCTION manager_input_admission_allowed(input_id uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT CASE WHEN s.task_space_id IS NULL THEN manager_input_admission_before_task_spaces(i.id)
 ELSE manager_input_actor_allowed(i.id) END FROM manager_inputs i JOIN manager_sessions s ON s.id=i.session_id WHERE i.id=input_id;
$$;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'TaskSpace rollback requires explicit ownership/data migration'; END $$;
-- +goose StatementEnd
