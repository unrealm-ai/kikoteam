-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION platform_channel_actor_access_allowed(binding uuid, policy jsonb, person text, group_chat boolean) RETURNS boolean
 LANGUAGE sql STABLE AS $$
 SELECT NOT COALESCE(policy->'deny_ids','[]'::jsonb) ? person
 AND NOT COALESCE(platform_channel_directory_department_match(binding,person,COALESCE(NULLIF(policy->'deny_department_rules','null'::jsonb),policy->'deny_department_ids')),true)
 AND ((group_chat AND COALESCE((policy->>'group_members_enabled')::boolean,false)) OR policy->>'mode' IN ('all','denylist')
 OR (policy->>'mode'='allowlist' AND (COALESCE(policy->'allow_ids','[]'::jsonb) ? person
 OR COALESCE(platform_channel_directory_department_match(binding,person,COALESCE(NULLIF(policy->'allow_department_rules','null'::jsonb),policy->'allow_department_ids')),false))));
$$;

-- Uses the same current channel identity and private-chat access as inbound
-- admission, and independently requires a directory or verified contact match.
CREATE FUNCTION manager_human_recipient_name(workspace uuid, agent uuid, recipient jsonb) RETURNS text
 LANGUAGE sql STABLE AS $$
 SELECT CASE
 WHEN NOT COALESCE(recipient @> jsonb_build_object('kind','user','workspace_id',workspace::text,'agent_id',agent::text),false) THEN NULL
 WHEN recipient->>'provider'='web' THEN (
  SELECT ac.name FROM platform_accounts ac
  WHERE ac.id=CASE WHEN pg_input_is_valid(recipient->>'external_id','uuid') THEN (recipient->>'external_id')::uuid END
  AND platform_agent_web_allowed(workspace,agent,ac.id))
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
  AND platform_channel_actor_access_allowed(b.id,b.access,recipient->>'external_id',false))
 END;
$$;

CREATE OR REPLACE FUNCTION manager_channel_actor_policy_allowed(principal uuid, conversation uuid, generation bigint) RETURNS boolean
    LANGUAGE sql STABLE
    AS $$
 SELECT EXISTS(SELECT 1 FROM manager_channel_principals p JOIN platform_channel_bindings b ON b.id=p.binding_id JOIN manager_conversations c ON c.channel_binding_id=b.id
 WHERE p.id=principal AND c.id=conversation AND p.workspace_id=c.workspace_id AND b.workspace_id=c.workspace_id AND b.agent_id=c.agent_id
 AND b.enabled AND (b.provider='feishu' OR b.identity_verified_generation=b.connection_generation) AND b.connection_generation=generation AND b.identity_epoch=p.identity_epoch AND c.channel_identity_epoch=b.identity_epoch AND c.channel_tenant_id=p.external_tenant_id AND (b.external_tenant_id<>'' AND b.external_tenant_id=p.external_tenant_id)
 AND platform_channel_actor_access_allowed(b.id,b.access,p.external_user_id,c.channel_chat_type='group')
 AND (c.channel_chat_type='direct' OR (NOT COALESCE(b.access->'deny_chat_ids','[]'::jsonb) ? c.external_chat_id AND (jsonb_array_length(COALESCE(b.access->'allow_chat_ids','[]'::jsonb))=0 OR COALESCE(b.access->'allow_chat_ids','[]'::jsonb) ? c.external_chat_id)))
 AND (c.channel_chat_type='group' OR c.channel_created_by=p.id));
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
 AND (manager_human_recipient_name(r.workspace_id,r.agent_id,r.recipient) IS NOT NULL)
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
   AND (manager_human_recipient_name(w.workspace_id,e.id,h.recipient) IS NOT NULL))));
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
  AND (manager_human_recipient_name(i.workspace_id,h.agent_id,h.recipient) IS NOT NULL)
  AND manager_work_result_allowed(root.id))
 ELSE manager_input_actor_before_work_items(i.id) END
 FROM manager_inputs i WHERE i.id=$1;
$$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Channel human confirmation rollback requires explicit migration of rules and requests'; END $$;
-- +goose StatementEnd
