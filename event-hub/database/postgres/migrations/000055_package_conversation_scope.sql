-- +goose Up
-- NULL keeps legacy package and historical snapshot authorization exact.
ALTER TABLE platform_capability_packages ADD COLUMN conversation_scope jsonb
 CHECK (conversation_scope IS NULL OR jsonb_typeof(conversation_scope)='object');
-- +goose Down
-- The old evaluator ignores scene restrictions (including disabled trigger
-- scenes inside retained snapshots); it cannot safely interpret newer writes.
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'package scene authorization rollback requires explicit policy and snapshot migration'; END $$;
-- +goose StatementEnd
