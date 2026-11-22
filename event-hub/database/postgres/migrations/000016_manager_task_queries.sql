-- +goose Up
-- Read projections use existing Task/Session/Input/Turn facts, with no backfill.
CREATE INDEX manager_tasks_workspace_created_idx ON manager_tasks(workspace_id,created_at DESC,id DESC);
CREATE INDEX manager_tasks_conversation_created_idx ON manager_tasks(workspace_id,conversation_id,created_at DESC,id DESC);

-- +goose Down
DROP INDEX manager_tasks_conversation_created_idx;
DROP INDEX manager_tasks_workspace_created_idx;
