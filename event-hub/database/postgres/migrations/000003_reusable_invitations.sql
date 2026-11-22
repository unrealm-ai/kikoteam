-- +goose Up
ALTER TABLE platform_workspaces ADD COLUMN personal_owner_id uuid UNIQUE REFERENCES platform_accounts(id);
ALTER TABLE platform_invitations ADD COLUMN kind text NOT NULL DEFAULT 'email' CHECK(kind IN ('email','link'));
ALTER TABLE platform_invitations ALTER COLUMN email DROP NOT NULL;
ALTER TABLE platform_invitations ADD CONSTRAINT platform_invitation_recipient CHECK((kind='email' AND email IS NOT NULL) OR (kind='link' AND email IS NULL AND role='member'));
CREATE TABLE platform_invitation_redemptions (
 invitation_id uuid NOT NULL REFERENCES platform_invitations(id),
 account_id uuid NOT NULL REFERENCES platform_accounts(id),
 redeemed_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(invitation_id,account_id)
);
CREATE TABLE platform_removed_members (
 workspace_id uuid NOT NULL REFERENCES platform_workspaces(id),
 account_id uuid NOT NULL REFERENCES platform_accounts(id),
 removed_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(workspace_id,account_id)
);
