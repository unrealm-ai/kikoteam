-- +goose Up
CREATE TABLE manager_trigger_notification_targets (
 task_id uuid NOT NULL REFERENCES manager_tasks(id), trigger_id uuid NOT NULL REFERENCES manager_triggers(id),
 trigger_version bigint NOT NULL, conversation_id uuid NOT NULL REFERENCES manager_conversations(id),
 target jsonb NOT NULL, PRIMARY KEY(task_id,conversation_id)
);
CREATE TABLE manager_trigger_notifications (
 id uuid PRIMARY KEY, task_id uuid NOT NULL REFERENCES manager_tasks(id), source_input_id uuid NOT NULL REFERENCES manager_inputs(id),
 source_turn_id uuid REFERENCES manager_turns(id), conversation_id uuid NOT NULL REFERENCES manager_conversations(id),
 trigger_id uuid NOT NULL REFERENCES manager_triggers(id), trigger_version bigint NOT NULL,
 target jsonb NOT NULL, text text NOT NULL, result_status text NOT NULL,
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','leased','sending','accepted','sent','failed','unknown','cancelled')),
 context_status text NOT NULL DEFAULT 'pending' CHECK(context_status IN ('pending','admitted','unavailable','suppressed')),
 context_error text, context_input_id uuid REFERENCES manager_inputs(id),
 lease_token uuid, lease_until timestamptz, attempt_target_digest text,
 provider_message_id text, provider_request_id text, provider_session_id text,
 provider_created_at_ms bigint, provider_chat_id text, provider_thread_id text,
 authorized_at timestamptz, confirmed_at timestamptz, error_code text,
 query_token uuid, query_until timestamptz, next_query_at timestamptz, query_attempts integer NOT NULL DEFAULT 0,
 web_message_id uuid REFERENCES manager_web_group_messages(id),
 created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(source_input_id,conversation_id)
);
CREATE TABLE manager_trigger_notification_receipts (
 notification_id uuid NOT NULL REFERENCES manager_trigger_notifications(id),attempt_token uuid NOT NULL,
 receipt_digest text NOT NULL,receipt jsonb NOT NULL,authorization_current boolean NOT NULL,created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(notification_id,attempt_token,receipt_digest)
);
CREATE INDEX manager_trigger_notifications_pending ON manager_trigger_notifications(status,created_at,id);
-- +goose Down
DROP TABLE manager_trigger_notification_receipts,manager_trigger_notifications,manager_trigger_notification_targets;
