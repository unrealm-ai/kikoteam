-- +goose Up
-- All old inputs retain the previous execution behavior. New silent inputs are
-- held by matching Engine consumers; stop old consumers before this migration.
ALTER TABLE manager_inputs ADD COLUMN trigger_agent_loop boolean NOT NULL DEFAULT true;
ALTER TABLE manager_inputs DROP CONSTRAINT manager_inputs_source_kind_check;
ALTER TABLE manager_inputs ADD CONSTRAINT manager_inputs_source_kind_check CHECK (source_kind IN ('user','task_report','system','human_confirmation'));

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM manager_inputs WHERE NOT trigger_agent_loop OR source_kind IN ('system','human_confirmation')) THEN
  RAISE EXCEPTION 'cannot discard silent input or extended source semantics';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE manager_inputs DROP CONSTRAINT manager_inputs_source_kind_check;
ALTER TABLE manager_inputs ADD CONSTRAINT manager_inputs_source_kind_check CHECK (source_kind IN ('user','task_report'));
ALTER TABLE manager_inputs DROP COLUMN trigger_agent_loop;
