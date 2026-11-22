-- +goose Up
ALTER TABLE manager_work_items ADD COLUMN requirements_version bigint NOT NULL DEFAULT 1 CHECK(requirements_version>0);
ALTER TABLE manager_work_boundaries ADD COLUMN requirements_version bigint NOT NULL DEFAULT 1 CHECK(requirements_version>0);

CREATE TABLE manager_work_amendments (
 work_item_id uuid NOT NULL REFERENCES manager_work_items(id),
 requirements_version bigint NOT NULL CHECK(requirements_version>1),
 request_key text NOT NULL,
 source_input_id uuid NOT NULL REFERENCES manager_inputs(id),
 input_id uuid NOT NULL UNIQUE REFERENCES manager_inputs(id),
 content text NOT NULL CHECK(length(content) BETWEEN 1 AND 4000),
 PRIMARY KEY(work_item_id,requirements_version),
 UNIQUE(work_item_id,request_key)
);

ALTER TABLE manager_inputs DROP CONSTRAINT manager_inputs_source_kind_check;
ALTER TABLE manager_inputs ADD CONSTRAINT manager_inputs_source_kind_check CHECK
 (source_kind IN ('user','task_report','task_result','system','human_confirmation','work_item_assignment','work_item_result','work_item_amendment'));
ALTER TABLE manager_inputs DROP CONSTRAINT manager_work_input_kind;
ALTER TABLE manager_inputs ADD CONSTRAINT manager_work_input_kind CHECK (
 (work_assignment_id IS NULL OR source_kind IN ('work_item_assignment','work_item_amendment','human_confirmation'))
 AND (source_kind NOT IN ('work_item_assignment','work_item_amendment') OR work_assignment_id IS NOT NULL)
 AND (work_submission_id IS NOT NULL)=(source_kind='work_item_result')
 AND (source_kind NOT IN ('work_item_assignment','work_item_amendment','work_item_result') OR (trigger_agent_loop AND delivery_mode='queue')));

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION manager_work_input_before_policy(input_id uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
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
 AND (i.source_kind IN ('work_item_assignment','work_item_amendment') OR EXISTS(
   SELECT 1 FROM manager_human_requests h JOIN manager_inputs original ON original.id=h.authority_input_id
   WHERE h.id=i.human_request_id AND h.resume_input_id=i.id AND (h.state='approved' OR (h.state='revised' AND h.answer->>'decision'='revise')) AND h.session_id=s.id
   AND original.work_assignment_id=a.id AND original.source_kind IN ('work_item_assignment','work_item_amendment')
   AND (manager_human_recipient_name(w.workspace_id,e.id,h.recipient,false) IS NOT NULL))));
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Work-item amendment rollback requires explicit migration of revised requirements and inputs'; END $$;
-- +goose StatementEnd
