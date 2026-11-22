-- +goose Up
ALTER TABLE manager_inputs ADD COLUMN notification_id uuid REFERENCES manager_trigger_notifications(id);
ALTER TABLE manager_inputs DROP CONSTRAINT manager_input_actor;
ALTER TABLE manager_inputs ADD CONSTRAINT manager_input_actor CHECK((submitted_by IS NOT NULL)::integer+(channel_principal_id IS NOT NULL)::integer+(trigger_event_id IS NOT NULL)::integer+(notification_id IS NOT NULL)::integer=1 AND (channel_principal_id IS NULL)=(channel_generation IS NULL));
ALTER TABLE manager_inputs DROP CONSTRAINT manager_inputs_source_kind_check;
ALTER TABLE manager_inputs ADD CONSTRAINT manager_inputs_source_kind_check CHECK(source_kind IN ('user','task_report','system','human_confirmation','task_result'));
ALTER TABLE manager_inputs ADD CONSTRAINT manager_notification_context_only CHECK(notification_id IS NULL OR (NOT trigger_agent_loop AND report_id IS NULL AND delivery_mode='queue'));
ALTER TABLE manager_inputs ADD CONSTRAINT manager_task_result_server_source CHECK(source_kind<>'task_result' OR (notification_id IS NOT NULL AND NOT trigger_agent_loop));
-- +goose StatementBegin
ALTER FUNCTION manager_input_actor_allowed(uuid) RENAME TO manager_input_actor_before_notifications;
CREATE FUNCTION manager_input_actor_allowed(input_id uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT CASE WHEN i.notification_id IS NULL THEN manager_input_actor_before_notifications(i.id) ELSE
 NOT i.trigger_agent_loop AND n.status='sent' AND n.context_status='admitted' AND s.conversation_id=n.conversation_id
 AND c.lifecycle='active' AND a.enabled AND a.admission_epoch=i.admission_epoch AND s.lifecycle='active' AND s.mode='chat'
 AND i.workspace_id=c.workspace_id AND a.id=c.agent_id
 AND (c.web_group_id IS NOT NULL AND EXISTS(SELECT 1 FROM platform_web_groups g JOIN platform_web_group_agents ga ON ga.group_id=g.id WHERE g.id=c.web_group_id AND g.status='active' AND ga.agent_id=c.agent_id AND ga.status='active' AND g.admission_epoch=c.web_group_epoch AND ga.admission_epoch=c.web_group_agent_epoch))
 END FROM manager_inputs i JOIN manager_sessions s ON s.id=i.session_id LEFT JOIN manager_trigger_notifications n ON n.id=i.notification_id LEFT JOIN manager_conversations c ON c.id=s.conversation_id LEFT JOIN platform_agents a ON a.id=s.agent_id WHERE i.id=input_id;
$$;
ALTER FUNCTION manager_input_admission_allowed(uuid) RENAME TO manager_input_admission_before_notifications;
CREATE FUNCTION manager_input_admission_allowed(input_id uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT CASE WHEN notification_id IS NULL THEN manager_input_admission_before_notifications(id) ELSE manager_input_actor_allowed(id) END FROM manager_inputs WHERE id=input_id;
$$;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Notification rollback requires explicit context data migration'; END $$;
-- +goose StatementEnd
