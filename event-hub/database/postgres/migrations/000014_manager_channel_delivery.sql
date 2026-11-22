-- +goose Up
CREATE TABLE manager_channel_principals (
 id uuid PRIMARY KEY, workspace_id uuid NOT NULL, binding_id uuid NOT NULL REFERENCES platform_channel_bindings(id),
 identity_epoch bigint NOT NULL DEFAULT 1, provider text NOT NULL, external_tenant_id text NOT NULL, external_user_id text NOT NULL,
 UNIQUE(binding_id,identity_epoch,external_tenant_id,external_user_id), UNIQUE(workspace_id,id)
);
ALTER TABLE manager_conversations ALTER COLUMN created_by DROP NOT NULL;
ALTER TABLE manager_conversations ADD COLUMN channel_created_by uuid REFERENCES manager_channel_principals(id);
ALTER TABLE manager_conversations ADD COLUMN channel_binding_id uuid REFERENCES platform_channel_bindings(id);
ALTER TABLE manager_conversations ADD COLUMN external_chat_id text;
ALTER TABLE manager_conversations ADD COLUMN channel_identity_epoch bigint NOT NULL DEFAULT 1;
ALTER TABLE manager_conversations ADD COLUMN channel_tenant_id text NOT NULL DEFAULT '';
ALTER TABLE manager_conversations ADD COLUMN channel_chat_type text CHECK(channel_chat_type IN ('direct','group'));
ALTER TABLE manager_conversations ADD CONSTRAINT manager_conversation_creator CHECK ((created_by IS NOT NULL)::integer+(channel_created_by IS NOT NULL)::integer=1);
ALTER TABLE manager_conversations ADD CONSTRAINT manager_conversation_channel CHECK ((channel_created_by IS NOT NULL)=(channel_binding_id IS NOT NULL) AND (channel_binding_id IS NULL OR (web_user_id IS NULL AND external_chat_id IS NOT NULL AND channel_chat_type IS NOT NULL)));
CREATE UNIQUE INDEX manager_channel_conversation_unique ON manager_conversations(channel_binding_id,channel_identity_epoch,channel_tenant_id,channel_chat_type,external_chat_id) WHERE channel_binding_id IS NOT NULL;
ALTER TABLE manager_sessions ALTER COLUMN created_by DROP NOT NULL;
ALTER TABLE manager_sessions ADD COLUMN channel_created_by uuid REFERENCES manager_channel_principals(id);
ALTER TABLE manager_sessions ADD CONSTRAINT manager_session_creator CHECK ((created_by IS NOT NULL)::integer+(channel_created_by IS NOT NULL)::integer=1);
ALTER TABLE manager_inputs ALTER COLUMN submitted_by DROP NOT NULL;
ALTER TABLE manager_inputs ADD COLUMN channel_principal_id uuid REFERENCES manager_channel_principals(id);
ALTER TABLE manager_inputs ADD COLUMN channel_generation bigint;
ALTER TABLE manager_inputs ADD COLUMN context_state text CHECK(context_state IN ('pending','committed','absent','unknown'));
ALTER TABLE manager_inputs ADD COLUMN context_committed_at timestamptz;
ALTER TABLE manager_inputs ADD COLUMN channel_history_count integer NOT NULL DEFAULT 0 CHECK(channel_history_count BETWEEN 0 AND 100);
ALTER TABLE manager_inputs ADD CONSTRAINT manager_input_actor CHECK ((submitted_by IS NOT NULL)::integer+(channel_principal_id IS NOT NULL)::integer=1 AND (channel_principal_id IS NULL)=(channel_generation IS NULL));
CREATE TABLE manager_channel_inbox (
 id uuid PRIMARY KEY, binding_id uuid NOT NULL REFERENCES platform_channel_bindings(id), connection_generation bigint NOT NULL, identity_epoch bigint NOT NULL DEFAULT 1,
 config_revision bigint NOT NULL, rules_version bigint NOT NULL,
 provider_event_id text NOT NULL, provider_message_id text NOT NULL, fingerprint text NOT NULL,
 principal_id uuid REFERENCES manager_channel_principals(id), conversation_id uuid REFERENCES manager_conversations(id),
 external_tenant_id text NOT NULL, external_chat_id text NOT NULL, chat_type text NOT NULL CHECK(chat_type IN ('direct','group')),
 sender_id text NOT NULL, text text NOT NULL, message_type text NOT NULL,
 thread_id text NOT NULL DEFAULT '', trigger_created_at_ms bigint NOT NULL DEFAULT 0, bot_mentioned boolean NOT NULL DEFAULT false,
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','processing','history','accepted','rejected','ignored')),
 input_id uuid REFERENCES manager_inputs(id), lease_token uuid, lease_until timestamptz, error_code text,
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(binding_id,identity_epoch,external_tenant_id,provider_event_id), UNIQUE(binding_id,identity_epoch,external_tenant_id,provider_message_id)
);
CREATE TABLE manager_channel_history_boundaries (
 conversation_id uuid PRIMARY KEY REFERENCES manager_conversations(id),
 trigger_message_id text NOT NULL, trigger_created_at_ms bigint NOT NULL, input_seq bigint NOT NULL,
 inbox_id uuid NOT NULL REFERENCES manager_channel_inbox(id)
);
CREATE TABLE manager_channel_history_jobs (
 id uuid PRIMARY KEY, inbox_id uuid NOT NULL UNIQUE REFERENCES manager_channel_inbox(id),
 requested_limit integer NOT NULL CHECK(requested_limit BETWEEN 1 AND 100),
 previous_trigger_message_id text NOT NULL DEFAULT '', previous_trigger_created_at_ms bigint NOT NULL DEFAULT 0,
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','leased','completed')),
 lease_token uuid, lease_until timestamptz, deadline timestamptz NOT NULL DEFAULT now()+interval '45 seconds',
 snapshot jsonb, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE manager_channel_deliveries (
 id uuid PRIMARY KEY, inbox_id uuid NOT NULL REFERENCES manager_channel_inbox(id), binding_id uuid NOT NULL REFERENCES platform_channel_bindings(id),
 connection_generation bigint NOT NULL, kind text NOT NULL, text text NOT NULL,
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','leased','sending','sent','unknown','cancelled')),
 lease_token uuid, lease_until timestamptz, attempts integer NOT NULL DEFAULT 0,
 provider_message_id text, error_code text, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(inbox_id,kind)
);
CREATE INDEX manager_channel_inbox_pending ON manager_channel_inbox(status,created_at);
CREATE INDEX manager_channel_deliveries_pending ON manager_channel_deliveries(status,created_at);
-- +goose StatementBegin
CREATE FUNCTION manager_channel_actor_allowed(principal uuid, conversation uuid, generation bigint) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM manager_channel_principals p JOIN platform_channel_bindings b ON b.id=p.binding_id JOIN manager_conversations c ON c.channel_binding_id=b.id
 WHERE p.id=principal AND c.id=conversation AND p.workspace_id=c.workspace_id AND b.workspace_id=c.workspace_id AND b.agent_id=c.agent_id
 AND b.enabled AND b.connection_generation=generation AND b.identity_epoch=p.identity_epoch AND c.channel_identity_epoch=b.identity_epoch AND c.channel_tenant_id=p.external_tenant_id AND (b.external_tenant_id<>'' AND b.external_tenant_id=p.external_tenant_id)
 AND NOT COALESCE(b.access->'deny_ids','[]'::jsonb) ? p.external_user_id
 AND ((c.channel_chat_type='group' AND COALESCE((b.access->>'group_members_enabled')::boolean,false)) OR b.access->>'mode' IN ('all','denylist') OR (b.access->>'mode'='allowlist' AND COALESCE(b.access->'allow_ids','[]'::jsonb) ? p.external_user_id))
 AND (c.channel_chat_type='direct' OR (NOT COALESCE(b.access->'deny_chat_ids','[]'::jsonb) ? c.external_chat_id AND (jsonb_array_length(COALESCE(b.access->'allow_chat_ids','[]'::jsonb))=0 OR COALESCE(b.access->'allow_chat_ids','[]'::jsonb) ? c.external_chat_id)))
 AND (c.channel_chat_type='group' OR c.channel_created_by=p.id));
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION manager_input_actor_allowed(input uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM manager_inputs i JOIN manager_sessions s ON s.id=i.session_id WHERE i.id=input AND
 ((i.submitted_by IS NOT NULL AND EXISTS(SELECT 1 FROM platform_members m WHERE m.workspace_id=i.workspace_id AND m.user_id=i.submitted_by AND m.status='active'))
 OR (i.channel_principal_id IS NOT NULL AND manager_channel_actor_allowed(i.channel_principal_id,s.conversation_id,i.channel_generation) AND (i.channel_history_count=0 OR EXISTS(SELECT 1 FROM manager_channel_principals p JOIN platform_channel_bindings b ON b.id=p.binding_id WHERE p.id=i.channel_principal_id AND COALESCE((b.history->>'enabled')::boolean,false) AND COALESCE((b.history->>'limit')::integer,0)>=i.channel_history_count)))));
$$;
-- +goose StatementEnd
-- +goose Down
-- Channel histories must be migrated offline before removing principal identity.
DO $$ BEGIN IF EXISTS(SELECT 1 FROM manager_channel_inbox) THEN RAISE EXCEPTION 'channel data requires reviewed offline migration'; END IF; END $$;
DROP FUNCTION manager_input_actor_allowed(uuid);
DROP FUNCTION manager_channel_actor_allowed(uuid,uuid,bigint);
DROP TABLE manager_channel_deliveries,manager_channel_history_jobs,manager_channel_history_boundaries,manager_channel_inbox;
ALTER TABLE manager_inputs DROP CONSTRAINT manager_input_actor, DROP COLUMN channel_principal_id, DROP COLUMN channel_generation, DROP COLUMN context_state, DROP COLUMN context_committed_at, DROP COLUMN channel_history_count;
ALTER TABLE manager_inputs ALTER COLUMN submitted_by SET NOT NULL;
ALTER TABLE manager_sessions DROP CONSTRAINT manager_session_creator, DROP COLUMN channel_created_by;
ALTER TABLE manager_sessions ALTER COLUMN created_by SET NOT NULL;
ALTER TABLE manager_conversations DROP CONSTRAINT manager_conversation_creator, DROP CONSTRAINT manager_conversation_channel, DROP COLUMN channel_created_by, DROP COLUMN channel_binding_id, DROP COLUMN external_chat_id, DROP COLUMN channel_chat_type, DROP COLUMN channel_identity_epoch, DROP COLUMN channel_tenant_id;
ALTER TABLE manager_conversations ALTER COLUMN created_by SET NOT NULL;
DROP TABLE manager_channel_principals;
