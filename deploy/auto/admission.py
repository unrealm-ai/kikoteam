"""Single-host task admission. Called with the application's PostgreSQL connection."""

# The status of a task is derived from its session, inputs and turns. A started
# planning_state, on its own, survives completion and is not an execution lease.
ACTIVE = {
    "manager_sessions": """engine_id IS NOT NULL OR
        active_turn_id IS NOT NULL OR background_process_count IS DISTINCT FROM 0 OR
        (lifecycle='active' AND block_reason IS NOT NULL) OR
        stop_revision<>applied_stop_revision OR required_actions<>'[]'""",
    "manager_inputs": "status IN ('queued','started')",
    "manager_turns": "finished_at IS NULL",
    "manager_capability_calls": "finished_at IS NULL",
    "manager_conversation_sandboxes": "state IN ('preparing','stopping','unknown')",
    "manager_human_requests": "state IN ('preparing','pending','blocked')",
    "manager_work_items": "status IN ('working','awaiting_acceptance','blocked')",
    "manager_channel_inbox": "status IN ('pending','processing')",
    "manager_web_group_inbox": "status='pending'",
    "manager_trigger_events": "status='pending'",
    "manager_channel_history_jobs": "status IN ('pending','leased')",
    "manager_group_history_queries": "status IN ('pending','leased')",
    "manager_channel_groups": "lease_until > now()",
    "manager_channel_media": "lease_until > now()",
    "platform_channel_directory_state": "lease_until > now()",
    "platform_feishu_permission_checks": "lease_until > now()",
    "platform_wecom_group_refresh_jobs": "status IN ('pending','leased')",
    "platform_model_probe_leases": "expires_at > now()",
    "platform_model_usage": "status='running'",
    "platform_invitation_deliveries": "status='sending'",
}
for table in ("manager_channel_deliveries", "manager_human_notices", "manager_trigger_notifications"):
    ACTIVE[table] = "status IN ('pending','leased','sending','accepted','unknown')"
ACTIVE["manager_trigger_notifications"] += " OR context_lease_until > now()"

TABLES = sorted([*ACTIVE, "manager_tasks"])
SCHEMA = "kikoteam_deploy_guard"


def busy_query():
    return "\nUNION ALL\n".join(
        f"SELECT '{table}' AS source WHERE EXISTS (SELECT FROM public.{table} WHERE {predicate})"
        for table, predicate in ACTIVE.items()
    )


def acquire_sql():
    relations = ", ".join("public." + name for name in TABLES)
    triggers = "\n".join(
        f"CREATE TRIGGER kiko_hold_work BEFORE INSERT OR UPDATE ON public.{name} "
        f"FOR EACH ROW EXECUTE FUNCTION {SCHEMA}.hold_work();"
        for name in TABLES
    )
    # Empty queue polls take write locks too. Give those short transactions a
    # moment to finish, then take the final snapshot under all the locks.
    # Web, long connections, cron and the Engine all see the same boundary.
    return f"""
BEGIN;
SET LOCAL statement_timeout='10s';
SET LOCAL lock_timeout='1500ms';
LOCK TABLE {relations} IN SHARE ROW EXCLUSIVE MODE;
SELECT NOT EXISTS ({busy_query()}) AS idle
\\gset
\\if :idle
CREATE SCHEMA {SCHEMA};
CREATE FUNCTION {SCHEMA}.hold_work() RETURNS trigger LANGUAGE plpgsql AS $guard$
BEGIN
    IF TG_TABLE_NAME='manager_conversation_sandboxes' AND TG_OP='UPDATE' THEN
        IF NEW.state='stopped' THEN RETURN NEW; END IF;
    END IF;
    RAISE EXCEPTION 'deployment in progress' USING ERRCODE='55000';
END;
$guard$;
{triggers}
COMMIT;
\\echo FENCED
\\else
ROLLBACK;
\\echo BUSY
\\endif
"""


def release_sql():
    drops = "\n".join(f"DROP TRIGGER kiko_hold_work ON public.{name};" for name in TABLES)
    return f"""
BEGIN;
SET LOCAL lock_timeout='5s';
SELECT to_regnamespace('{SCHEMA}') IS NOT NULL AS guarded
\\gset
\\if :guarded
{drops}
DROP FUNCTION {SCHEMA}.hold_work();
DROP SCHEMA {SCHEMA};
\\endif
COMMIT;
"""
