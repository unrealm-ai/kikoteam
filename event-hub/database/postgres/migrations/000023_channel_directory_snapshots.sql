-- +goose Up
-- A complete application-scoped snapshot is published atomically. Partial syncs
-- never establish department membership or author display names.
CREATE TABLE platform_channel_directory_state (
 binding_id uuid PRIMARY KEY REFERENCES platform_channel_bindings(id),
 identity_epoch bigint NOT NULL,
 connection_generation bigint NOT NULL,
 external_tenant_id text NOT NULL,
 snapshot_version bigint NOT NULL DEFAULT 0,
 full_snapshot boolean NOT NULL DEFAULT false,
 last_result text NOT NULL DEFAULT 'never' CHECK(last_result IN ('never','ready','failed','invalidated')),
 error_code text NOT NULL DEFAULT '',
 synced_at timestamptz,
 expires_at timestamptz,
 next_sync_at timestamptz NOT NULL DEFAULT now(),
 sync_requested boolean NOT NULL DEFAULT true,
 lease_token uuid,
 lease_until timestamptz,
 user_count integer NOT NULL DEFAULT 0,
 department_count integer NOT NULL DEFAULT 0
);
CREATE TABLE platform_channel_directory_users (
 binding_id uuid NOT NULL REFERENCES platform_channel_directory_state(binding_id),
 user_id text NOT NULL,
 display_name text NOT NULL,
 status text NOT NULL CHECK(status IN ('active','inactive','unknown')),
 department_ids jsonb NOT NULL CHECK(jsonb_typeof(department_ids)='array'),
 memberships_known boolean NOT NULL,
 PRIMARY KEY(binding_id,user_id)
);
CREATE TABLE platform_channel_directory_departments (
 binding_id uuid NOT NULL REFERENCES platform_channel_directory_state(binding_id),
 department_id text NOT NULL,
 display_name text NOT NULL,
 parent_id text NOT NULL DEFAULT '',
 PRIMARY KEY(binding_id,department_id)
);
CREATE INDEX platform_directory_users_name ON platform_channel_directory_users(binding_id,lower(display_name),user_id);
CREATE INDEX platform_directory_users_memberships ON platform_channel_directory_users USING gin(department_ids);
CREATE INDEX platform_directory_departments_parent ON platform_channel_directory_departments(binding_id,parent_id,department_id);
-- +goose StatementBegin
CREATE FUNCTION platform_channel_directory_display_name(binding uuid, epoch bigint, tenant text, person text) RETURNS text LANGUAGE sql STABLE AS $$
 SELECT u.display_name FROM platform_channel_directory_users u
 JOIN platform_channel_directory_state d ON d.binding_id=u.binding_id
 JOIN platform_channel_bindings b ON b.id=d.binding_id
 WHERE u.binding_id=binding AND u.user_id=person AND u.status='active' AND d.full_snapshot AND d.expires_at>now()
 AND b.enabled AND b.identity_epoch=epoch AND d.identity_epoch=b.identity_epoch
 AND b.connection_generation=d.connection_generation
 AND b.external_tenant_id=tenant AND d.external_tenant_id=tenant;
$$;
-- +goose StatementEnd
-- NULL means unknown, not non-membership. In particular, absence from an old
-- snapshot cannot clear a department blacklist for a newly joined user.
-- +goose StatementBegin
CREATE FUNCTION platform_channel_directory_department_match(binding uuid, person text, departments jsonb) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT CASE WHEN jsonb_array_length(COALESCE(departments,'[]'::jsonb))=0 THEN false
 ELSE (SELECT CASE WHEN u.memberships_known AND u.status<>'inactive' THEN u.department_ids ?| ARRAY(SELECT jsonb_array_elements_text(departments)) ELSE NULL END
 FROM platform_channel_directory_users u
 JOIN platform_channel_directory_state d ON d.binding_id=u.binding_id
 JOIN platform_channel_bindings b ON b.id=d.binding_id
 WHERE u.binding_id=binding AND u.user_id=person AND d.full_snapshot AND d.expires_at>now()
 AND b.enabled AND b.identity_epoch=d.identity_epoch AND b.connection_generation=d.connection_generation
 AND b.external_tenant_id<>'' AND b.external_tenant_id=d.external_tenant_id) END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION manager_channel_actor_allowed(principal uuid, conversation uuid, generation bigint) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM manager_channel_principals p JOIN platform_channel_bindings b ON b.id=p.binding_id JOIN manager_conversations c ON c.channel_binding_id=b.id
 WHERE p.id=principal AND c.id=conversation AND p.workspace_id=c.workspace_id AND b.workspace_id=c.workspace_id AND b.agent_id=c.agent_id
 AND b.enabled AND b.connection_generation=generation AND b.identity_epoch=p.identity_epoch AND c.channel_identity_epoch=b.identity_epoch AND c.channel_tenant_id=p.external_tenant_id AND (b.external_tenant_id<>'' AND b.external_tenant_id=p.external_tenant_id)
 AND NOT COALESCE(b.access->'deny_ids','[]'::jsonb) ? p.external_user_id
 AND NOT COALESCE(platform_channel_directory_department_match(b.id,p.external_user_id,b.access->'deny_department_ids'),true)
 AND ((c.channel_chat_type='group' AND COALESCE((b.access->>'group_members_enabled')::boolean,false)) OR b.access->>'mode' IN ('all','denylist') OR (b.access->>'mode'='allowlist' AND (COALESCE(b.access->'allow_ids','[]'::jsonb) ? p.external_user_id OR COALESCE(platform_channel_directory_department_match(b.id,p.external_user_id,b.access->'allow_department_ids'),false))))
 AND (c.channel_chat_type='direct' OR (NOT COALESCE(b.access->'deny_chat_ids','[]'::jsonb) ? c.external_chat_id AND (jsonb_array_length(COALESCE(b.access->'allow_chat_ids','[]'::jsonb))=0 OR COALESCE(b.access->'allow_chat_ids','[]'::jsonb) ? c.external_chat_id)))
 AND (c.channel_chat_type='group' OR c.channel_created_by=p.id));
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM platform_channel_bindings WHERE jsonb_array_length(COALESCE(access->'allow_department_ids','[]'::jsonb))>0 OR jsonb_array_length(COALESCE(access->'deny_department_ids','[]'::jsonb))>0) THEN
  RAISE EXCEPTION 'cannot remove active department authorization rules';
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION manager_channel_actor_allowed(principal uuid, conversation uuid, generation bigint) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM manager_channel_principals p JOIN platform_channel_bindings b ON b.id=p.binding_id JOIN manager_conversations c ON c.channel_binding_id=b.id
 WHERE p.id=principal AND c.id=conversation AND p.workspace_id=c.workspace_id AND b.workspace_id=c.workspace_id AND b.agent_id=c.agent_id
 AND b.enabled AND b.connection_generation=generation AND b.identity_epoch=p.identity_epoch AND c.channel_identity_epoch=b.identity_epoch AND c.channel_tenant_id=p.external_tenant_id AND (b.external_tenant_id<>'' AND b.external_tenant_id=p.external_tenant_id)
 AND NOT COALESCE(b.access->'deny_ids','[]'::jsonb) ? p.external_user_id
 AND ((c.channel_chat_type='group' AND COALESCE((b.access->>'group_members_enabled')::boolean,false)) OR b.access->>'mode' IN ('all','denylist') OR (b.access->>'mode'='allowlist' AND COALESCE(b.access->'allow_ids','[]'::jsonb) ? p.external_user_id))
 AND (c.channel_chat_type='direct' OR (NOT COALESCE(b.access->'deny_chat_ids','[]'::jsonb) ? c.external_chat_id AND (jsonb_array_length(COALESCE(b.access->'allow_chat_ids','[]'::jsonb))=0 OR COALESCE(b.access->'allow_chat_ids','[]'::jsonb) ? c.external_chat_id)))
 AND (c.channel_chat_type='group' OR c.channel_created_by=p.id));
$$;
-- +goose StatementEnd
DROP FUNCTION platform_channel_directory_display_name(uuid,bigint,text,text);
DROP FUNCTION platform_channel_directory_department_match(uuid,text,jsonb);
DROP TABLE platform_channel_directory_users,platform_channel_directory_departments,platform_channel_directory_state;
