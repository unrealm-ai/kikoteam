-- +goose Up
-- Translate stored rules and snapshots without changing their effective grants.
-- +goose StatementBegin
CREATE FUNCTION pg_temp.explicit_trigger_scope(scope jsonb) RETURNS jsonb LANGUAGE sql IMMUTABLE AS $$
 SELECT CASE scope->>'mode'
 WHEN 'all' THEN jsonb_set(scope,'{allow_trigger_ids}','[]'::jsonb)
 WHEN 'allowlist' THEN (scope || jsonb_build_object('deny_trigger_ids','[]'::jsonb,
  'allow_trigger_ids',COALESCE((SELECT jsonb_agg(v ORDER BY n)
    FROM jsonb_array_elements(COALESCE(NULLIF(scope->'allow_trigger_ids','null'::jsonb),'[]'::jsonb)) WITH ORDINALITY AS e(v,n)
    WHERE NOT (COALESCE(scope->'deny_trigger_ids','[]'::jsonb) @> jsonb_build_array(v))),'[]'::jsonb)))
 ELSE scope END;
$$;
CREATE FUNCTION pg_temp.explicit_work_scope(p jsonb) RETURNS jsonb LANGUAGE sql IMMUTABLE AS $$
 SELECT CASE WHEN manager_work_package_allowed(jsonb_set(p,'{enabled}','true'::jsonb))
 THEN '{"mode":"inherit","employee_ids":[]}'::jsonb ELSE '{"mode":"allowlist","employee_ids":[]}'::jsonb END;
$$;
CREATE FUNCTION pg_temp.explicit_package(p jsonb) RETURNS jsonb LANGUAGE sql IMMUTABLE AS $$
 SELECT p || jsonb_build_object('work_scope',COALESCE(NULLIF(p->'work_scope','null'::jsonb),pg_temp.explicit_work_scope(p))) ||
 CASE WHEN jsonb_typeof(p->'trigger_scope')='object' THEN jsonb_build_object('trigger_scope',pg_temp.explicit_trigger_scope(p->'trigger_scope')) ELSE '{}'::jsonb END;
$$;
-- +goose StatementEnd

UPDATE platform_capability_packages p SET
 work_scope=COALESCE(p.work_scope,pg_temp.explicit_work_scope(to_jsonb(p))),
 trigger_scope=pg_temp.explicit_trigger_scope(p.trigger_scope)
 WHERE p.work_scope IS NULL OR trigger_scope IS DISTINCT FROM pg_temp.explicit_trigger_scope(p.trigger_scope);

UPDATE platform_capability_package_versions v SET snapshot=pg_temp.explicit_package(v.snapshot)
 WHERE v.snapshot IS DISTINCT FROM pg_temp.explicit_package(v.snapshot);

UPDATE manager_sessions s SET configuration=jsonb_set(s.configuration,'{capabilities,packages}',(
 SELECT jsonb_agg(pg_temp.explicit_package(p) ORDER BY n)
 FROM jsonb_array_elements(s.configuration#>'{capabilities,packages}') WITH ORDINALITY AS x(p,n)
))
WHERE jsonb_typeof(s.configuration#>'{capabilities,packages}')='array'
 AND EXISTS(SELECT 1 FROM jsonb_array_elements(s.configuration#>'{capabilities,packages}') p
 WHERE p IS DISTINCT FROM pg_temp.explicit_package(p));

ALTER TABLE platform_capability_packages ALTER COLUMN work_scope SET DEFAULT '{"mode":"inherit","employee_ids":[]}'::jsonb;
ALTER TABLE platform_capability_packages ALTER COLUMN work_scope SET NOT NULL;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION manager_work_package_allows(p jsonb,source uuid) RETURNS boolean LANGUAGE sql IMMUTABLE AS $$
 SELECT COALESCE((p->>'enabled')::boolean,false) AND manager_work_scope_allows(p->'work_scope',source);
$$;
-- +goose StatementEnd
DROP FUNCTION pg_temp.explicit_package(jsonb);
DROP FUNCTION pg_temp.explicit_work_scope(jsonb);
DROP FUNCTION pg_temp.explicit_trigger_scope(jsonb);
DROP FUNCTION manager_work_package_allowed(jsonb);

-- +goose Down
-- Keep explicit grants; rollback cannot reconstruct the old pair of lists.
ALTER TABLE platform_capability_packages ALTER COLUMN work_scope DROP NOT NULL;
ALTER TABLE platform_capability_packages ALTER COLUMN work_scope DROP DEFAULT;
-- +goose StatementBegin
CREATE FUNCTION manager_work_package_allowed(p jsonb) RETURNS boolean LANGUAGE sql IMMUTABLE AS $$
 SELECT COALESCE((p->>'enabled')::boolean,false) AND COALESCE(p->'conversation_scope','null'::jsonb)='null'
 AND p#>>'{scope,mode}'='inherit_with_blacklist'
 AND NOT EXISTS (SELECT 1 FROM unnest(ARRAY['allow_member_ids','deny_member_ids','allow','deny']) AS k
  WHERE COALESCE(p->'scope'->k,'null'::jsonb) NOT IN ('null','[]'));
$$;
CREATE OR REPLACE FUNCTION manager_work_package_allows(p jsonb,source uuid) RETURNS boolean LANGUAGE sql IMMUTABLE AS $$
 SELECT CASE WHEN COALESCE(p->'work_scope','null'::jsonb)='null' THEN manager_work_package_allowed(p)
 ELSE (p->>'enabled')::boolean AND manager_work_scope_allows(p->'work_scope',source) END;
$$;
-- +goose StatementEnd
