-- +goose Up
CREATE TABLE manager_work_items (
 id uuid PRIMARY KEY,
 workspace_id uuid NOT NULL REFERENCES platform_workspaces(id),
 root_session_id uuid NOT NULL REFERENCES manager_sessions(id),
 root_turn_id uuid NOT NULL REFERENCES manager_turns(id),
 authority_input_id uuid NOT NULL REFERENCES manager_inputs(id),
 root_stop_revision bigint NOT NULL,
 task_space_id uuid NOT NULL UNIQUE REFERENCES manager_task_spaces(id),
 title text NOT NULL,
 goal text NOT NULL,
 criteria text NOT NULL,
 status text NOT NULL CHECK (status IN ('working','awaiting_acceptance','completed','blocked','cancelled')),
 version bigint NOT NULL DEFAULT 1 CHECK (version>0),
 current_assignment_id uuid,
 current_submission_id uuid,
 acceptance_evidence text,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX manager_work_items_active_root ON manager_work_items(root_session_id) WHERE status IN ('working','awaiting_acceptance','blocked');

CREATE TABLE manager_work_assignments (
 id uuid PRIMARY KEY,
 work_item_id uuid NOT NULL REFERENCES manager_work_items(id),
 source_agent_id uuid NOT NULL REFERENCES platform_agents(id),
 target_agent_id uuid NOT NULL REFERENCES platform_agents(id),
 session_id uuid NOT NULL UNIQUE REFERENCES manager_sessions(id),
 summary text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE manager_work_items ADD FOREIGN KEY(current_assignment_id) REFERENCES manager_work_assignments(id);

CREATE TABLE manager_work_boundaries (
 id uuid PRIMARY KEY,
 session_id uuid NOT NULL REFERENCES manager_sessions(id),
 turn_id uuid NOT NULL UNIQUE REFERENCES manager_turns(id),
 input_id uuid NOT NULL REFERENCES manager_inputs(id),
 stop_revision bigint NOT NULL,
 operation_id text NOT NULL,
 kind text NOT NULL CHECK (kind IN ('handoff','submit')),
 work_item_id uuid REFERENCES manager_work_items(id),
 payload jsonb NOT NULL,
 state text NOT NULL DEFAULT 'preparing' CHECK (state IN ('preparing','safe','committed','blocked','cancelled')),
 fenced_at timestamptz,
 error_code text,
 CHECK (state NOT IN ('safe','committed') OR fenced_at IS NOT NULL)
);
CREATE INDEX manager_work_boundaries_session ON manager_work_boundaries(session_id,state);
CREATE INDEX manager_work_boundaries_safe ON manager_work_boundaries(id) WHERE state='safe';
ALTER TABLE manager_work_items ADD FOREIGN KEY(current_submission_id) REFERENCES manager_work_boundaries(id);

ALTER TABLE manager_inputs ADD COLUMN work_assignment_id uuid REFERENCES manager_work_assignments(id);
ALTER TABLE manager_inputs ADD COLUMN work_submission_id uuid UNIQUE REFERENCES manager_work_boundaries(id);
CREATE UNIQUE INDEX manager_work_first_input ON manager_inputs(work_assignment_id) WHERE source_kind='work_item_assignment';
ALTER TABLE manager_inputs DROP CONSTRAINT manager_input_actor;
ALTER TABLE manager_inputs ADD CONSTRAINT manager_input_actor CHECK (
 (submitted_by IS NOT NULL)::integer+(channel_principal_id IS NOT NULL)::integer+
 (trigger_event_id IS NOT NULL)::integer+(notification_id IS NOT NULL)::integer+(work_assignment_id IS NOT NULL)::integer=1
 AND (channel_principal_id IS NULL)=(channel_generation IS NULL));
ALTER TABLE manager_inputs DROP CONSTRAINT manager_inputs_source_kind_check;
ALTER TABLE manager_inputs ADD CONSTRAINT manager_inputs_source_kind_check CHECK
 (source_kind IN ('user','task_report','task_result','system','human_confirmation','work_item_assignment','work_item_result'));
ALTER TABLE manager_inputs ADD CONSTRAINT manager_work_input_kind CHECK (
 (work_assignment_id IS NULL OR source_kind IN ('work_item_assignment','human_confirmation'))
 AND (source_kind<>'work_item_assignment' OR work_assignment_id IS NOT NULL)
 AND (work_submission_id IS NOT NULL)=(source_kind='work_item_result')
 AND (source_kind NOT IN ('work_item_assignment','work_item_result') OR (trigger_agent_loop AND delivery_mode='queue')));

-- +goose StatementBegin
ALTER FUNCTION manager_input_actor_allowed(uuid) RENAME TO manager_input_actor_before_work_items;
ALTER FUNCTION manager_input_admission_allowed(uuid) RENAME TO manager_input_admission_before_work_items;
CREATE FUNCTION manager_work_package_allowed(p jsonb) RETURNS boolean LANGUAGE sql IMMUTABLE AS $$
 SELECT COALESCE((p->>'enabled')::boolean,false) AND COALESCE(p->'conversation_scope','null'::jsonb)='null'
 AND p#>>'{scope,mode}'='inherit_with_blacklist'
 AND NOT EXISTS (SELECT 1 FROM unnest(ARRAY['allow_member_ids','deny_member_ids','allow','deny']) AS k
  WHERE COALESCE(p->'scope'->k,'null'::jsonb) NOT IN ('null','[]'));
$$;
CREATE FUNCTION manager_work_input_allowed(input_id uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(
 SELECT 1 FROM manager_inputs i
 JOIN manager_work_assignments a ON a.id=i.work_assignment_id AND a.session_id=i.session_id
 JOIN manager_work_items w ON w.id=a.work_item_id AND w.workspace_id=i.workspace_id
 JOIN manager_sessions s ON s.id=a.session_id AND s.workspace_id=w.workspace_id AND s.task_space_id=w.task_space_id AND s.agent_id=a.target_agent_id
 JOIN manager_task_spaces p ON p.id=w.task_space_id AND p.lifecycle='active'
 JOIN platform_agents e ON e.id=a.target_agent_id AND e.enabled
 JOIN manager_sessions root ON root.id=w.root_session_id AND root.lifecycle='active' AND root.stop_revision=w.root_stop_revision
 JOIN platform_agents ra ON ra.id=root.agent_id AND ra.enabled
 JOIN manager_inputs authority ON authority.id=w.authority_input_id AND authority.session_id=root.id AND authority.admission_epoch=ra.admission_epoch
 LEFT JOIN manager_conversations c ON c.id=root.conversation_id
 LEFT JOIN manager_task_spaces rs ON rs.id=root.task_space_id
 WHERE i.id=$1 AND i.admission_epoch=e.admission_epoch AND w.status='working' AND w.current_assignment_id=a.id
 AND COALESCE(c.lifecycle,rs.lifecycle)='active' AND manager_input_actor_before_work_items(w.authority_input_id)
 AND (i.source_kind='work_item_assignment' OR EXISTS(
   SELECT 1 FROM manager_human_requests h JOIN manager_inputs original ON original.id=h.authority_input_id
   WHERE h.id=i.human_request_id AND h.resume_input_id=i.id AND h.state='approved' AND h.session_id=s.id
   AND original.work_assignment_id=a.id AND original.source_kind='work_item_assignment'
   AND platform_agent_web_allowed(w.workspace_id,e.id,(h.recipient->>'external_id')::uuid))));
$$;
CREATE FUNCTION manager_work_result_allowed(input_id uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM manager_inputs i JOIN manager_work_boundaries b ON b.id=i.work_submission_id
 JOIN manager_work_items w ON w.id=b.work_item_id AND w.root_session_id=i.session_id
 JOIN manager_sessions s ON s.id=i.session_id
 JOIN manager_inputs original ON original.id=w.authority_input_id AND original.session_id=s.id
 WHERE i.id=$1 AND b.kind='submit' AND b.state='committed'
 AND w.current_submission_id=b.id AND w.status IN ('awaiting_acceptance','completed')
 AND s.stop_revision=w.root_stop_revision
 AND i.admission_epoch=original.admission_epoch
 AND i.submitted_by IS NOT DISTINCT FROM original.submitted_by
 AND i.channel_principal_id IS NOT DISTINCT FROM original.channel_principal_id
 AND i.channel_generation IS NOT DISTINCT FROM original.channel_generation
 AND i.trigger_event_id IS NOT DISTINCT FROM original.trigger_event_id
 AND manager_input_actor_before_work_items(original.id));
$$;
CREATE FUNCTION manager_input_actor_allowed(input_id uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT CASE WHEN i.work_assignment_id IS NOT NULL THEN manager_work_input_allowed(i.id)
 WHEN i.work_submission_id IS NOT NULL THEN manager_work_result_allowed(i.id)
 WHEN i.human_request_id IS NOT NULL AND EXISTS(SELECT 1 FROM manager_human_requests h JOIN manager_inputs root ON root.id=h.authority_input_id WHERE h.id=i.human_request_id AND root.work_submission_id IS NOT NULL)
 THEN EXISTS(SELECT 1 FROM manager_human_requests h JOIN manager_inputs root ON root.id=h.authority_input_id WHERE h.id=i.human_request_id
  AND h.resume_input_id=i.id AND h.state='approved' AND h.session_id=i.session_id
  AND i.admission_epoch=root.admission_epoch AND i.submitted_by IS NOT DISTINCT FROM root.submitted_by
  AND i.channel_principal_id IS NOT DISTINCT FROM root.channel_principal_id AND i.channel_generation IS NOT DISTINCT FROM root.channel_generation
  AND i.trigger_event_id IS NOT DISTINCT FROM root.trigger_event_id
  AND platform_agent_web_allowed(i.workspace_id,h.agent_id,(h.recipient->>'external_id')::uuid)
  AND manager_work_result_allowed(root.id))
 ELSE manager_input_actor_before_work_items(i.id) END
 FROM manager_inputs i WHERE i.id=$1;
$$;
CREATE FUNCTION manager_input_admission_allowed(input_id uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT CASE WHEN i.work_assignment_id IS NOT NULL OR i.work_submission_id IS NOT NULL OR i.human_request_id IS NOT NULL
 THEN manager_input_actor_allowed(i.id) ELSE manager_input_admission_before_work_items(i.id) END
 FROM manager_inputs i WHERE i.id=$1;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Work-item rollback requires explicit migration of assignments and resume inputs'; END $$;
-- +goose StatementEnd
