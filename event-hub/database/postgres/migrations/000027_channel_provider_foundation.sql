-- +goose Up
ALTER TABLE platform_channel_bindings DROP CONSTRAINT platform_channel_bindings_provider_check;
ALTER TABLE platform_channel_bindings ADD CONSTRAINT platform_channel_bindings_provider_check CHECK(provider IN ('feishu','dingtalk','wecom'));
ALTER TABLE platform_channel_bindings ADD COLUMN identity_verified_generation bigint NOT NULL DEFAULT 0;
UPDATE platform_channel_bindings SET identity_verified_generation=connection_generation WHERE provider='feishu' AND external_tenant_id<>'' AND bot_open_id<>'';
ALTER TABLE manager_channel_inbox ADD COLUMN provider_directed boolean NOT NULL DEFAULT false;
CREATE TABLE platform_channel_observed_chats (
 binding_id uuid NOT NULL REFERENCES platform_channel_bindings(id), identity_epoch bigint NOT NULL,
 external_tenant_id text NOT NULL, external_chat_id text NOT NULL, display_name text NOT NULL DEFAULT '',
 observed_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(binding_id,identity_epoch,external_tenant_id,external_chat_id)
);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION platform_channel_directory_department_match(binding uuid, person text, departments jsonb) RETURNS boolean LANGUAGE plpgsql STABLE AS $$
DECLARE member_departments jsonb; rule jsonb; target text; recursive boolean; start_id text; next_id text; parent text; visited text[]; uncertain boolean := false; steps integer := 0;
BEGIN
 IF jsonb_array_length(COALESCE(departments,'[]'::jsonb))=0 THEN RETURN false; END IF;
 SELECT u.department_ids INTO member_departments FROM platform_channel_directory_users u
 JOIN platform_channel_directory_state d ON d.binding_id=u.binding_id JOIN platform_channel_bindings b ON b.id=d.binding_id
 WHERE u.binding_id=binding AND u.user_id=person AND u.memberships_known AND u.status<>'inactive'
 AND d.full_snapshot AND d.expires_at>now() AND b.enabled AND b.identity_epoch=d.identity_epoch
 AND b.connection_generation=d.connection_generation AND (b.provider='feishu' OR b.identity_verified_generation=b.connection_generation) AND b.external_tenant_id<>'' AND b.external_tenant_id=d.external_tenant_id;
 IF NOT FOUND THEN RETURN NULL; END IF;
 -- Resolve all direct evidence before spending the bounded ancestor budget.
 FOR rule IN SELECT value FROM jsonb_array_elements(departments) LOOP
  target := CASE WHEN jsonb_typeof(rule)='string' THEN rule#>>'{}' ELSE rule->>'department_id' END;
  IF member_departments ? target THEN RETURN true; END IF;
 END LOOP;
 FOR rule IN SELECT value FROM jsonb_array_elements(departments) LOOP
  target := CASE WHEN jsonb_typeof(rule)='string' THEN rule#>>'{}' ELSE rule->>'department_id' END;
  recursive := COALESCE((rule->>'include_descendants')::boolean,false);
  IF target IS NULL OR target='' THEN uncertain:=true; CONTINUE; END IF;
  IF member_departments ? target THEN RETURN true; END IF;
  IF NOT recursive THEN CONTINUE; END IF;
  FOR start_id IN SELECT jsonb_array_elements_text(member_departments) LOOP
   next_id:=start_id; visited:=ARRAY[]::text[];
   WHILE next_id<>'' LOOP
    IF next_id=ANY(visited) OR cardinality(visited)>=100 THEN uncertain:=true; EXIT; END IF;
    IF next_id=target THEN RETURN true; END IF;
    visited:=array_append(visited,next_id);
    steps:=steps+1;
    IF steps>10000 THEN RETURN NULL; END IF;
    SELECT parent_id INTO parent FROM platform_channel_directory_departments WHERE binding_id=binding AND department_id=next_id;
    IF NOT FOUND THEN uncertain:=true; EXIT; END IF;
    next_id:=parent;
   END LOOP;
  END LOOP;
 END LOOP;
 IF uncertain THEN RETURN NULL; END IF;
 RETURN false;
END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION manager_channel_actor_policy_allowed(principal uuid, conversation uuid, generation bigint) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM manager_channel_principals p JOIN platform_channel_bindings b ON b.id=p.binding_id JOIN manager_conversations c ON c.channel_binding_id=b.id
 WHERE p.id=principal AND c.id=conversation AND p.workspace_id=c.workspace_id AND b.workspace_id=c.workspace_id AND b.agent_id=c.agent_id
 AND b.enabled AND (b.provider='feishu' OR b.identity_verified_generation=b.connection_generation) AND b.connection_generation=generation AND b.identity_epoch=p.identity_epoch AND c.channel_identity_epoch=b.identity_epoch AND c.channel_tenant_id=p.external_tenant_id AND (b.external_tenant_id<>'' AND b.external_tenant_id=p.external_tenant_id)
 AND NOT COALESCE(b.access->'deny_ids','[]'::jsonb) ? p.external_user_id
 AND NOT COALESCE(platform_channel_directory_department_match(b.id,p.external_user_id,COALESCE(NULLIF(b.access->'deny_department_rules','null'::jsonb),b.access->'deny_department_ids')),true)
 AND ((c.channel_chat_type='group' AND COALESCE((b.access->>'group_members_enabled')::boolean,false)) OR b.access->>'mode' IN ('all','denylist') OR (b.access->>'mode'='allowlist' AND (COALESCE(b.access->'allow_ids','[]'::jsonb) ? p.external_user_id OR COALESCE(platform_channel_directory_department_match(b.id,p.external_user_id,COALESCE(NULLIF(b.access->'allow_department_rules','null'::jsonb),b.access->'allow_department_ids')),false))))
 AND (c.channel_chat_type='direct' OR (NOT COALESCE(b.access->'deny_chat_ids','[]'::jsonb) ? c.external_chat_id AND (jsonb_array_length(COALESCE(b.access->'allow_chat_ids','[]'::jsonb))=0 OR COALESCE(b.access->'allow_chat_ids','[]'::jsonb) ? c.external_chat_id)))
 AND (c.channel_chat_type='group' OR c.channel_created_by=p.id));
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION platform_channel_directory_display_name(binding uuid, epoch bigint, tenant text, person text) RETURNS text LANGUAGE sql STABLE AS $$
 SELECT u.display_name FROM platform_channel_directory_users u
 JOIN platform_channel_directory_state d ON d.binding_id=u.binding_id
 JOIN platform_channel_bindings b ON b.id=d.binding_id
 WHERE u.binding_id=binding AND u.user_id=person AND u.status='active' AND d.full_snapshot AND d.expires_at>now()
 AND b.enabled AND (b.provider='feishu' OR b.identity_verified_generation=b.connection_generation) AND b.identity_epoch=epoch AND d.identity_epoch=b.identity_epoch
 AND b.connection_generation=d.connection_generation
 AND b.external_tenant_id=tenant AND d.external_tenant_id=tenant;
$$;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION platform_channel_directory_department_match(binding uuid, person text, departments jsonb) RETURNS boolean LANGUAGE plpgsql STABLE AS $$
DECLARE member_departments jsonb; rule jsonb; target text; recursive boolean; start_id text; next_id text; parent text; visited text[]; uncertain boolean := false; steps integer := 0;
BEGIN
 IF jsonb_array_length(COALESCE(departments,'[]'::jsonb))=0 THEN RETURN false; END IF;
 SELECT u.department_ids INTO member_departments FROM platform_channel_directory_users u
 JOIN platform_channel_directory_state d ON d.binding_id=u.binding_id JOIN platform_channel_bindings b ON b.id=d.binding_id
 WHERE u.binding_id=binding AND u.user_id=person AND u.memberships_known AND u.status<>'inactive'
 AND d.full_snapshot AND d.expires_at>now() AND b.enabled AND b.identity_epoch=d.identity_epoch
 AND b.connection_generation=d.connection_generation AND b.external_tenant_id<>'' AND b.external_tenant_id=d.external_tenant_id;
 IF NOT FOUND THEN RETURN NULL; END IF;
 -- Resolve all direct evidence before spending the bounded ancestor budget.
 FOR rule IN SELECT value FROM jsonb_array_elements(departments) LOOP
  target := CASE WHEN jsonb_typeof(rule)='string' THEN rule#>>'{}' ELSE rule->>'department_id' END;
  IF member_departments ? target THEN RETURN true; END IF;
 END LOOP;
 FOR rule IN SELECT value FROM jsonb_array_elements(departments) LOOP
  target := CASE WHEN jsonb_typeof(rule)='string' THEN rule#>>'{}' ELSE rule->>'department_id' END;
  recursive := COALESCE((rule->>'include_descendants')::boolean,false);
  IF target IS NULL OR target='' THEN uncertain:=true; CONTINUE; END IF;
  IF member_departments ? target THEN RETURN true; END IF;
  IF NOT recursive THEN CONTINUE; END IF;
  FOR start_id IN SELECT jsonb_array_elements_text(member_departments) LOOP
   next_id:=start_id; visited:=ARRAY[]::text[];
   WHILE next_id<>'' LOOP
    IF next_id=ANY(visited) OR cardinality(visited)>=100 THEN uncertain:=true; EXIT; END IF;
    IF next_id=target THEN RETURN true; END IF;
    visited:=array_append(visited,next_id);
    steps:=steps+1;
    IF steps>10000 THEN RETURN NULL; END IF;
    SELECT parent_id INTO parent FROM platform_channel_directory_departments WHERE binding_id=binding AND department_id=next_id;
    IF NOT FOUND THEN uncertain:=true; EXIT; END IF;
    next_id:=parent;
   END LOOP;
  END LOOP;
 END LOOP;
 IF uncertain THEN RETURN NULL; END IF;
 RETURN false;
END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION manager_channel_actor_policy_allowed(principal uuid, conversation uuid, generation bigint) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM manager_channel_principals p JOIN platform_channel_bindings b ON b.id=p.binding_id JOIN manager_conversations c ON c.channel_binding_id=b.id
 WHERE p.id=principal AND c.id=conversation AND p.workspace_id=c.workspace_id AND b.workspace_id=c.workspace_id AND b.agent_id=c.agent_id
 AND b.enabled AND b.connection_generation=generation AND b.identity_epoch=p.identity_epoch AND c.channel_identity_epoch=b.identity_epoch AND c.channel_tenant_id=p.external_tenant_id AND (b.external_tenant_id<>'' AND b.external_tenant_id=p.external_tenant_id)
 AND NOT COALESCE(b.access->'deny_ids','[]'::jsonb) ? p.external_user_id
 AND NOT COALESCE(platform_channel_directory_department_match(b.id,p.external_user_id,COALESCE(NULLIF(b.access->'deny_department_rules','null'::jsonb),b.access->'deny_department_ids')),true)
 AND ((c.channel_chat_type='group' AND COALESCE((b.access->>'group_members_enabled')::boolean,false)) OR b.access->>'mode' IN ('all','denylist') OR (b.access->>'mode'='allowlist' AND (COALESCE(b.access->'allow_ids','[]'::jsonb) ? p.external_user_id OR COALESCE(platform_channel_directory_department_match(b.id,p.external_user_id,COALESCE(NULLIF(b.access->'allow_department_rules','null'::jsonb),b.access->'allow_department_ids')),false))))
 AND (c.channel_chat_type='direct' OR (NOT COALESCE(b.access->'deny_chat_ids','[]'::jsonb) ? c.external_chat_id AND (jsonb_array_length(COALESCE(b.access->'allow_chat_ids','[]'::jsonb))=0 OR COALESCE(b.access->'allow_chat_ids','[]'::jsonb) ? c.external_chat_id)))
 AND (c.channel_chat_type='group' OR c.channel_created_by=p.id));
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION platform_channel_directory_display_name(binding uuid, epoch bigint, tenant text, person text) RETURNS text LANGUAGE sql STABLE AS $$
 SELECT u.display_name FROM platform_channel_directory_users u
 JOIN platform_channel_directory_state d ON d.binding_id=u.binding_id
 JOIN platform_channel_bindings b ON b.id=d.binding_id
 WHERE u.binding_id=binding AND u.user_id=person AND u.status='active' AND d.full_snapshot AND d.expires_at>now()
 AND b.enabled AND b.identity_epoch=epoch AND d.identity_epoch=b.identity_epoch
 AND b.connection_generation=d.connection_generation
 AND b.external_tenant_id=tenant AND d.external_tenant_id=tenant;
$$;
-- +goose StatementEnd
DROP TABLE platform_channel_observed_chats;

-- Existing non-Feishu data must never be deleted to make rollback succeed.
ALTER TABLE platform_channel_bindings DROP CONSTRAINT platform_channel_bindings_provider_check;
ALTER TABLE platform_channel_bindings ADD CONSTRAINT platform_channel_bindings_provider_check CHECK(provider='feishu');
ALTER TABLE manager_channel_inbox DROP COLUMN provider_directed;

ALTER TABLE platform_channel_bindings DROP COLUMN identity_verified_generation;
