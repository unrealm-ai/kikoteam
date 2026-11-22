-- +goose Up
-- Credential bindings retain only a materialized, read-only provider projection.
-- Identity-scoped selectors cannot migrate to a replacement application epoch.
-- +goose StatementBegin
CREATE FUNCTION platform_agent_usage_projection(config jsonb,provider text,binding uuid,epoch bigint) RETURNS jsonb LANGUAGE sql IMMUTABLE AS $$
 WITH selectors AS (
 SELECT side,s FROM (VALUES ('allow'),('deny')) sides(side),LATERAL jsonb_array_elements(COALESCE(config->'access'->side,'[]'::jsonb)) s
 WHERE s->>'channel'=provider AND (provider='web' OR (s->>'binding_id'=binding::text AND s->>'identity_epoch'=epoch::text))
 ), parts AS (
 SELECT jsonb_build_object(
 'mode',COALESCE(config#>>'{access,mode}','allowlist'),'group_members_enabled',COALESCE((config#>>'{access,group_members_enabled}')::boolean,true),
 'allow_ids',COALESCE((SELECT jsonb_agg(s->>'id') FROM selectors WHERE side='allow' AND s->>'kind'='user'),'[]'::jsonb),
 'deny_ids',COALESCE((SELECT jsonb_agg(s->>'id') FROM selectors WHERE side='deny' AND s->>'kind'='user'),'[]'::jsonb),
 'allow_chat_ids',COALESCE((SELECT jsonb_agg(s->>'id') FROM selectors WHERE side='allow' AND s->>'kind'='group'),'[]'::jsonb),
 'deny_chat_ids',COALESCE((SELECT jsonb_agg(s->>'id') FROM selectors WHERE side='deny' AND s->>'kind'='group'),'[]'::jsonb),
 'allow_department_ids',COALESCE((SELECT jsonb_agg(s->>'id') FROM selectors WHERE side='allow' AND s->>'kind'='department'),'[]'::jsonb),
 'deny_department_ids',COALESCE((SELECT jsonb_agg(s->>'id') FROM selectors WHERE side='deny' AND s->>'kind'='department'),'[]'::jsonb),
 'allow_department_rules',COALESCE((SELECT jsonb_agg(jsonb_build_object('department_id',s->>'id','include_descendants',COALESCE((s->>'include_descendants')::boolean,false))) FROM selectors WHERE side='allow' AND s->>'kind'='department'),'[]'::jsonb),
 'deny_department_rules',COALESCE((SELECT jsonb_agg(jsonb_build_object('department_id',s->>'id','include_descendants',COALESCE((s->>'include_descendants')::boolean,false))) FROM selectors WHERE side='deny' AND s->>'kind'='department'),'[]'::jsonb)
 ) AS access
 )
 SELECT jsonb_build_object('access',access,'trigger',jsonb_build_object(
 'mention',CASE WHEN provider IN ('dingtalk','wecom') THEN true ELSE COALESCE((config#>>'{trigger,mention}')::boolean,true) END,
 'keywords',COALESCE((SELECT jsonb_agg(r->>'text') FROM jsonb_array_elements(COALESCE(config#>'{trigger,keywords}','[]'::jsonb)) r WHERE provider IN ('web','feishu') AND r->'channels' ? provider),'[]'::jsonb),
 'regexes',COALESCE((SELECT jsonb_agg(r->>'text') FROM jsonb_array_elements(COALESCE(config#>'{trigger,regexes}','[]'::jsonb)) r WHERE provider IN ('web','feishu') AND r->'channels' ? provider),'[]'::jsonb)),
 'history',jsonb_build_object('enabled',COALESCE((config#>>'{history,enabled}')::boolean,false) AND provider IN ('web','feishu','wecom') AND COALESCE(config#>'{history,channels}','[]'::jsonb) ? provider,'limit',COALESCE((config#>>'{history,limit}')::integer,20))) FROM parts;
$$;
-- +goose StatementEnd
-- Policies are deliberately not imported from per-channel settings. No message,
-- binding, identity, or history data is deleted by this migration.
UPDATE platform_channel_bindings b SET trigger=p.value->'trigger',history=p.value->'history',access=p.value->'access',rules_version=rules_version+1,config_revision=config_revision+1,applied_status='pending'
FROM (SELECT b2.id,platform_agent_usage_projection(a.usage_config,b2.provider,b2.id,b2.identity_epoch) value FROM platform_channel_bindings b2 JOIN platform_agents a ON a.id=b2.agent_id AND a.workspace_id=b2.workspace_id) p WHERE b.id=p.id;
UPDATE platform_channel_config_clock SET revision=revision+1 WHERE id=true;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION platform_agent_web_allowed(workspace uuid,agent uuid,actor uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM platform_agents a JOIN platform_members m ON m.workspace_id=a.workspace_id AND m.user_id=$3 AND m.status='active'
 CROSS JOIN LATERAL (SELECT platform_agent_usage_projection(a.usage_config,'web',NULL,0)->'access' AS access) p
 WHERE a.workspace_id=$1 AND a.id=$2 AND a.enabled AND NOT (p.access->'deny_ids' ? $3::text)
 AND (p.access->>'mode'='all' OR p.access->'allow_ids' ? $3::text));
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION platform_web_group_agent_allowed(workspace uuid,group_id uuid,agent uuid,actor uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT platform_web_group_member_allowed($1,$2,$4) AND EXISTS(
 SELECT 1 FROM platform_web_group_agents ga JOIN platform_agents a ON a.id=ga.agent_id AND a.workspace_id=ga.workspace_id
 CROSS JOIN LATERAL (SELECT platform_agent_usage_projection(a.usage_config,'web',NULL,0)->'access' AS access) p
 WHERE ga.group_id=$2 AND ga.workspace_id=$1 AND ga.agent_id=$3 AND ga.status='active' AND a.enabled
 AND NOT (p.access->'deny_ids' ? $4::text) AND ((p.access->>'group_members_enabled')::boolean OR p.access->>'mode'='all' OR p.access->'allow_ids' ? $4::text)
 AND NOT (p.access->'deny_chat_ids' ? $2::text) AND (jsonb_array_length(p.access->'allow_chat_ids')=0 OR p.access->'allow_chat_ids' ? $2::text));
$$;
-- +goose StatementEnd
-- +goose Down
-- Employee policy cannot safely be translated back to disconnected settings.
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'employee usage projection rollback requires explicit policy migration'; END $$;
-- +goose StatementEnd
