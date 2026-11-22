-- +goose Up
ALTER TABLE manager_tasks ADD COLUMN source_chat_input_id uuid REFERENCES manager_inputs(id);
ALTER TABLE manager_inputs ADD COLUMN delivery_mode text NOT NULL DEFAULT 'queue' CHECK(delivery_mode IN ('steer','queue'));
ALTER TABLE manager_inputs ALTER COLUMN delivery_mode SET DEFAULT 'steer';
ALTER TABLE manager_inputs ADD COLUMN source_kind text NOT NULL DEFAULT 'user' CHECK(source_kind IN ('user','task_report'));
ALTER TABLE manager_inputs ADD COLUMN report_id uuid UNIQUE;
ALTER TABLE manager_inputs ADD CONSTRAINT manager_report_source CHECK ((source_kind='task_report')=(report_id IS NOT NULL));
CREATE TABLE manager_task_reports (
 id uuid PRIMARY KEY,
 workspace_id uuid NOT NULL,
 source_input_id uuid NOT NULL UNIQUE REFERENCES manager_inputs(id),
 source_turn_id uuid UNIQUE REFERENCES manager_turns(id),
 target_session_id uuid NOT NULL,
 status text NOT NULL CHECK(status IN ('admitted','suppressed')),
 target_input_id uuid UNIQUE REFERENCES manager_inputs(id),
 reason text,
 created_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY(workspace_id,target_session_id) REFERENCES manager_sessions(workspace_id,id),
 CHECK ((status='admitted')=(target_input_id IS NOT NULL))
);
ALTER TABLE manager_inputs ADD CONSTRAINT manager_input_report_fk FOREIGN KEY(report_id) REFERENCES manager_task_reports(id) DEFERRABLE INITIALLY DEFERRED;
-- Only work outstanding at rollout, or accepted subsequently, is reported.
ALTER TABLE manager_inputs ADD COLUMN report_enabled boolean NOT NULL DEFAULT true;
UPDATE manager_inputs SET report_enabled=false WHERE status IN ('processed','canceled','rejected');
CREATE INDEX manager_report_candidates ON manager_inputs(finished_at,id) WHERE report_enabled AND status IN ('processed','canceled','rejected');

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM manager_task_reports) OR EXISTS(SELECT 1 FROM manager_inputs WHERE delivery_mode='steer') THEN
  RAISE EXCEPTION 'cannot discard task reports or steering inputs';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE manager_inputs DROP CONSTRAINT manager_input_report_fk;
DROP TABLE manager_task_reports;
ALTER TABLE manager_tasks DROP COLUMN source_chat_input_id;
ALTER TABLE manager_inputs DROP CONSTRAINT manager_report_source;
ALTER TABLE manager_inputs DROP COLUMN delivery_mode, DROP COLUMN source_kind, DROP COLUMN report_id, DROP COLUMN report_enabled;
