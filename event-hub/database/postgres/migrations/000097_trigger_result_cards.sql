-- +goose Up
ALTER TABLE manager_trigger_notifications
    ADD COLUMN task_detail_card_authorized boolean NOT NULL DEFAULT false,
    ADD COLUMN presentation text,
    ADD CONSTRAINT manager_trigger_notification_presentation_valid CHECK
        (presentation IS NULL OR presentation IN ('task_detail_card','task_detail_link'));

-- +goose Down
ALTER TABLE manager_trigger_notifications
    DROP CONSTRAINT manager_trigger_notification_presentation_valid,
    DROP COLUMN presentation,
    DROP COLUMN task_detail_card_authorized;
