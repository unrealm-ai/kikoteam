-- +goose Up
ALTER TABLE manager_channel_inbox
    ADD COLUMN reaction_ready_at timestamptz,
    ADD COLUMN reaction_claim_token uuid;
CREATE INDEX manager_channel_reaction_ready_idx ON manager_channel_inbox(reaction_ready_at, id)
    WHERE reaction_ready_at IS NOT NULL AND reaction_claim_token IS NULL;

-- +goose Down
DROP INDEX manager_channel_reaction_ready_idx;
ALTER TABLE manager_channel_inbox DROP COLUMN reaction_ready_at, DROP COLUMN reaction_claim_token;
