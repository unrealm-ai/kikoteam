-- +goose Up
ALTER TABLE manager_human_requests ADD COLUMN answer_channel_inbox_id uuid UNIQUE REFERENCES manager_channel_inbox(id);
ALTER TABLE manager_human_requests ADD CONSTRAINT manager_human_answer_evidence_kind
 CHECK (num_nonnulls(answer_input_id,answer_channel_inbox_id)<=1
 AND (answer_channel_inbox_id IS NULL OR answer IS NOT NULL));
ALTER TABLE manager_channel_deliveries ADD COLUMN human_request_ids uuid[] NOT NULL DEFAULT '{}';
ALTER TABLE manager_channel_deliveries ADD CONSTRAINT manager_channel_human_reply_scope
 CHECK (kind='human_confirmation' OR cardinality(human_request_ids)=0);

-- +goose StatementBegin
-- Configuring or choosing a new recipient always uses the employee-use check.
-- An already assigned request only uses the current identity check.
CREATE FUNCTION manager_human_recipient_name(workspace uuid, agent uuid, recipient jsonb, require_employee_use boolean) RETURNS text
 LANGUAGE sql STABLE AS $$
 SELECT CASE
 WHEN NOT COALESCE(recipient @> jsonb_build_object('kind','user','workspace_id',workspace::text,'agent_id',agent::text),false) THEN NULL
 WHEN recipient->>'provider'='web' THEN (
  SELECT ac.name FROM platform_accounts ac
  WHERE ac.id=CASE WHEN pg_input_is_valid(recipient->>'external_id','uuid') THEN (recipient->>'external_id')::uuid END
  AND EXISTS(SELECT 1 FROM platform_agents a JOIN platform_members m
    ON m.workspace_id=a.workspace_id AND m.user_id=ac.id
    WHERE a.workspace_id=workspace AND a.id=agent AND a.enabled AND m.status='active')
  AND (NOT require_employee_use OR platform_agent_web_allowed(workspace,agent,ac.id)))
 ELSE (
  SELECT candidate.name
  FROM platform_channel_bindings b
  JOIN platform_agents a ON a.id=b.agent_id AND a.workspace_id=b.workspace_id
  CROSS JOIN LATERAL (
   SELECT u.display_name AS name FROM platform_channel_directory_state d
   JOIN platform_channel_directory_users u ON u.binding_id=d.binding_id
   WHERE b.provider IN ('feishu','dingtalk') AND d.binding_id=b.id AND d.identity_epoch=b.identity_epoch
   AND d.connection_generation=b.connection_generation AND d.external_tenant_id=b.external_tenant_id
   AND d.full_snapshot AND d.expires_at>now() AND u.user_id=recipient->>'external_id' AND u.status IN ('active','unknown')
   UNION ALL
   SELECT COALESCE(c.display_name,m.search_user_id)
   FROM platform_wecom_identity_mappings m LEFT JOIN platform_wecom_contacts c
    ON c.binding_id=m.binding_id AND c.identity_epoch=m.identity_epoch
    AND c.connection_generation=m.connection_generation AND c.search_user_id=m.search_user_id
   WHERE b.provider='wecom' AND m.binding_id=b.id AND m.identity_epoch=b.identity_epoch
   AND m.connection_generation=b.connection_generation AND m.callback_user_id=recipient->>'external_id'
  ) candidate
  WHERE b.workspace_id=workspace AND b.agent_id=agent AND a.enabled AND b.enabled
  AND b.id::text=recipient->>'binding_id' AND b.provider=recipient->>'provider'
  AND b.external_tenant_id<>'' AND b.external_tenant_id=recipient->>'tenant_id' AND b.bot_open_id<>''
  AND (b.provider='feishu' OR b.identity_verified_generation=b.connection_generation)
  AND b.identity_epoch::text=recipient->>'identity_epoch' AND b.connection_generation::text=recipient->>'connection_generation'
  AND recipient->>'identity_namespace'=CASE WHEN b.provider='feishu' THEN 'open_id' ELSE 'user_id' END
  AND (NOT require_employee_use OR platform_channel_actor_access_allowed(b.id,b.access,recipient->>'external_id',false)))
 END;
$$;

CREATE OR REPLACE FUNCTION manager_human_recipient_name(workspace uuid, agent uuid, recipient jsonb) RETURNS text
 LANGUAGE sql STABLE AS $$
 SELECT manager_human_recipient_name(workspace,agent,recipient,true);
$$;

CREATE FUNCTION manager_human_recipient_matches(workspace uuid, agent uuid, recipient jsonb, actor uuid, channel boolean) RETURNS boolean
 LANGUAGE sql STABLE AS $$
 SELECT manager_human_recipient_name(workspace,agent,recipient,false) IS NOT NULL AND CASE
 WHEN channel THEN EXISTS(
  SELECT 1 FROM manager_channel_principals p WHERE p.id=actor AND p.workspace_id=workspace
  AND p.provider=recipient->>'provider' AND p.binding_id::text=recipient->>'binding_id'
  AND p.identity_epoch::text=recipient->>'identity_epoch' AND p.external_tenant_id=recipient->>'tenant_id'
  AND p.external_user_id=recipient->>'external_id')
 ELSE recipient->>'provider'='web' AND recipient->>'external_id'=actor::text END;
$$;

-- This validates only the authenticated main DM envelope. It grants neither
-- ordinary channel use nor access to a particular request.
CREATE FUNCTION manager_human_channel_inbox_current(inbox uuid, actor uuid) RETURNS boolean
 LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM manager_channel_inbox n
  JOIN manager_channel_principals p ON p.id=n.principal_id
  JOIN platform_channel_bindings b ON b.id=n.binding_id AND b.id=p.binding_id AND b.workspace_id=p.workspace_id
  JOIN platform_agents a ON a.id=b.agent_id AND a.workspace_id=b.workspace_id
  JOIN manager_conversations c ON c.id=n.conversation_id AND c.channel_binding_id=b.id AND c.agent_id=a.id
  WHERE n.id=inbox AND p.id=actor AND n.chat_type='direct' AND n.thread_id=''
  AND n.sender_id=p.external_user_id AND p.provider=b.provider AND n.identity_epoch=b.identity_epoch
  AND n.external_tenant_id=b.external_tenant_id AND p.external_tenant_id=b.external_tenant_id AND b.external_tenant_id<>''
  AND n.connection_generation=b.connection_generation AND p.identity_epoch=b.identity_epoch
  AND n.message_metadata->>'sender_id_type'=CASE WHEN b.provider='feishu' THEN 'open_id' ELSE 'user_id' END
  AND c.channel_chat_type='direct' AND c.channel_thread_id='' AND c.channel_created_by=p.id AND c.lifecycle='active'
  AND c.workspace_id=b.workspace_id AND c.channel_tenant_id=b.external_tenant_id
  AND c.external_chat_id=n.external_chat_id AND c.channel_identity_epoch=b.identity_epoch
  AND a.enabled AND b.enabled AND b.bot_open_id<>''
  AND (b.provider='feishu' OR b.identity_verified_generation=b.connection_generation));
$$;

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

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Restricted confirmation rollback requires explicit migration of channel answer evidence'; END $$;
-- +goose StatementEnd
