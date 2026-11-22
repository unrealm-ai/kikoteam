-- +goose Up
ALTER TABLE manager_tasks ADD COLUMN planning_state text NOT NULL DEFAULT 'started' CHECK(planning_state IN ('planned','started','canceled'));
ALTER TABLE manager_tasks ADD COLUMN planning_version bigint NOT NULL DEFAULT 1 CHECK(planning_version>0);
ALTER TABLE manager_tasks ADD COLUMN planned_instruction text;
ALTER TABLE manager_tasks ADD CONSTRAINT planned_task_source CHECK(planning_state='started' OR (source_chat_session_id IS NOT NULL AND planned_instruction IS NOT NULL));
CREATE TABLE manager_task_plan_operations (
 workspace_id uuid NOT NULL REFERENCES platform_workspaces(id),source_session_id uuid NOT NULL REFERENCES manager_sessions(id),
 actor_id uuid NOT NULL,request_key text NOT NULL,fingerprint text NOT NULL,
 task_id uuid NOT NULL REFERENCES manager_tasks(id),action text NOT NULL CHECK(action IN ('create_todo','start_todo','cancel_todo','supplement_todo')),
 created_at timestamptz NOT NULL DEFAULT now(),PRIMARY KEY(workspace_id,source_session_id,actor_id,request_key)
);
-- +goose StatementBegin
CREATE FUNCTION manager_planned_task_input_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM manager_sessions s JOIN manager_tasks t ON t.id=s.task_id WHERE s.id=NEW.session_id AND t.planning_state<>'started') THEN
 RAISE EXCEPTION 'planned or canceled task cannot receive execution inputs' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER manager_planned_task_input_guard BEFORE INSERT ON manager_inputs FOR EACH ROW EXECUTE FUNCTION manager_planned_task_input_guard();
-- +goose Down
DROP TRIGGER manager_planned_task_input_guard ON manager_inputs;
DROP FUNCTION manager_planned_task_input_guard();
DROP TABLE manager_task_plan_operations;
ALTER TABLE manager_tasks DROP CONSTRAINT planned_task_source;
ALTER TABLE manager_tasks DROP COLUMN planned_instruction,DROP COLUMN planning_version,DROP COLUMN planning_state;
