-- +goose Up
-- Each native prompt has one trigger input and zero or more preceding context
-- inputs. Keep individual source rows and bind their receipt to that trigger.
ALTER TABLE manager_inputs ADD COLUMN batch_input_id uuid;
ALTER TABLE manager_inputs ADD CONSTRAINT manager_inputs_batch_fk FOREIGN KEY(workspace_id,session_id,batch_input_id) REFERENCES manager_inputs(workspace_id,session_id,id) DEFERRABLE INITIALLY DEFERRED;
CREATE INDEX manager_inputs_batch_idx ON manager_inputs(batch_input_id) WHERE batch_input_id IS NOT NULL;

-- Context-only data does not inherit its historical event actor's current
-- execution authority. It must still belong to the same enabled binding epoch.
-- +goose StatementBegin
CREATE FUNCTION manager_input_admission_allowed(input uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT CASE WHEN i.trigger_agent_loop THEN manager_input_actor_allowed(i.id)
 ELSE i.channel_principal_id IS NULL OR EXISTS (
 SELECT 1 FROM manager_channel_principals p
 JOIN manager_sessions s ON s.id=i.session_id
 JOIN manager_conversations c ON c.id=s.conversation_id
 JOIN platform_channel_bindings b ON b.id=c.channel_binding_id
 WHERE p.id=i.channel_principal_id AND p.binding_id=b.id AND b.enabled
 AND b.connection_generation=i.channel_generation AND p.external_tenant_id=c.channel_tenant_id
 AND b.external_tenant_id=p.external_tenant_id
 AND b.identity_epoch=p.identity_epoch AND b.identity_epoch=c.channel_identity_epoch
 AND b.workspace_id=i.workspace_id AND p.workspace_id=i.workspace_id
 AND b.agent_id=s.agent_id
 AND (i.channel_history_count=0 OR (COALESCE((b.history->>'enabled')::boolean,false) AND COALESCE((b.history->>'limit')::integer,0)>=i.channel_history_count))
 ) END FROM manager_inputs i WHERE i.id=input;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM manager_inputs WHERE batch_input_id IS NOT NULL) THEN
  RAISE EXCEPTION 'cannot discard native input batch receipt associations';
 END IF;
END $$;
-- +goose StatementEnd
DROP FUNCTION manager_input_admission_allowed(uuid);
ALTER TABLE manager_inputs DROP COLUMN batch_input_id;
