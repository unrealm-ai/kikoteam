-- +goose Up
CREATE TABLE manager_human_assistant_clarifications (
    input_id uuid NOT NULL REFERENCES manager_inputs(id) ON DELETE CASCADE,
    request_id uuid NOT NULL REFERENCES manager_human_requests(id) ON DELETE CASCADE,
    question_version bigint NOT NULL,
    question_digest text NOT NULL,
    body text NOT NULL,
    PRIMARY KEY(input_id,request_id,question_version)
);

CREATE TABLE manager_human_chat_inputs (
    input_id uuid PRIMARY KEY REFERENCES manager_inputs(id) ON DELETE CASCADE,
    inbox_id uuid NOT NULL UNIQUE REFERENCES manager_channel_inbox(id) ON DELETE CASCADE
);

-- +goose StatementBegin
CREATE FUNCTION manager_human_inbox_confirmation_allowed(inbound uuid, actor uuid, reply_input uuid DEFAULT NULL) RETURNS boolean
LANGUAGE sql STABLE AS $$
 SELECT manager_human_channel_inbox_current(inbound, actor)
 AND EXISTS(SELECT 1 FROM manager_channel_inbox n
 JOIN manager_conversations chat ON chat.id=n.conversation_id
 JOIN manager_human_requests r ON r.workspace_id=chat.workspace_id AND r.agent_id=chat.agent_id
 JOIN manager_sessions task ON task.id=r.session_id
 LEFT JOIN manager_conversations c ON c.id=task.conversation_id
 LEFT JOIN manager_task_spaces p ON p.id=task.task_space_id
 JOIN platform_agent_human_recipients h ON h.agent_id=r.agent_id
 WHERE n.id=inbound
 AND h.recipients @> jsonb_build_array(jsonb_build_object('id',r.question->>'recipient_id','ref',r.recipient))
 AND manager_human_recipient_matches(r.workspace_id,r.agent_id,r.recipient,actor,true)
 AND ((r.state='pending' AND task.lifecycle='active' AND COALESCE(c.lifecycle,p.lifecycle)='active'
       AND task.stop_revision=r.stop_revision AND manager_input_actor_allowed(r.authority_input_id))
      OR (reply_input IS NOT NULL AND r.answer_input_id=reply_input)));
$$;

CREATE FUNCTION manager_human_chat_input_allowed(input_id uuid) RETURNS boolean
LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT FROM manager_human_chat_inputs h
 JOIN manager_inputs i ON i.id=h.input_id
 JOIN manager_channel_inbox n ON n.id=h.inbox_id AND n.input_id=i.id
 JOIN manager_sessions s ON s.id=i.session_id AND s.conversation_id=n.conversation_id
 WHERE h.input_id=$1 AND i.source_kind='user' AND s.mode='chat' AND s.lifecycle='active'
 AND i.channel_principal_id=n.principal_id AND i.channel_generation=n.connection_generation
 AND manager_human_inbox_confirmation_allowed(n.id,i.channel_principal_id,i.id));
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION manager_human_chat_input_allowed(uuid);
DROP FUNCTION manager_human_inbox_confirmation_allowed(uuid,uuid,uuid);
DROP TABLE manager_human_chat_inputs;
DROP TABLE manager_human_assistant_clarifications;
