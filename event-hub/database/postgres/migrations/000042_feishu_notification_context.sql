-- +goose Up
ALTER TABLE manager_trigger_notifications ADD COLUMN context_lease_token uuid, ADD COLUMN context_lease_until timestamptz, ADD COLUMN context_job jsonb, ADD COLUMN context_snapshot jsonb;
ALTER TABLE manager_channel_history_boundaries ALTER COLUMN inbox_id DROP NOT NULL;
ALTER TABLE manager_channel_history_boundaries ADD COLUMN notification_id uuid REFERENCES manager_trigger_notifications(id);
ALTER TABLE manager_channel_history_boundaries ADD CONSTRAINT manager_history_boundary_source CHECK((inbox_id IS NOT NULL)::integer+(notification_id IS NOT NULL)::integer=1);
-- +goose StatementBegin
ALTER FUNCTION manager_input_actor_allowed(uuid) RENAME TO manager_input_actor_before_im_notifications;
CREATE FUNCTION manager_input_actor_allowed(input_id uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT CASE WHEN i.notification_id IS NULL OR c.channel_binding_id IS NULL THEN manager_input_actor_before_im_notifications(i.id) ELSE
 NOT i.trigger_agent_loop AND n.status='sent' AND n.context_status='admitted' AND s.conversation_id=n.conversation_id
 AND c.lifecycle='active' AND a.enabled AND a.admission_epoch=i.admission_epoch AND s.lifecycle='active' AND s.mode='chat'
 AND i.workspace_id=c.workspace_id AND a.id=c.agent_id AND c.channel_chat_type='group'
 AND b.enabled AND b.provider='feishu' AND b.agent_id=a.id AND b.identity_epoch=c.channel_identity_epoch
 AND b.external_tenant_id=c.channel_tenant_id AND b.external_tenant_id=n.target->>'tenant_id'
 AND b.connection_generation=(n.target->>'generation')::bigint
 AND manager_channel_group_allowed(b.id,b.identity_epoch,c.channel_tenant_id,c.external_chat_id,c.channel_thread_id)
 END FROM manager_inputs i JOIN manager_sessions s ON s.id=i.session_id LEFT JOIN manager_trigger_notifications n ON n.id=i.notification_id LEFT JOIN manager_conversations c ON c.id=s.conversation_id LEFT JOIN platform_agents a ON a.id=s.agent_id LEFT JOIN platform_channel_bindings b ON b.id=c.channel_binding_id WHERE i.id=input_id;
$$;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Notification context rollback requires explicit data migration'; END $$;
-- +goose StatementEnd
