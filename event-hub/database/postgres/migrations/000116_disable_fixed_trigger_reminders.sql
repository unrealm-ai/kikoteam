-- +goose Up
-- Stop existing fixed reminders without deleting their plans or their history.
UPDATE manager_triggers
SET enabled=false, version=version+1, next_due=NULL, updated_at=now()
WHERE kind='schedule' AND enabled AND config->>'action'='send_message';

-- Discard only queued reminders that have not reached the provider.
-- Sending and terminal deliveries keep their real outcomes.
DELETE FROM manager_trigger_notifications n
USING manager_triggers t
WHERE t.id=n.trigger_id AND t.kind='schedule' AND t.config->>'action'='send_message'
  AND n.reminder_event_id IS NOT NULL AND n.status IN ('pending','leased');

-- +goose Down
-- Intentionally do not restart notifications as a side effect of rollback.
SELECT 1;
