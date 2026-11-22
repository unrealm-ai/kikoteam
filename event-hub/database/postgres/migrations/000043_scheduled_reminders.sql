-- +goose Up
ALTER TABLE manager_trigger_events DROP CONSTRAINT manager_trigger_events_action_check;
ALTER TABLE manager_trigger_events ADD CONSTRAINT manager_trigger_events_action_check CHECK(action IN ('create_task','supplement_task','cancel_task','send_message'));
ALTER TABLE manager_trigger_notifications ALTER COLUMN task_id DROP NOT NULL;
ALTER TABLE manager_trigger_notifications ALTER COLUMN source_input_id DROP NOT NULL;
ALTER TABLE manager_trigger_notifications ADD COLUMN reminder_event_id uuid REFERENCES manager_trigger_events(id);
ALTER TABLE manager_trigger_notifications ADD CONSTRAINT trigger_notification_source_check CHECK(
 (reminder_event_id IS NULL AND task_id IS NOT NULL AND source_input_id IS NOT NULL)
 OR (reminder_event_id IS NOT NULL AND task_id IS NULL AND source_input_id IS NULL AND source_turn_id IS NULL));
CREATE UNIQUE INDEX trigger_reminder_occurrence_target ON manager_trigger_notifications(reminder_event_id,conversation_id) WHERE reminder_event_id IS NOT NULL;
-- +goose Down
ALTER TABLE manager_trigger_notifications DROP CONSTRAINT trigger_notification_source_check;
DROP INDEX trigger_reminder_occurrence_target;
ALTER TABLE manager_trigger_notifications DROP COLUMN reminder_event_id;
ALTER TABLE manager_trigger_notifications ALTER COLUMN task_id SET NOT NULL;
ALTER TABLE manager_trigger_notifications ALTER COLUMN source_input_id SET NOT NULL;
ALTER TABLE manager_trigger_events DROP CONSTRAINT manager_trigger_events_action_check;
ALTER TABLE manager_trigger_events ADD CONSTRAINT manager_trigger_events_action_check CHECK(action IN ('create_task','supplement_task','cancel_task'));
