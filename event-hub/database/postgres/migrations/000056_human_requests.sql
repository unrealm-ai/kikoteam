-- +goose Up
CREATE TABLE platform_agent_human_recipients (
  agent_id uuid PRIMARY KEY REFERENCES platform_agents(id),
  version bigint NOT NULL CHECK (version > 0),
  recipients jsonb NOT NULL CHECK (jsonb_typeof(recipients) = 'array')
);

CREATE TABLE manager_human_requests (
  id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL REFERENCES platform_workspaces(id),
  agent_id uuid NOT NULL REFERENCES platform_agents(id),
  session_id uuid NOT NULL REFERENCES manager_sessions(id),
  turn_id uuid NOT NULL UNIQUE REFERENCES manager_turns(id),
  input_id uuid NOT NULL REFERENCES manager_inputs(id),
  authority_input_id uuid NOT NULL REFERENCES manager_inputs(id),
  recipient jsonb NOT NULL,
  rules_version bigint NOT NULL CHECK (rules_version > 0),
  version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
  question jsonb NOT NULL,
  stop_revision bigint NOT NULL,
  fenced_at timestamptz,
  state text NOT NULL DEFAULT 'preparing' CHECK (state IN ('preparing','pending','blocked','approved','rejected','cancelled')),
  answer jsonb,
  answer_input_id uuid REFERENCES manager_inputs(id),
  resume_input_id uuid UNIQUE REFERENCES manager_inputs(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  answered_at timestamptz,
  CHECK ((answer IS NULL) = (answered_at IS NULL)),
  CHECK (state <> 'pending' OR fenced_at IS NOT NULL)
);
CREATE UNIQUE INDEX manager_human_requests_active ON manager_human_requests(session_id)
 WHERE state IN ('preparing','pending','blocked');
CREATE INDEX manager_human_requests_recipient ON manager_human_requests(workspace_id,
 (recipient->>'provider'),(recipient->>'external_id'),created_at DESC,id DESC)
 WHERE state='pending';

ALTER TABLE manager_inputs ADD COLUMN human_request_id uuid UNIQUE REFERENCES manager_human_requests(id);
ALTER TABLE manager_inputs ADD CONSTRAINT manager_human_resume_input CHECK
 (human_request_id IS NULL OR (source_kind='human_confirmation' AND trigger_agent_loop AND delivery_mode='queue' AND confirmation_request_id IS NULL));

-- +goose StatementBegin
ALTER FUNCTION manager_input_actor_allowed(uuid) RENAME TO manager_input_actor_before_human_resume;
ALTER FUNCTION manager_input_admission_allowed(uuid) RENAME TO manager_input_admission_before_human_resume;
CREATE FUNCTION manager_human_resume_allowed(input_id uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM manager_inputs i JOIN manager_human_requests r ON r.id=i.human_request_id
 JOIN manager_inputs root ON root.id=r.authority_input_id AND root.session_id=i.session_id AND root.workspace_id=i.workspace_id
 WHERE i.id=$1 AND r.state='approved' AND r.resume_input_id=i.id AND r.session_id=i.session_id
 AND i.admission_epoch=root.admission_epoch AND i.submitted_by IS NOT DISTINCT FROM root.submitted_by
 AND i.channel_principal_id IS NOT DISTINCT FROM root.channel_principal_id
 AND i.channel_generation IS NOT DISTINCT FROM root.channel_generation
 AND i.trigger_event_id IS NOT DISTINCT FROM root.trigger_event_id
 AND platform_agent_web_allowed(r.workspace_id,r.agent_id,(r.recipient->>'external_id')::uuid)
 AND manager_input_actor_before_human_resume(root.id));
$$;
CREATE FUNCTION manager_input_actor_allowed(input_id uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT CASE WHEN human_request_id IS NULL THEN manager_input_actor_before_human_resume(id) ELSE manager_human_resume_allowed(id) END
 FROM manager_inputs WHERE id=$1;
$$;
CREATE FUNCTION manager_input_admission_allowed(input_id uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT CASE WHEN human_request_id IS NULL THEN manager_input_admission_before_human_resume(id) ELSE manager_human_resume_allowed(id) END
 FROM manager_inputs WHERE id=$1;
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION manager_input_admission_allowed(uuid);
DROP FUNCTION manager_input_actor_allowed(uuid);
DROP FUNCTION manager_human_resume_allowed(uuid);
ALTER FUNCTION manager_input_admission_before_human_resume(uuid) RENAME TO manager_input_admission_allowed;
ALTER FUNCTION manager_input_actor_before_human_resume(uuid) RENAME TO manager_input_actor_allowed;
ALTER TABLE manager_inputs DROP COLUMN human_request_id;
DROP TABLE manager_human_requests;
DROP TABLE platform_agent_human_recipients;
