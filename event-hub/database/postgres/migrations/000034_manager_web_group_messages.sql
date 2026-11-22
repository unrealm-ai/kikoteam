-- +goose Up
ALTER TABLE manager_conversations ADD COLUMN web_group_id uuid REFERENCES platform_web_groups(id);
ALTER TABLE manager_conversations ADD COLUMN web_group_epoch bigint;
ALTER TABLE manager_conversations ADD COLUMN web_group_agent_epoch bigint;
ALTER TABLE manager_conversations ADD COLUMN web_group_boundary bigint NOT NULL DEFAULT 0;
ALTER TABLE manager_conversations ADD CONSTRAINT manager_web_group_origin CHECK(web_group_id IS NULL OR (channel_binding_id IS NULL AND web_user_id IS NULL AND web_group_epoch>0 AND web_group_agent_epoch>0));
CREATE UNIQUE INDEX manager_web_group_agent_context ON manager_conversations(web_group_id,agent_id,web_group_epoch,web_group_agent_epoch) WHERE web_group_id IS NOT NULL;
ALTER TABLE manager_inputs ADD COLUMN web_group_member_epoch bigint;
ALTER TABLE manager_inputs ADD COLUMN web_group_reply_done boolean NOT NULL DEFAULT false;
CREATE TABLE manager_web_group_streams(group_id uuid PRIMARY KEY REFERENCES platform_web_groups(id),last_seq bigint NOT NULL DEFAULT 0);
CREATE TABLE manager_web_group_messages(
 id uuid PRIMARY KEY,group_id uuid NOT NULL REFERENCES platform_web_groups(id),workspace_id uuid NOT NULL REFERENCES platform_workspaces(id),
 seq bigint NOT NULL,author_kind text NOT NULL CHECK(author_kind IN ('user','assistant','system')),sender_id uuid REFERENCES platform_accounts(id),agent_id uuid REFERENCES platform_agents(id),
 display_name text NOT NULL,text text NOT NULL,mention_agent_ids jsonb NOT NULL DEFAULT '[]',request_key text,fingerprint text,
 origin_input_id uuid UNIQUE REFERENCES manager_inputs(id),created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(group_id,seq),UNIQUE(group_id,sender_id,request_key)
);
CREATE TABLE manager_web_group_inbox(
 id uuid PRIMARY KEY,group_id uuid NOT NULL REFERENCES platform_web_groups(id),message_id uuid NOT NULL REFERENCES manager_web_group_messages(id),agent_id uuid NOT NULL REFERENCES platform_agents(id),sender_id uuid NOT NULL REFERENCES platform_accounts(id),
 message_seq bigint NOT NULL,group_epoch bigint NOT NULL,agent_epoch bigint NOT NULL,member_epoch bigint NOT NULL,
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','accepted','rejected')),error_code text,conversation_id uuid REFERENCES manager_conversations(id),input_id uuid UNIQUE REFERENCES manager_inputs(id),reply_done boolean NOT NULL DEFAULT false,
 UNIQUE(message_id,agent_id)
);
CREATE INDEX manager_web_group_pending ON manager_web_group_inbox(group_id,agent_id,message_seq) WHERE status='pending';
-- Private engine-only execution scope. Public reads must still exclude these conversations.
-- +goose StatementBegin
CREATE FUNCTION manager_web_group_actor_allowed(conversation uuid,actor uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM manager_conversations c JOIN platform_web_groups g ON g.id=c.web_group_id
 JOIN platform_web_group_agents ga ON ga.group_id=g.id AND ga.agent_id=c.agent_id
 WHERE c.id=conversation AND c.lifecycle='active' AND c.web_group_epoch=g.admission_epoch
 AND c.web_group_agent_epoch=ga.admission_epoch
 AND platform_web_group_agent_allowed(c.workspace_id,g.id,c.agent_id,actor)); $$;
-- +goose StatementEnd
ALTER FUNCTION manager_input_actor_allowed(uuid) RENAME TO manager_input_actor_before_web_groups;
-- +goose StatementBegin
CREATE FUNCTION manager_input_actor_allowed(input uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT CASE WHEN c.web_group_id IS NULL THEN manager_input_actor_before_web_groups(i.id) ELSE manager_web_group_actor_allowed(c.id,i.submitted_by) AND EXISTS(SELECT 1 FROM platform_web_group_members gm WHERE gm.group_id=c.web_group_id AND gm.user_id=i.submitted_by AND gm.admission_epoch=i.web_group_member_epoch) END
 FROM manager_inputs i JOIN manager_sessions s ON s.id=i.session_id JOIN manager_conversations c ON c.id=s.conversation_id WHERE i.id=input; $$;
-- +goose StatementEnd
ALTER FUNCTION manager_input_admission_allowed(uuid) RENAME TO manager_input_admission_before_web_groups;
-- +goose StatementBegin
CREATE FUNCTION manager_input_admission_allowed(input uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT CASE WHEN c.web_group_id IS NULL THEN manager_input_admission_before_web_groups(i.id) ELSE manager_input_actor_allowed(i.id) END
 FROM manager_inputs i JOIN manager_sessions s ON s.id=i.session_id JOIN manager_conversations c ON c.id=s.conversation_id WHERE i.id=input; $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN IF EXISTS(SELECT 1 FROM manager_web_group_messages) OR EXISTS(SELECT 1 FROM manager_conversations WHERE web_group_id IS NOT NULL) THEN RAISE EXCEPTION 'cannot discard web group messages or execution identity'; END IF; END $$;
-- +goose StatementEnd
DROP FUNCTION manager_input_admission_allowed(uuid);
ALTER FUNCTION manager_input_admission_before_web_groups(uuid) RENAME TO manager_input_admission_allowed;
DROP FUNCTION manager_input_actor_allowed(uuid);
ALTER FUNCTION manager_input_actor_before_web_groups(uuid) RENAME TO manager_input_actor_allowed;
DROP FUNCTION manager_web_group_actor_allowed(uuid,uuid);
DROP TABLE manager_web_group_inbox,manager_web_group_messages,manager_web_group_streams;
ALTER TABLE manager_inputs DROP COLUMN web_group_member_epoch,DROP COLUMN web_group_reply_done;
DROP INDEX manager_web_group_agent_context;
ALTER TABLE manager_conversations DROP COLUMN web_group_id,DROP COLUMN web_group_epoch,DROP COLUMN web_group_agent_epoch,DROP COLUMN web_group_boundary;
