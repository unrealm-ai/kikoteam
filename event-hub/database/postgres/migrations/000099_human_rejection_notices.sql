-- +goose Up
CREATE TABLE manager_human_rejection_notices (
    request_id uuid PRIMARY KEY REFERENCES manager_human_requests(id) ON DELETE CASCADE,
    request_version bigint NOT NULL CHECK (request_version > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    outcome text CHECK (outcome IN ('web_published','channel_queued','suppressed'))
);
CREATE INDEX manager_human_rejection_notices_pending
    ON manager_human_rejection_notices (created_at,request_id) WHERE outcome IS NULL;

-- +goose Down
DROP TABLE manager_human_rejection_notices;
