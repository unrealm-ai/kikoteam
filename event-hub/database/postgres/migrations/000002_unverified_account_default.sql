-- +goose Up
-- New password-only accounts must not claim proven mailbox ownership.
-- Existing verified accounts keep their current state; all creation paths explicitly set this field.
ALTER TABLE platform_accounts ALTER COLUMN email_verified SET DEFAULT false;
