-- +goose Up
-- Each recipient keeps its existing redemption, delivery lease and retry identity.
ALTER TABLE platform_invitations ADD COLUMN shared_token_hash text
    CHECK (shared_token_hash IS NULL OR (kind = 'email' AND length(shared_token_hash) = 64));
CREATE UNIQUE INDEX platform_invitation_shared_recipient
    ON platform_invitations(shared_token_hash, email) WHERE shared_token_hash IS NOT NULL;

-- +goose Down
-- Refuse to discard links that recipients can still use.
ALTER TABLE platform_invitations ADD CONSTRAINT no_shared_invitations CHECK (shared_token_hash IS NULL);
ALTER TABLE platform_invitations DROP COLUMN shared_token_hash;
