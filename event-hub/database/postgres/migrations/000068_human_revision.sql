-- +goose Up
ALTER TABLE manager_human_requests DROP CONSTRAINT manager_human_requests_state_check,
 ADD CONSTRAINT manager_human_requests_state_check CHECK (state IN ('preparing','pending','blocked','approved','rejected','cancelled','revised')),
 ADD CONSTRAINT manager_human_revised_requires_answer CHECK (state<>'revised' OR
  (answer IS NOT NULL AND COALESCE(answer->>'decision'='revise',false) AND resume_input_id IS NOT NULL));
CREATE TABLE manager_human_clarifications (
 request_id uuid NOT NULL REFERENCES manager_human_requests(id) ON DELETE CASCADE,
 question_version bigint NOT NULL CHECK(question_version>0),
 body_digest text NOT NULL CHECK(length(body_digest)=64),
 body text NOT NULL CHECK(char_length(body) BETWEEN 1 AND 4000),
 source_input_id uuid REFERENCES manager_inputs(id) ON DELETE SET NULL,
 source_inbox_id uuid REFERENCES manager_channel_inbox(id) ON DELETE SET NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(request_id,question_version,body_digest),
 CHECK(source_input_id IS NULL OR source_inbox_id IS NULL)
);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION manager_human_resume_allowed(input_id uuid) RETURNS boolean
    LANGUAGE sql STABLE
    AS $$
 SELECT EXISTS(SELECT 1 FROM manager_inputs i JOIN manager_human_requests r ON r.id=i.human_request_id
 JOIN manager_inputs root ON root.id=r.authority_input_id AND root.session_id=i.session_id AND root.workspace_id=i.workspace_id
 WHERE i.id=$1 AND (r.state='approved' OR (r.state='revised' AND r.answer->>'decision'='revise')) AND r.resume_input_id=i.id AND r.session_id=i.session_id
 AND i.admission_epoch=root.admission_epoch AND i.submitted_by IS NOT DISTINCT FROM root.submitted_by
 AND i.channel_principal_id IS NOT DISTINCT FROM root.channel_principal_id
 AND i.channel_generation IS NOT DISTINCT FROM root.channel_generation
 AND i.trigger_event_id IS NOT DISTINCT FROM root.trigger_event_id
 AND (manager_human_recipient_name(r.workspace_id,r.agent_id,r.recipient,false) IS NOT NULL)
 AND manager_input_actor_before_human_resume(root.id));
$$;

CREATE OR REPLACE FUNCTION manager_work_input_allowed(input_id uuid) RETURNS boolean
    LANGUAGE sql STABLE
    AS $$
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
   WHERE h.id=i.human_request_id AND h.resume_input_id=i.id AND (h.state='approved' OR (h.state='revised' AND h.answer->>'decision'='revise')) AND h.session_id=s.id
   AND original.work_assignment_id=a.id AND original.source_kind='work_item_assignment'
   AND (manager_human_recipient_name(w.workspace_id,e.id,h.recipient,false) IS NOT NULL))));
$$;

CREATE OR REPLACE FUNCTION manager_input_actor_allowed(input_id uuid) RETURNS boolean
    LANGUAGE sql STABLE
    AS $$
 SELECT CASE WHEN i.work_assignment_id IS NOT NULL THEN manager_work_input_allowed(i.id)
 WHEN i.work_submission_id IS NOT NULL THEN manager_work_result_allowed(i.id)
 WHEN i.human_request_id IS NOT NULL AND EXISTS(SELECT 1 FROM manager_human_requests h JOIN manager_inputs root ON root.id=h.authority_input_id WHERE h.id=i.human_request_id AND root.work_submission_id IS NOT NULL)
 THEN EXISTS(SELECT 1 FROM manager_human_requests h JOIN manager_inputs root ON root.id=h.authority_input_id WHERE h.id=i.human_request_id
  AND h.resume_input_id=i.id AND (h.state='approved' OR (h.state='revised' AND h.answer->>'decision'='revise')) AND h.session_id=i.session_id
  AND i.admission_epoch=root.admission_epoch AND i.submitted_by IS NOT DISTINCT FROM root.submitted_by
  AND i.channel_principal_id IS NOT DISTINCT FROM root.channel_principal_id AND i.channel_generation IS NOT DISTINCT FROM root.channel_generation
  AND i.trigger_event_id IS NOT DISTINCT FROM root.trigger_event_id
  AND (manager_human_recipient_name(i.workspace_id,h.agent_id,h.recipient,false) IS NOT NULL)
  AND manager_work_result_allowed(root.id))
 ELSE manager_input_actor_before_work_items(i.id) END
 FROM manager_inputs i WHERE i.id=$1;
$$;

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM manager_human_requests WHERE state='revised') OR EXISTS(SELECT 1 FROM manager_human_clarifications) THEN
  RAISE EXCEPTION 'Human revision rollback requires preserving recorded decisions and clarifications';
 END IF;
END $$;
CREATE OR REPLACE FUNCTION manager_human_resume_allowed(input_id uuid) RETURNS boolean
    LANGUAGE sql STABLE
    AS $$
 SELECT EXISTS(SELECT 1 FROM manager_inputs i JOIN manager_human_requests r ON r.id=i.human_request_id
 JOIN manager_inputs root ON root.id=r.authority_input_id AND root.session_id=i.session_id AND root.workspace_id=i.workspace_id
 WHERE i.id=$1 AND r.state='approved' AND r.resume_input_id=i.id AND r.session_id=i.session_id
 AND i.admission_epoch=root.admission_epoch AND i.submitted_by IS NOT DISTINCT FROM root.submitted_by
 AND i.channel_principal_id IS NOT DISTINCT FROM root.channel_principal_id
 AND i.channel_generation IS NOT DISTINCT FROM root.channel_generation
 AND i.trigger_event_id IS NOT DISTINCT FROM root.trigger_event_id
 AND (manager_human_recipient_name(r.workspace_id,r.agent_id,r.recipient,false) IS NOT NULL)
 AND manager_input_actor_before_human_resume(root.id));
$$;

CREATE OR REPLACE FUNCTION manager_work_input_allowed(input_id uuid) RETURNS boolean
    LANGUAGE sql STABLE
    AS $$
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
   AND (manager_human_recipient_name(w.workspace_id,e.id,h.recipient,false) IS NOT NULL))));
$$;

CREATE OR REPLACE FUNCTION manager_input_actor_allowed(input_id uuid) RETURNS boolean
    LANGUAGE sql STABLE
    AS $$
 SELECT CASE WHEN i.work_assignment_id IS NOT NULL THEN manager_work_input_allowed(i.id)
 WHEN i.work_submission_id IS NOT NULL THEN manager_work_result_allowed(i.id)
 WHEN i.human_request_id IS NOT NULL AND EXISTS(SELECT 1 FROM manager_human_requests h JOIN manager_inputs root ON root.id=h.authority_input_id WHERE h.id=i.human_request_id AND root.work_submission_id IS NOT NULL)
 THEN EXISTS(SELECT 1 FROM manager_human_requests h JOIN manager_inputs root ON root.id=h.authority_input_id WHERE h.id=i.human_request_id
  AND h.resume_input_id=i.id AND h.state='approved' AND h.session_id=i.session_id
  AND i.admission_epoch=root.admission_epoch AND i.submitted_by IS NOT DISTINCT FROM root.submitted_by
  AND i.channel_principal_id IS NOT DISTINCT FROM root.channel_principal_id AND i.channel_generation IS NOT DISTINCT FROM root.channel_generation
  AND i.trigger_event_id IS NOT DISTINCT FROM root.trigger_event_id
  AND (manager_human_recipient_name(i.workspace_id,h.agent_id,h.recipient,false) IS NOT NULL)
  AND manager_work_result_allowed(root.id))
 ELSE manager_input_actor_before_work_items(i.id) END
 FROM manager_inputs i WHERE i.id=$1;
$$;

-- +goose StatementEnd
DROP TABLE manager_human_clarifications;
ALTER TABLE manager_human_requests DROP CONSTRAINT manager_human_revised_requires_answer,
 DROP CONSTRAINT manager_human_requests_state_check,
 ADD CONSTRAINT manager_human_requests_state_check CHECK (state IN ('preparing','pending','blocked','approved','rejected','cancelled'));
