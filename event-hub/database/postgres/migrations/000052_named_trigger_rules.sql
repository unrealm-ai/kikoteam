-- +goose Up
-- Credential bindings retain only a materialized, read-only provider projection.
-- Identity-scoped selectors cannot migrate to a replacement application epoch.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION platform_agent_usage_projection(config jsonb,provider text,binding uuid,epoch bigint) RETURNS jsonb LANGUAGE sql IMMUTABLE AS $$
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
 'keywords',COALESCE((SELECT jsonb_agg(r->>'text') FROM jsonb_array_elements(COALESCE(config#>'{trigger,keywords}','[]'::jsonb)) r WHERE provider IN ('web','feishu') AND r->'channels' ? provider AND COALESCE((r->>'enabled')::boolean,true)),'[]'::jsonb),
 'regexes',COALESCE((SELECT jsonb_agg(r->>'text') FROM jsonb_array_elements(COALESCE(config#>'{trigger,regexes}','[]'::jsonb)) r WHERE provider IN ('web','feishu') AND r->'channels' ? provider AND COALESCE((r->>'enabled')::boolean,true)),'[]'::jsonb)),
 'history',jsonb_build_object('enabled',COALESCE((config#>>'{history,enabled}')::boolean,false) AND provider IN ('web','feishu','wecom') AND COALESCE(config#>'{history,channels}','[]'::jsonb) ? provider,'limit',COALESCE((config#>>'{history,limit}')::integer,20))) FROM parts;
$$;
-- +goose StatementEnd

-- Metadata is stored with employee usage JSON; omitted enabled preserves legacy behavior.
