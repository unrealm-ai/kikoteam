-- +goose Up
-- One direct message can contain independently authorized replies to several
-- explicitly identified requests; each request still has one final answer.
ALTER TABLE manager_human_requests DROP CONSTRAINT manager_human_requests_answer_channel_inbox_id_key;
CREATE INDEX manager_human_answer_channel_inbox ON manager_human_requests(answer_channel_inbox_id) WHERE answer_channel_inbox_id IS NOT NULL;
CREATE TABLE manager_human_dm_context (
    source_id uuid PRIMARY KEY,
    conversation_id uuid NOT NULL REFERENCES manager_conversations(id) ON DELETE CASCADE,
    principal_id uuid NOT NULL REFERENCES manager_channel_principals(id) ON DELETE CASCADE,
    connection_generation bigint NOT NULL,
    admission_epoch bigint NOT NULL,
    text text NOT NULL,
    is_system boolean NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    input_id uuid UNIQUE REFERENCES manager_inputs(id) ON DELETE CASCADE
);
CREATE INDEX manager_human_dm_context_pending ON manager_human_dm_context(conversation_id,created_at,source_id) WHERE input_id IS NULL;

CREATE TABLE manager_human_numbered_answers (
    request_id uuid NOT NULL REFERENCES manager_human_requests(id) ON DELETE CASCADE,
    question_version bigint NOT NULL,
    ordinal integer NOT NULL CHECK (ordinal BETWEEN 1 AND 10),
    body text NOT NULL CHECK (char_length(body) BETWEEN 1 AND 1000),
    source_input_id uuid REFERENCES manager_inputs(id),
    source_inbox_id uuid REFERENCES manager_channel_inbox(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (num_nonnulls(source_input_id,source_inbox_id)<=1),
    PRIMARY KEY(request_id,question_version,ordinal)
);

-- +goose Down
DROP TABLE manager_human_numbered_answers;
DROP TABLE manager_human_dm_context;
DROP INDEX manager_human_answer_channel_inbox;
ALTER TABLE manager_human_requests ADD UNIQUE(answer_channel_inbox_id);
