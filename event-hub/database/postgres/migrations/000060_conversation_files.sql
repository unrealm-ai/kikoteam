-- +goose Up
ALTER TABLE manager_inputs ADD COLUMN attachments jsonb NOT NULL DEFAULT '[]'
    CHECK (jsonb_typeof(attachments) = 'array');
ALTER TABLE manager_inputs DROP CONSTRAINT manager_inputs_text_check,
    ADD CONSTRAINT manager_inputs_text_check CHECK (length(text) <= 65536 AND (length(text) >= 1 OR jsonb_array_length(attachments) > 0));
ALTER TABLE manager_tasks ADD COLUMN planned_attachments jsonb NOT NULL DEFAULT '[]'
    CHECK (jsonb_typeof(planned_attachments) = 'array');

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM manager_inputs WHERE attachments <> '[]') OR EXISTS (SELECT 1 FROM manager_tasks WHERE planned_attachments <> '[]') THEN
  RAISE EXCEPTION 'cannot discard project attachment references';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE manager_tasks DROP COLUMN planned_attachments;
ALTER TABLE manager_inputs DROP CONSTRAINT manager_inputs_text_check, DROP COLUMN attachments,
    ADD CONSTRAINT manager_inputs_text_check CHECK (length(text) BETWEEN 1 AND 65536);
