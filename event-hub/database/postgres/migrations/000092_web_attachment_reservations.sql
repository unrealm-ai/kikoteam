-- +goose Up
CREATE TABLE manager_web_attachments (
 id uuid PRIMARY KEY,
 workspace_id uuid NOT NULL REFERENCES platform_workspaces(id),
 conversation_id uuid NOT NULL REFERENCES manager_conversations(id),
 actor_id uuid NOT NULL REFERENCES platform_accounts(id),
 request_key text NOT NULL CHECK(length(request_key) BETWEEN 1 AND 128),
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 255),
 size bigint NOT NULL CHECK(size BETWEEN 0 AND 33554432),
 status text NOT NULL DEFAULT 'downloading' CHECK(status IN ('downloading','ready','failed')),
 path text NOT NULL DEFAULT '',
 upload_token uuid,
 expires_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 CHECK ((status='ready') = (path<>'')),
 CHECK (status='downloading' OR upload_token IS NULL),
 UNIQUE(workspace_id,conversation_id,actor_id,request_key)
);
CREATE INDEX manager_web_attachment_pending_idx ON manager_web_attachments(workspace_id,conversation_id,actor_id,expires_at) WHERE status='downloading';
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT FROM manager_web_attachments) THEN
 RAISE EXCEPTION 'cannot discard Web attachment reservations'; END IF;
END $$;
-- +goose StatementEnd
DROP TABLE manager_web_attachments;
