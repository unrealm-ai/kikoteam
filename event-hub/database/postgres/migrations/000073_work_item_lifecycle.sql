-- +goose Up
ALTER TABLE manager_work_items DROP CONSTRAINT manager_work_items_status_check;
ALTER TABLE manager_work_items ADD CHECK(status IN ('working','awaiting_acceptance','completed','failed','blocked','cancelled'));
ALTER TABLE manager_work_items ADD COLUMN reason text NOT NULL DEFAULT '';
CREATE TABLE manager_work_events (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 work_item_id uuid NOT NULL REFERENCES manager_work_items(id),
 kind text NOT NULL CHECK(kind IN ('created','handoff','started','submitted','returned','completed','blocked','failed','cancelled')),
 agent_id uuid REFERENCES platform_agents(id),
 target_agent_id uuid REFERENCES platform_agents(id),
 turn_id uuid REFERENCES manager_turns(id),
 summary text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(),
 notify boolean NOT NULL DEFAULT false CHECK(NOT notify OR kind IN ('created','returned','completed','failed')),
 notified_at timestamptz
);
CREATE INDEX manager_work_events_history ON manager_work_events(work_item_id,id);
CREATE INDEX manager_work_events_terminal ON manager_work_events(turn_id) WHERE turn_id IS NOT NULL AND kind IN ('completed','failed');
CREATE INDEX manager_work_events_pending ON manager_work_events(id) WHERE notify AND notified_at IS NULL;
CREATE INDEX manager_work_board ON manager_work_items(workspace_id,created_at DESC,id DESC);
-- Reconstruct durable milestones in chronological order. Historical rows do not send notices.
INSERT INTO manager_work_events(work_item_id,kind,agent_id,target_agent_id,turn_id,summary,created_at)
 SELECT item,kind,source,target,turn,summary,stamp FROM (
 SELECT w.id AS item,CASE WHEN b.kind='submit' THEN 'submitted' WHEN b.turn_id=w.root_turn_id THEN 'created'
 WHEN b.payload->>'submission_id' IS NOT NULL THEN 'returned' ELSE 'handoff' END AS kind,
 s.agent_id AS source,NULLIF(b.payload->>'target_employee_id','')::uuid AS target,t.id AS turn,
 COALESCE(b.payload->>'summary',b.payload->>'completion_evidence','') AS summary,t.finished_at AS stamp,1 AS ordinal,b.id AS tie
 FROM manager_work_boundaries b JOIN manager_work_items w ON w.id=b.work_item_id
 JOIN manager_sessions s ON s.id=b.session_id JOIN manager_turns t ON t.id=b.turn_id
 WHERE b.state='committed' AND t.finished_at IS NOT NULL
 UNION ALL SELECT a.work_item_id,'started',a.target_agent_id,NULL,t.id,'',t.started_at,2,t.id
 FROM manager_work_assignments a JOIN manager_inputs i ON i.work_assignment_id=a.id AND i.source_kind='work_item_assignment' JOIN manager_turns t ON t.input_id=i.id
 UNION ALL SELECT id,status,NULL,NULL,NULL,COALESCE(acceptance_evidence,''),updated_at,3,id FROM manager_work_items WHERE status IN ('completed','cancelled','blocked')
 ) facts ORDER BY stamp,ordinal,tie;
ALTER TABLE manager_work_boundaries ADD COLUMN retry_after timestamptz, ADD COLUMN target_epoch bigint;
CREATE TABLE platform_work_receivers (
 agent_id uuid PRIMARY KEY REFERENCES platform_agents(id),version bigint NOT NULL CHECK(version>0),scope jsonb NOT NULL
);
ALTER TABLE platform_capability_packages ADD COLUMN work_scope jsonb;
CREATE TABLE manager_work_files (
 id uuid PRIMARY KEY, boundary_id uuid NOT NULL REFERENCES manager_work_boundaries(id),
 name text NOT NULL, digest text NOT NULL, size_bytes bigint NOT NULL CHECK(size_bytes BETWEEN 0 AND 33554432)
);
CREATE TABLE manager_work_file_selections (
 boundary_id uuid NOT NULL REFERENCES manager_work_boundaries(id),file_id uuid NOT NULL REFERENCES manager_work_files(id),
 parent_boundary_id uuid REFERENCES manager_work_boundaries(id), PRIMARY KEY(boundary_id,file_id)
);
ALTER TABLE manager_work_assignments ADD COLUMN boundary_id uuid UNIQUE REFERENCES manager_work_boundaries(id);
CREATE TABLE manager_work_notice_targets (
 event_id bigint NOT NULL REFERENCES manager_work_events(id),conversation_id uuid NOT NULL REFERENCES manager_conversations(id),
 trigger_notice_id uuid REFERENCES manager_trigger_notifications(id),channel_delivery_id uuid REFERENCES manager_channel_deliveries(id),
 status text NOT NULL, PRIMARY KEY(event_id,conversation_id)
);
-- +goose StatementBegin
CREATE FUNCTION manager_work_scope_allows(scope jsonb,source uuid) RETURNS boolean LANGUAGE sql IMMUTABLE AS $$
 SELECT CASE scope->>'mode' WHEN 'inherit' THEN true WHEN 'all' THEN true
 WHEN 'allowlist' THEN COALESCE(scope->'employee_ids' ? source::text,false)
 WHEN 'denylist' THEN NOT COALESCE(scope->'employee_ids' ? source::text,false) ELSE false END;
$$;
CREATE FUNCTION manager_work_receiver_allows(target uuid,source uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT COALESCE((SELECT manager_work_scope_allows(scope,source) FROM platform_work_receivers WHERE agent_id=target),true);
$$;
CREATE FUNCTION manager_work_package_allows(p jsonb,source uuid) RETURNS boolean LANGUAGE sql IMMUTABLE AS $$
 SELECT CASE WHEN COALESCE(p->'work_scope','null'::jsonb)='null' THEN manager_work_package_allowed(p)
 ELSE (p->>'enabled')::boolean AND manager_work_scope_allows(p->'work_scope',source) END;
$$;
ALTER FUNCTION manager_work_input_allowed(uuid) RENAME TO manager_work_input_before_policy;
CREATE FUNCTION manager_work_input_allowed(input_id uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT manager_work_input_before_policy(input_id) AND EXISTS(SELECT FROM manager_inputs i JOIN manager_work_assignments a
 ON a.id=i.work_assignment_id WHERE i.id=input_id AND manager_work_receiver_allows(a.target_agent_id,a.source_agent_id));
$$;
CREATE FUNCTION manager_work_file_allowed(grant_id uuid,file_id uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
WITH RECURSIVE chain AS (
 SELECT s.boundary_id,s.parent_boundary_id FROM manager_work_file_selections s WHERE s.boundary_id=$1 AND s.file_id=$2
 UNION ALL SELECT s.boundary_id,s.parent_boundary_id FROM chain c JOIN manager_work_file_selections s ON s.boundary_id=c.parent_boundary_id AND s.file_id=$2)
 SELECT EXISTS(SELECT FROM chain) AND NOT EXISTS(SELECT FROM chain c JOIN manager_work_boundaries b ON b.id=c.boundary_id
 JOIN manager_sessions src ON src.id=b.session_id JOIN platform_agents e ON e.id=src.agent_id
 JOIN manager_inputs source_input ON source_input.id=b.input_id
 LEFT JOIN manager_work_assignments source_assignment ON source_assignment.id=source_input.work_assignment_id
 JOIN manager_work_items w ON w.id=b.work_item_id JOIN manager_sessions root ON root.id=w.root_session_id
 JOIN platform_agents root_agent ON root_agent.id=root.agent_id JOIN manager_inputs authority ON authority.id=w.authority_input_id
 LEFT JOIN platform_agents target ON target.id=NULLIF(b.payload->>'target_employee_id','')::uuid
 WHERE b.state<>'committed' OR (source_assignment.id IS NOT NULL AND NOT manager_work_receiver_allows(e.id,source_assignment.source_agent_id))
 OR NOT root_agent.enabled OR authority.admission_epoch<>root_agent.admission_epoch OR source_input.admission_epoch<>e.admission_epoch OR NOT e.enabled OR src.lifecycle<>'active' OR src.stop_revision<>b.stop_revision
 OR root.lifecycle<>'active' OR root.stop_revision<>w.root_stop_revision OR NOT manager_input_actor_before_work_items(w.authority_input_id)
 OR (b.kind='handoff' AND (NOT target.enabled OR target.admission_epoch<>b.target_epoch OR NOT manager_work_receiver_allows(target.id,src.agent_id))));
$$;
CREATE FUNCTION manager_work_review_ready(session_id uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT FROM manager_sessions s WHERE s.id=session_id AND (
 EXISTS(SELECT FROM platform_model_profiles p WHERE p.id=NULLIF(s.configuration->>'model_profile_id','')::uuid AND p.workspace_id=s.workspace_id AND p.enabled AND p.validated AND p.protocol=s.configuration->>'protocol' AND p.base_url=s.configuration->>'base_url' AND p.model_id=s.configuration->>'model_id')
 OR EXISTS(SELECT FROM platform_proxy_models p JOIN platform_agent_model_keys k ON k.agent_id=s.agent_id
 WHERE p.id=NULLIF(s.configuration->>'platform_model_id','')::uuid AND p.protocol=s.configuration->>'protocol' AND p.enabled AND p.validated)));
$$;
CREATE FUNCTION manager_work_started() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 INSERT INTO manager_work_events(work_item_id,kind,agent_id,turn_id)
 SELECT a.work_item_id,'started',a.target_agent_id,NEW.id FROM manager_inputs i JOIN manager_work_assignments a ON a.id=i.work_assignment_id
 WHERE i.id=NEW.input_id AND i.source_kind='work_item_assignment';
 RETURN NEW;
END $$;
CREATE TRIGGER manager_work_started AFTER INSERT ON manager_turns FOR EACH ROW EXECUTE FUNCTION manager_work_started();
CREATE FUNCTION manager_work_cancel_event() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 INSERT INTO manager_work_events(work_item_id,kind) VALUES(NEW.id,'cancelled');
 RETURN NEW;
END $$;
CREATE TRIGGER manager_work_cancel_event AFTER UPDATE OF status ON manager_work_items FOR EACH ROW
 WHEN(OLD.status<>NEW.status AND NEW.status='cancelled') EXECUTE FUNCTION manager_work_cancel_event();
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Work item history, grants and delivery receipts require a forward migration'; END $$;
-- +goose StatementEnd
