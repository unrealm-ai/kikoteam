-- +goose Up
CREATE TABLE manager_human_response_operations (
    input_id uuid NOT NULL REFERENCES manager_inputs(id) ON DELETE CASCADE,
    operation_id text NOT NULL CHECK (operation_id ~ '^[A-Za-z0-9_-]{1,64}$'),
    request_id uuid NOT NULL REFERENCES manager_human_requests(id) ON DELETE CASCADE,
    proposal_digest bytea NOT NULL CHECK (octet_length(proposal_digest) = 32),
    receipt jsonb NOT NULL CHECK (jsonb_typeof(receipt) = 'object'),
    PRIMARY KEY (input_id, operation_id)
);

CREATE INDEX manager_human_response_operations_request
    ON manager_human_response_operations (request_id);

ALTER TABLE manager_human_requests ADD COLUMN continuation_context jsonb;

-- +goose StatementBegin
CREATE FUNCTION manager_human_continuation_context(r manager_human_requests, s manager_sessions, decision jsonb)
RETURNS jsonb LANGUAGE sql STABLE AS $$
 SELECT jsonb_build_object(
     'requestId', r.id, 'version', r.version::text,
     'questionDigest', encode(sha256(convert_to(r.question::text, 'UTF8')), 'hex'),
     'question', r.question, 'answer', decision,
     'source', jsonb_build_object('sessionId', s.id, 'turnId', r.turn_id,
         'inputId', r.input_id, 'authorityInputId', r.authority_input_id,
         'stopRevision', s.stop_revision, 'nativeSessionId', s.native_session_id,
         'configuration', s.configuration));
$$;
-- +goose StatementEnd
