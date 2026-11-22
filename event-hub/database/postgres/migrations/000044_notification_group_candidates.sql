-- +goose Up
-- Discovery is a provider-verified cache, never a client supplied group id.
CREATE TABLE platform_channel_group_candidates (
 id uuid PRIMARY KEY, binding_id uuid NOT NULL REFERENCES platform_channel_bindings(id),
 identity_epoch bigint NOT NULL, external_tenant_id text NOT NULL, connection_generation bigint NOT NULL, config_revision bigint NOT NULL,
 external_chat_id text NOT NULL, display_name text NOT NULL DEFAULT '', confirmed_at timestamptz NOT NULL DEFAULT now(), expires_at timestamptz NOT NULL,
 UNIQUE(binding_id,identity_epoch,external_tenant_id,external_chat_id)
);
ALTER TABLE platform_channel_observed_chats ADD COLUMN id uuid NOT NULL DEFAULT gen_random_uuid() UNIQUE;
ALTER TABLE manager_conversations ADD COLUMN system_owned boolean NOT NULL DEFAULT false;
ALTER TABLE manager_conversations DROP CONSTRAINT manager_conversation_creator;
ALTER TABLE manager_conversations ADD CONSTRAINT manager_conversation_creator CHECK((created_by IS NOT NULL)::integer+(channel_created_by IS NOT NULL)::integer+system_owned::integer=1);
ALTER TABLE manager_conversations DROP CONSTRAINT manager_conversation_channel;
ALTER TABLE manager_conversations ADD CONSTRAINT manager_conversation_channel CHECK((channel_created_by IS NULL OR channel_binding_id IS NOT NULL) AND (channel_binding_id IS NULL OR ((channel_created_by IS NOT NULL OR system_owned) AND web_user_id IS NULL AND external_chat_id IS NOT NULL AND channel_chat_type IS NOT NULL)));
ALTER TABLE manager_conversations ADD CONSTRAINT manager_conversation_system_group CHECK(NOT system_owned OR (web_group_id IS NOT NULL OR (channel_binding_id IS NOT NULL AND channel_chat_type='group')));
ALTER TABLE manager_sessions ADD COLUMN system_owned boolean NOT NULL DEFAULT false;
ALTER TABLE manager_sessions DROP CONSTRAINT manager_session_creator;
ALTER TABLE manager_sessions ADD CONSTRAINT manager_session_creator CHECK((created_by IS NOT NULL)::integer+(channel_created_by IS NOT NULL)::integer+(trigger_id IS NOT NULL)::integer+system_owned::integer=1);
ALTER TABLE manager_sessions ADD CONSTRAINT manager_session_system_chat CHECK(NOT system_owned OR (conversation_id IS NOT NULL AND mode='chat' AND task_id IS NULL));
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'System group ownership requires explicit data migration'; END $$;
-- +goose StatementEnd
