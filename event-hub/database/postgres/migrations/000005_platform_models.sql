-- +goose Up
-- Platform owns profiles, immutable versions and short-lived test receipts.
-- Versions reference the stable profile ID, never retain a historical secret.
CREATE TABLE platform_model_profiles (
 id uuid PRIMARY KEY,
 workspace_id uuid NOT NULL REFERENCES platform_workspaces(id),
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 100),
 description text NOT NULL DEFAULT '' CHECK(length(description)<=2000),
 protocol text NOT NULL CHECK(protocol IN ('chat_completions','openai_responses','anthropic_messages')),
 base_url text NOT NULL CHECK(length(base_url) BETWEEN 1 AND 2048),
 model_id text NOT NULL CHECK(length(model_id) BETWEEN 1 AND 256),
 version bigint NOT NULL CHECK(version>0),
 has_key boolean NOT NULL,
 validated boolean NOT NULL,
 credential bytea NOT NULL,
 credential_revision bigint NOT NULL CHECK(credential_revision>0),
 vision_status text NOT NULL CHECK(vision_status IN ('passed','unsupported','unknown')),
 enabled boolean NOT NULL DEFAULT true,
 is_default boolean NOT NULL DEFAULT false,
 tested_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(workspace_id,id),
 CHECK(NOT is_default OR enabled),
 CHECK(NOT enabled OR validated)
);
CREATE INDEX platform_model_profiles_workspace_idx ON platform_model_profiles(workspace_id,created_at DESC,id DESC);
CREATE UNIQUE INDEX platform_model_profiles_default_idx ON platform_model_profiles(workspace_id) WHERE is_default;
CREATE TABLE platform_model_profile_versions (
 workspace_id uuid NOT NULL,
 profile_id uuid NOT NULL,
 version bigint NOT NULL CHECK(version>0),
 name text NOT NULL,
 description text NOT NULL,
 protocol text NOT NULL CHECK(protocol IN ('chat_completions','openai_responses','anthropic_messages')),
 base_url text NOT NULL,
 model_id text NOT NULL,
 vision_status text NOT NULL CHECK(vision_status IN ('passed','unsupported','unknown')),
 enabled boolean NOT NULL,
 tested_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(profile_id,version),
 FOREIGN KEY(workspace_id,profile_id) REFERENCES platform_model_profiles(workspace_id,id)
);
CREATE TABLE platform_model_test_receipts (
 workspace_id uuid NOT NULL REFERENCES platform_workspaces(id),
 token_hash text NOT NULL CHECK(length(token_hash)=64),
 actor_id uuid NOT NULL REFERENCES platform_accounts(id),
 profile_id uuid,
 profile_version bigint NOT NULL CHECK(profile_version>=0),
 fingerprint text NOT NULL CHECK(length(fingerprint)=64),
 tested_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL,
 result jsonb NOT NULL,
 used_profile_id uuid,
 PRIMARY KEY(workspace_id,token_hash),
 FOREIGN KEY(workspace_id,profile_id) REFERENCES platform_model_profiles(workspace_id,id),
 FOREIGN KEY(workspace_id,used_profile_id) REFERENCES platform_model_profiles(workspace_id,id),
 CHECK((profile_id IS NULL AND profile_version=0) OR (profile_id IS NOT NULL AND profile_version>0)),
 CHECK(expires_at>tested_at)
);
CREATE INDEX platform_model_test_receipts_expiry_idx ON platform_model_test_receipts(expires_at);
-- Default selection is a workspace preference, not versioned model content.

-- At most one model verification (its text+image requests run together) per actor
-- and workspace across Backend instances. Expired leases are replaceable; claim
-- identities fence delayed cleanup from an older attempt.
CREATE TABLE platform_model_probe_leases (
 workspace_id uuid NOT NULL REFERENCES platform_workspaces(id),
 actor_id uuid NOT NULL REFERENCES platform_accounts(id),
 claim_id uuid NOT NULL,
 expires_at timestamptz NOT NULL,
 PRIMARY KEY(workspace_id,actor_id)
);
CREATE INDEX platform_model_probe_leases_expiry_idx ON platform_model_probe_leases(expires_at);
