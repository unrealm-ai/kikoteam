-- +goose Up
-- Existing conversations remain the parent channel conversation. Historical
-- native sessions are not rewritten; new topic events get independent sessions.
ALTER TABLE manager_conversations ADD COLUMN channel_thread_id text NOT NULL DEFAULT '';
ALTER TABLE manager_conversations ADD CONSTRAINT manager_channel_thread_check CHECK (channel_binding_id IS NOT NULL OR channel_thread_id='');
DROP INDEX manager_channel_conversation_unique;
CREATE UNIQUE INDEX manager_channel_conversation_unique ON manager_conversations(channel_binding_id,channel_identity_epoch,channel_tenant_id,channel_chat_type,external_chat_id,channel_thread_id) WHERE channel_binding_id IS NOT NULL;
CREATE INDEX manager_channel_inbox_conversation_pending ON manager_channel_inbox(binding_id,identity_epoch,external_tenant_id,external_chat_id,chat_type,thread_id,created_at,id) WHERE status IN ('pending','processing','history');

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM manager_conversations WHERE channel_thread_id<>'') THEN
  RAISE EXCEPTION 'cannot collapse independent topic conversations';
 END IF;
END $$;
-- +goose StatementEnd
DROP INDEX manager_channel_inbox_conversation_pending;
DROP INDEX manager_channel_conversation_unique;
ALTER TABLE manager_conversations DROP COLUMN channel_thread_id;
CREATE UNIQUE INDEX manager_channel_conversation_unique ON manager_conversations(channel_binding_id,channel_identity_epoch,channel_tenant_id,channel_chat_type,external_chat_id) WHERE channel_binding_id IS NOT NULL;
