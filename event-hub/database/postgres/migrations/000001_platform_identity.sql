-- +goose Up
-- Platform owns these records; Event Hub owns only schema migration execution.
CREATE TABLE platform_accounts (
 id uuid PRIMARY KEY, email text NOT NULL UNIQUE CHECK(email=lower(email)), name text NOT NULL,
 password_hash text, email_verified boolean NOT NULL DEFAULT true, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE platform_email_challenges (
 id uuid PRIMARY KEY, email text NOT NULL, purpose text NOT NULL CHECK(purpose IN ('register','login','reset_password')),
 code_hash text NOT NULL, attempts integer NOT NULL DEFAULT 0 CHECK(attempts>=0),
 expires_at timestamptz NOT NULL, consumed_at timestamptz, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX platform_email_challenges_email_purpose ON platform_email_challenges(email,purpose,created_at DESC);
CREATE TABLE platform_sessions (
 token_hash text PRIMARY KEY, account_id uuid NOT NULL REFERENCES platform_accounts(id) ON DELETE CASCADE,
 expires_at timestamptz NOT NULL, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX platform_sessions_account ON platform_sessions(account_id);
CREATE TABLE platform_rate_limits(key text PRIMARY KEY,window_end timestamptz NOT NULL,count integer NOT NULL);
CREATE TABLE platform_workspaces (
 id uuid PRIMARY KEY,
 name text NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
 description text NOT NULL DEFAULT '' CHECK(length(description) <= 2000),
 version bigint NOT NULL DEFAULT 1 CHECK(version > 0),
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE platform_members (
 workspace_id uuid NOT NULL REFERENCES platform_workspaces(id),
 user_id uuid NOT NULL REFERENCES platform_accounts(id),
 role text NOT NULL CHECK(role IN ('owner','admin','member')),
 status text NOT NULL DEFAULT 'active' CHECK(status IN ('active','disabled')),
 version bigint NOT NULL DEFAULT 1 CHECK(version > 0),
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(workspace_id,user_id)
);
CREATE INDEX platform_members_user_idx ON platform_members(user_id,workspace_id) WHERE status='active';
CREATE UNIQUE INDEX platform_members_one_owner_idx ON platform_members(workspace_id) WHERE role='owner';
CREATE TABLE platform_invitations (
 id uuid PRIMARY KEY,
 workspace_id uuid NOT NULL REFERENCES platform_workspaces(id),
 inviter_id uuid NOT NULL REFERENCES platform_accounts(id),
 email text NOT NULL,
 role text NOT NULL CHECK(role IN ('admin','member')),
 token_hash text NOT NULL UNIQUE,
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','accepted','revoked')),
 expires_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX platform_invitations_workspace_idx ON platform_invitations(workspace_id,created_at DESC,id DESC);
