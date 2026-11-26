-- +goose Up
ALTER TABLE manager_task_detail_grants ADD COLUMN full_execution boolean NOT NULL DEFAULT false;
UPDATE manager_task_detail_grants SET revoked_at=clock_timestamp() WHERE revoked_at IS NULL;

-- +goose Down
-- Revoked links must not become live again on rollback.
ALTER TABLE manager_task_detail_grants DROP COLUMN full_execution;
