-- +goose Up
ALTER TABLE manager_sessions ADD COLUMN required_actions jsonb NOT NULL DEFAULT '[]'
 CHECK (jsonb_typeof(required_actions)='array');
CREATE INDEX manager_sessions_waiting ON manager_sessions(id) WHERE required_actions <> '[]'::jsonb;
ALTER TABLE manager_inputs ADD COLUMN confirmation_request_id uuid, ADD COLUMN confirmed boolean,
 ADD CHECK ((confirmation_request_id IS NULL)=(confirmed IS NULL)),
 ADD CHECK (confirmation_request_id IS NULL OR (source_kind='user' AND trigger_agent_loop));
CREATE UNIQUE INDEX manager_confirmation_answer ON manager_inputs(session_id,confirmation_request_id)
 WHERE confirmation_request_id IS NOT NULL;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT FROM manager_sessions WHERE required_actions <> '[]') OR
    EXISTS (SELECT FROM manager_inputs WHERE confirmation_request_id IS NOT NULL) THEN
   RAISE EXCEPTION 'confirmation data exists';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE manager_inputs DROP COLUMN confirmation_request_id, DROP COLUMN confirmed;
ALTER TABLE manager_sessions DROP COLUMN required_actions;
