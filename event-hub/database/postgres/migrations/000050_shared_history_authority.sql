-- +goose Up
ALTER TABLE manager_group_history_queries ALTER COLUMN policy_id DROP NOT NULL;
CREATE TABLE manager_group_history_grant_sets (
 input_id uuid PRIMARY KEY REFERENCES manager_inputs(id),session_id uuid NOT NULL REFERENCES manager_sessions(id),
 engine_id uuid NOT NULL,assignment_id uuid NOT NULL,generation bigint NOT NULL,created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE manager_group_history_grants (
 id uuid PRIMARY KEY,input_id uuid NOT NULL REFERENCES manager_group_history_grant_sets(input_id),
 workspace_id uuid NOT NULL,agent_id uuid NOT NULL,session_id uuid NOT NULL,
 grant_kind text NOT NULL DEFAULT 'policy' CHECK (grant_kind IN ('policy','self')),
 policy_id uuid,policy_version bigint NOT NULL,edge_ordinal integer NOT NULL,
 source_ref jsonb NOT NULL,target_ref jsonb NOT NULL,
 CHECK ((grant_kind='policy' AND policy_id IS NOT NULL AND policy_version>0 AND edge_ordinal>=0) OR (grant_kind='self' AND policy_id IS NULL AND policy_version=0 AND edge_ordinal=-1 AND source_ref=target_ref)),
 UNIQUE(input_id,policy_id,edge_ordinal)
);
CREATE UNIQUE INDEX manager_history_self_grant_unique ON manager_group_history_grants(input_id) WHERE grant_kind='self';
CREATE TABLE manager_group_sharing_lineage (
 workspace_id uuid NOT NULL,destination_key text NOT NULL,source_key text NOT NULL,
 source_ref jsonb NOT NULL,first_read_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(workspace_id,destination_key,source_key)
);
CREATE TABLE manager_group_history_reads (
 query_id uuid PRIMARY KEY REFERENCES manager_group_history_queries(id),workspace_id uuid NOT NULL,
 session_id uuid NOT NULL,input_id uuid NOT NULL,policy_id uuid,policy_version bigint NOT NULL,
 source_ref jsonb NOT NULL,target_ref jsonb NOT NULL,created_at timestamptz NOT NULL DEFAULT now()
);
-- Scope provenance survives binding refresh and applies across employees reaching
-- the same real destination. This is a controlled-history-path restriction, not DLP.
-- +goose StatementBegin
CREATE FUNCTION manager_sharing_scope_key(ref jsonb) RETURNS text LANGUAGE sql IMMUTABLE AS $$
 SELECT jsonb_build_array(ref->>'workspace_id',ref->>'provider',CASE WHEN ref->>'provider'='wecom' AND ref->>'kind'='group' THEN '' ELSE COALESCE(ref->>'tenant_id','') END,ref->>'kind',COALESCE(ref->>'identity_namespace',''),ref->>'external_id')::text;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION manager_sharing_ref_current(ref jsonb,workspace uuid,agent uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT ref->>'workspace_id'=workspace::text AND ref->>'agent_id'=agent::text AND CASE
 WHEN ref->>'provider'='web' THEN CASE WHEN ref->>'kind'='user' THEN EXISTS(
 SELECT 1 FROM platform_members m WHERE m.workspace_id=workspace AND m.user_id::text=ref->>'external_id' AND m.status='active')
 WHEN ref->>'kind'='group' THEN EXISTS(SELECT 1 FROM platform_web_groups g JOIN platform_web_group_agents a ON a.group_id=g.id AND a.workspace_id=g.workspace_id
 WHERE g.workspace_id=workspace AND a.agent_id=agent AND g.id::text=ref->>'external_id' AND g.status='active' AND a.status='active'
 AND g.admission_epoch::text=ref->>'group_epoch' AND a.admission_epoch::text=ref->>'agent_epoch') ELSE false END
 ELSE EXISTS(SELECT 1 FROM platform_channel_bindings b WHERE b.id::text=ref->>'binding_id' AND b.workspace_id=workspace AND b.agent_id=agent
 AND b.enabled AND b.provider=ref->>'provider' AND b.external_tenant_id<>'' AND b.external_tenant_id=ref->>'tenant_id'
 AND b.identity_epoch::text=ref->>'identity_epoch' AND b.connection_generation::text=ref->>'connection_generation'
 AND (ref->>'kind'='user' OR b.config_revision::text=ref->>'config_revision') AND (b.provider='feishu' OR b.identity_verified_generation=b.connection_generation)
 AND CASE WHEN ref->>'kind'='group' THEN manager_channel_group_allowed(b.id,b.identity_epoch,b.external_tenant_id,ref->>'external_id','') AND (
 EXISTS(SELECT 1 FROM platform_channel_group_candidates x WHERE x.binding_id=b.id AND x.external_chat_id=ref->>'external_id' AND x.identity_epoch=b.identity_epoch AND x.connection_generation=b.connection_generation AND x.config_revision=b.config_revision AND x.external_tenant_id=b.external_tenant_id AND x.expires_at>now())
 OR EXISTS(SELECT 1 FROM platform_channel_observed_chats x WHERE x.binding_id=b.id AND x.identity_epoch=b.identity_epoch AND x.external_tenant_id=b.external_tenant_id AND x.external_chat_id=ref->>'external_id'))
 WHEN ref->>'kind'='user' THEN CASE WHEN b.provider='wecom' THEN EXISTS(SELECT 1 FROM platform_wecom_identity_mappings m WHERE m.binding_id=b.id AND m.identity_epoch=b.identity_epoch AND m.connection_generation=b.connection_generation AND m.callback_user_id=ref->>'external_id')
 ELSE EXISTS(SELECT 1 FROM platform_channel_directory_state d JOIN platform_channel_directory_users u ON u.binding_id=d.binding_id WHERE d.binding_id=b.id AND d.identity_epoch=b.identity_epoch AND d.connection_generation=b.connection_generation AND d.external_tenant_id=b.external_tenant_id AND d.expires_at>now() AND d.full_snapshot AND u.user_id=ref->>'external_id' AND u.status='active') END ELSE false END) END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION manager_sharing_target_matches(input uuid,ref jsonb) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM manager_inputs i JOIN manager_sessions s ON s.id=i.session_id JOIN manager_conversations c ON c.id=s.conversation_id
 LEFT JOIN manager_channel_principals p ON p.id=i.channel_principal_id
 WHERE i.id=input AND i.source_kind='user' AND s.mode='task' AND s.trigger_id IS NULL
 AND manager_sharing_ref_current(ref,s.workspace_id,s.agent_id)
 AND CASE WHEN ref->>'provider'='web' THEN c.channel_binding_id IS NULL AND CASE WHEN ref->>'kind'='group' THEN c.web_group_id::text=ref->>'external_id'
 AND c.web_group_epoch::text=ref->>'group_epoch' AND c.web_group_agent_epoch::text=ref->>'agent_epoch'
 ELSE c.web_group_id IS NULL AND c.created_by::text=ref->>'external_id' AND i.submitted_by::text=ref->>'external_id' END
 ELSE c.channel_binding_id::text=ref->>'binding_id' AND c.channel_identity_epoch::text=ref->>'identity_epoch' AND c.channel_tenant_id=ref->>'tenant_id'
 AND CASE WHEN ref->>'kind'='group' THEN c.channel_chat_type='group' AND c.external_chat_id=ref->>'external_id'
 ELSE c.channel_chat_type='direct' AND c.channel_created_by=p.id AND p.external_user_id=ref->>'external_id' END END);
$$;
-- +goose StatementEnd
-- A grant is pinned once per input and cannot be expanded by later policy edits.
-- +goose StatementBegin
CREATE FUNCTION manager_shared_history_grant_allowed(grant_id uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM manager_group_history_grants g JOIN manager_group_history_grant_sets gs ON gs.input_id=g.input_id
 JOIN manager_inputs i ON i.id=g.input_id JOIN manager_sessions s ON s.id=g.session_id AND i.session_id=s.id
 JOIN manager_turns t ON t.id=s.active_turn_id AND t.input_id=i.id
 LEFT JOIN platform_group_sharing_policies p ON p.id=g.policy_id LEFT JOIN platform_group_sharing_edges e ON e.policy_id=p.id AND e.ordinal=g.edge_ordinal
 WHERE g.id=grant_id AND g.workspace_id=s.workspace_id AND g.agent_id=s.agent_id AND gs.session_id=s.id
 AND s.engine_id=gs.engine_id AND s.assignment_id=gs.assignment_id AND s.generation=gs.generation
 AND t.assignment_id=s.assignment_id AND t.generation=s.generation AND t.status='running' AND i.status='started' AND i.turn_id=t.id
 AND s.lifecycle='active' AND s.stop_revision=s.applied_stop_revision AND manager_input_actor_allowed(i.id)
 AND ((g.grant_kind='self' AND g.policy_id IS NULL AND g.source_ref=g.target_ref AND g.source_ref->>'kind'='group') OR (g.grant_kind='policy' AND p.workspace_id=g.workspace_id AND p.agent_id=g.agent_id AND p.enabled AND p.version=g.policy_version
 AND e.source_ref=g.source_ref AND e.target_ref=g.target_ref))
 AND manager_sharing_ref_current(g.source_ref,g.workspace_id,g.agent_id) AND manager_sharing_target_matches(i.id,g.target_ref));
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION manager_shared_history_allowed(query_id uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM manager_group_history_queries q JOIN manager_group_history_grants g ON g.input_id=q.input_id AND g.session_id=q.session_id
 AND g.workspace_id=q.workspace_id AND g.agent_id=q.agent_id AND g.policy_id IS NOT DISTINCT FROM q.policy_id AND g.policy_version=q.policy_version AND g.edge_ordinal=q.edge_ordinal
 AND g.source_ref=q.source_ref AND g.target_ref=q.target_ref
 WHERE q.id=query_id AND q.expires_at>now() AND manager_shared_history_grant_allowed(g.id)
 AND (manager_sharing_scope_key(q.source_ref)=manager_sharing_scope_key(q.target_ref) OR NOT EXISTS(SELECT 1 FROM manager_group_sharing_lineage l WHERE l.workspace_id=q.workspace_id AND l.destination_key=manager_sharing_scope_key(q.source_ref)
 AND l.source_key<>manager_sharing_scope_key(q.target_ref) AND NOT EXISTS(
 SELECT 1 FROM manager_group_history_grants ancestor WHERE ancestor.input_id=q.input_id AND manager_sharing_scope_key(ancestor.source_ref)=l.source_key
 AND manager_sharing_scope_key(ancestor.target_ref)=manager_sharing_scope_key(q.target_ref) AND manager_shared_history_grant_allowed(ancestor.id)))));
$$;
-- +goose StatementEnd
-- Serialize lineage inheritance/checks within a workspace; a crash may restrict
-- too much, but never expose text before persisting its inherited restrictions.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION manager_shared_history_record_read(query_id uuid) RETURNS boolean LANGUAGE plpgsql AS $$
 DECLARE q manager_group_history_queries%ROWTYPE;
 BEGIN
 SELECT * INTO q FROM manager_group_history_queries WHERE id=query_id;
 IF NOT FOUND OR q.status<>'completed' THEN RETURN false; END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended('sharing-lineage:'||q.workspace_id::text,0));
 IF NOT manager_shared_history_allowed(q.id) THEN RETURN false; END IF;
 INSERT INTO manager_group_sharing_lineage(workspace_id,destination_key,source_key,source_ref)
 VALUES(q.workspace_id,manager_sharing_scope_key(q.target_ref),manager_sharing_scope_key(q.source_ref),q.source_ref) ON CONFLICT DO NOTHING;
 INSERT INTO manager_group_sharing_lineage(workspace_id,destination_key,source_key,source_ref)
 SELECT q.workspace_id,manager_sharing_scope_key(q.target_ref),l.source_key,l.source_ref FROM manager_group_sharing_lineage l
 WHERE l.workspace_id=q.workspace_id AND l.destination_key=manager_sharing_scope_key(q.source_ref) ON CONFLICT DO NOTHING;
 INSERT INTO manager_group_history_reads(query_id,workspace_id,session_id,input_id,policy_id,policy_version,source_ref,target_ref)
 VALUES(q.id,q.workspace_id,q.session_id,q.input_id,q.policy_id,q.policy_version,q.source_ref,q.target_ref) ON CONFLICT DO NOTHING;
 RETURN true;
 END;
$$;
-- +goose StatementEnd
-- +goose Down
DROP FUNCTION manager_shared_history_grant_allowed(uuid) CASCADE;
DROP FUNCTION manager_sharing_target_matches(uuid,jsonb);
DROP FUNCTION manager_sharing_ref_current(jsonb,uuid,uuid);
DROP FUNCTION manager_sharing_scope_key(jsonb) CASCADE;
DROP TABLE manager_group_history_reads,manager_group_sharing_lineage,manager_group_history_grants,manager_group_history_grant_sets;
