-- +goose Up
-- Existing generic/channel conversations retain their ownership semantics.
ALTER TABLE manager_conversations ADD COLUMN web_user_id uuid REFERENCES platform_accounts(id);
ALTER TABLE manager_conversations ADD CONSTRAINT manager_web_direct_owner CHECK(web_user_id IS NULL OR web_user_id=created_by);
CREATE UNIQUE INDEX manager_web_direct_unique ON manager_conversations(workspace_id,agent_id,web_user_id) WHERE web_user_id IS NOT NULL;
-- +goose Down
DROP INDEX manager_web_direct_unique;
ALTER TABLE manager_conversations DROP CONSTRAINT manager_web_direct_owner;
ALTER TABLE manager_conversations DROP COLUMN web_user_id;
