-- +goose Up
ALTER TABLE manager_conversations ADD COLUMN last_message_at timestamptz;

UPDATE manager_conversations c
SET last_message_at = history.last_message_at
FROM (
 SELECT s.conversation_id, max(e.created_at) AS last_message_at
 FROM manager_sessions s
 JOIN manager_session_events e ON e.session_id=s.id
 WHERE s.mode='chat' AND e.event->>'type'='agent.message'
   AND e.event->>'status'='completed' AND e.event->>'turn_id' IS NOT NULL
 GROUP BY s.conversation_id
) history
WHERE c.id=history.conversation_id;

DROP INDEX manager_conversations_list;
CREATE INDEX manager_conversations_list ON manager_conversations
 (workspace_id, (COALESCE(last_message_at, created_at)) DESC, id DESC)
 WHERE web_group_id IS NULL AND channel_binding_id IS NULL;
