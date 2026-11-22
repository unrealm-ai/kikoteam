-- +goose Up
CREATE TABLE manager_human_recovery_audit (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 request_id uuid NOT NULL REFERENCES manager_human_requests(id),
 actor_id uuid NOT NULL REFERENCES platform_accounts(id),
 action text NOT NULL CHECK(action IN ('retry','reassign')),
 reason text NOT NULL CHECK(char_length(reason) BETWEEN 1 AND 1000),
 previous_version bigint NOT NULL,
 next_version bigint NOT NULL CHECK(next_version=previous_version+1),
 previous_recipient jsonb NOT NULL,
 next_recipient jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(request_id,next_version)
);
-- +goose Down
DROP TABLE manager_human_recovery_audit;
