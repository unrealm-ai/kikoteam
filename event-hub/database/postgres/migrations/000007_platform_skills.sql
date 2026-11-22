-- +goose Up
-- Platform owns immutable Skill assets; files are published before a version becomes visible.
CREATE TABLE platform_skills (
 id uuid PRIMARY KEY,
 workspace_id uuid NOT NULL REFERENCES platform_workspaces(id),
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 100),
 description text NOT NULL,
 enabled boolean NOT NULL DEFAULT true,
 version bigint NOT NULL CHECK(version>0),
 current_version_id uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(workspace_id,id)
);
CREATE INDEX platform_skills_list_idx ON platform_skills(workspace_id,created_at DESC,id DESC);
CREATE TABLE platform_skill_versions (
 id uuid PRIMARY KEY,
 workspace_id uuid NOT NULL,
 skill_id uuid NOT NULL,
 sequence bigint NOT NULL CHECK(sequence>0),
 version_label text NOT NULL DEFAULT '' CHECK(length(version_label)<=100),
 technical_name text NOT NULL,
 description text NOT NULL,
 manifest jsonb NOT NULL,
 source jsonb NOT NULL,
 storage_identity text NOT NULL,
 key_prefix text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(workspace_id,skill_id,id),
 UNIQUE(workspace_id,skill_id,sequence),
 FOREIGN KEY(workspace_id,skill_id) REFERENCES platform_skills(workspace_id,id)
);
ALTER TABLE platform_skills ADD CONSTRAINT platform_skills_current_version_fk FOREIGN KEY(workspace_id,id,current_version_id) REFERENCES platform_skill_versions(workspace_id,skill_id,id) DEFERRABLE INITIALLY DEFERRED;
CREATE TABLE platform_skill_imports (
 id uuid PRIMARY KEY,
 workspace_id uuid NOT NULL REFERENCES platform_workspaces(id),
 actor_id uuid NOT NULL REFERENCES platform_accounts(id),
 technical_name text NOT NULL,
 description text NOT NULL,
 manifest jsonb NOT NULL,
 source jsonb NOT NULL,
 storage_identity text NOT NULL,
 key_prefix text NOT NULL,
 cleaned boolean NOT NULL DEFAULT false,
 ready boolean NOT NULL DEFAULT false,
 expires_at timestamptz NOT NULL,
 published_skill_id uuid,
 published_version_id uuid,
 publish_fingerprint text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(),
 CHECK((published_skill_id IS NULL)=(published_version_id IS NULL)),
 FOREIGN KEY(workspace_id,published_skill_id,published_version_id) REFERENCES platform_skill_versions(workspace_id,skill_id,id)
);
CREATE INDEX platform_skill_imports_owner_idx ON platform_skill_imports(workspace_id,actor_id,expires_at);
-- +goose Down
DROP TABLE platform_skill_imports;
ALTER TABLE platform_skills DROP CONSTRAINT platform_skills_current_version_fk;
DROP TABLE platform_skill_versions;
DROP TABLE platform_skills;
