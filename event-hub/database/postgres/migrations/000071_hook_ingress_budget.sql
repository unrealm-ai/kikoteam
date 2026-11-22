-- +goose Up
CREATE TABLE manager_hook_ingress_budgets (
 workspace_id uuid NOT NULL,
 trigger_id uuid NOT NULL,
 credit_us bigint NOT NULL CHECK (credit_us BETWEEN 0 AND 10000000),
 refilled_at timestamptz NOT NULL,
 PRIMARY KEY (workspace_id,trigger_id),
 FOREIGN KEY (workspace_id,trigger_id) REFERENCES manager_triggers(workspace_id,id) ON DELETE CASCADE
);

-- 500,000 microseconds per reservation is 120 requests/minute. Twenty units
-- of credit bound burst. The database clock is the production time authority;
-- an earlier timestamp never rewinds it or creates extra credit.
-- +goose StatementBegin
CREATE FUNCTION manager_consume_hook_ingress(w uuid, trigger uuid, tick timestamptz DEFAULT clock_timestamp())
RETURNS boolean LANGUAGE sql VOLATILE AS $$
 WITH charged AS (
  INSERT INTO manager_hook_ingress_budgets AS b(workspace_id,trigger_id,credit_us,refilled_at)
  VALUES(w,trigger,9500000,tick)
  ON CONFLICT(workspace_id,trigger_id) DO UPDATE SET
   credit_us=LEAST(10000000,b.credit_us+FLOOR(EXTRACT(EPOCH FROM (GREATEST(b.refilled_at,EXCLUDED.refilled_at)-b.refilled_at))*1000000)::bigint)-500000,
   refilled_at=GREATEST(b.refilled_at,EXCLUDED.refilled_at)
  WHERE LEAST(10000000,b.credit_us+FLOOR(EXTRACT(EPOCH FROM (GREATEST(b.refilled_at,EXCLUDED.refilled_at)-b.refilled_at))*1000000)::bigint)>=500000
  RETURNING 1
 )
 SELECT EXISTS(SELECT 1 FROM charged);
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION manager_consume_hook_ingress(uuid,uuid,timestamptz);
DROP TABLE manager_hook_ingress_budgets;
