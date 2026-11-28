-- +goose Up
-- Each delegated input, including a supplement, points to its own user message.
ALTER TABLE manager_inputs ADD COLUMN delegated_from_input_id uuid REFERENCES manager_inputs(id);
CREATE INDEX manager_inputs_delegated_source_idx ON manager_inputs(delegated_from_input_id)
    WHERE delegated_from_input_id IS NOT NULL;

-- A confirmed rejection must survive rollback of the attempted task/input.
-- Empty destination denotes create; append uses the target session UUID.
CREATE TABLE manager_delegation_attachment_retries (
    source_input_id uuid NOT NULL REFERENCES manager_inputs(id) ON DELETE CASCADE,
    destination text NOT NULL,
    required_refs jsonb NOT NULL CHECK (jsonb_typeof(required_refs) = 'array'),
    PRIMARY KEY (source_input_id, destination)
);

-- +goose Down
DROP TABLE manager_delegation_attachment_retries;
ALTER TABLE manager_inputs DROP COLUMN delegated_from_input_id;
