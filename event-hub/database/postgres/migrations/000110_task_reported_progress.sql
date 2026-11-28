-- +goose Up
CREATE INDEX manager_task_progress_latest_idx ON manager_session_events
 (session_id, (event->>'turn_id'), seq DESC) WHERE event->>'type'='task.progress_updated';
CREATE UNIQUE INDEX manager_task_progress_plan_version_ux ON manager_session_events
 (session_id, (event->>'turn_id'), (event->>'plan_version'))
 WHERE event->>'type'='task.progress_updated' AND event->>'source'='agent';

-- +goose Down
DROP INDEX manager_task_progress_plan_version_ux;
DROP INDEX manager_task_progress_latest_idx;
