-- +goose Up
-- Binding identity epoch, not display names or a connection's transient generation,
-- scopes durable lifecycle evidence. A disband tombstone is terminal in that epoch.
CREATE TABLE manager_channel_groups (
 id uuid PRIMARY KEY, binding_id uuid NOT NULL REFERENCES platform_channel_bindings(id),
 identity_epoch bigint NOT NULL, external_tenant_id text NOT NULL, external_chat_id text NOT NULL,
 state text NOT NULL CHECK(state IN ('pending','present','absent','disbanded')),
 watermark_ms bigint NOT NULL CHECK(watermark_ms>0), version bigint NOT NULL DEFAULT 1,
 connection_generation bigint NOT NULL, config_revision bigint NOT NULL,
 retry_at timestamptz NOT NULL DEFAULT now(), attempts integer NOT NULL DEFAULT 0,
 last_error_code text NOT NULL DEFAULT '',
 lease_token uuid, lease_until timestamptz,
 UNIQUE(binding_id,identity_epoch,external_tenant_id,external_chat_id)
);
CREATE INDEX manager_channel_groups_pending ON manager_channel_groups(retry_at) WHERE state='pending';
CREATE TABLE manager_channel_group_events (
 id uuid PRIMARY KEY, binding_id uuid NOT NULL REFERENCES platform_channel_bindings(id), identity_epoch bigint NOT NULL,
 external_tenant_id text NOT NULL, provider_event_id text NOT NULL, fingerprint text NOT NULL,
 UNIQUE(binding_id,identity_epoch,external_tenant_id,provider_event_id)
);
CREATE TABLE manager_channel_group_members (
 binding_id uuid NOT NULL REFERENCES platform_channel_bindings(id), identity_epoch bigint NOT NULL,
 external_tenant_id text NOT NULL, external_chat_id text NOT NULL, member_id text NOT NULL,
 present boolean NOT NULL, watermark_ms bigint NOT NULL CHECK(watermark_ms>0),
 PRIMARY KEY(binding_id,identity_epoch,external_tenant_id,external_chat_id,member_id)
);
-- +goose StatementBegin
CREATE FUNCTION manager_channel_group_allowed(binding uuid, epoch bigint, tenant text, chat text, member text) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT NOT EXISTS(SELECT 1 FROM manager_channel_groups g WHERE g.binding_id=binding AND g.identity_epoch=epoch AND g.external_tenant_id=tenant AND g.external_chat_id=chat AND g.state<>'present')
 AND NOT EXISTS(SELECT 1 FROM manager_channel_group_members m WHERE m.binding_id=binding AND m.identity_epoch=epoch AND m.external_tenant_id=tenant AND m.external_chat_id=chat AND m.member_id=member AND NOT m.present);
$$;
-- +goose StatementEnd
ALTER FUNCTION manager_channel_actor_allowed(uuid,uuid,bigint) RENAME TO manager_channel_actor_policy_allowed;
-- +goose StatementBegin
CREATE FUNCTION manager_channel_actor_allowed(principal uuid, conversation uuid, generation bigint) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT manager_channel_actor_policy_allowed(principal,conversation,generation) AND EXISTS(SELECT 1 FROM manager_conversations c JOIN manager_channel_principals p ON p.id=principal WHERE c.id=conversation AND (c.channel_chat_type='direct' OR manager_channel_group_allowed(c.channel_binding_id,c.channel_identity_epoch,c.channel_tenant_id,c.external_chat_id,p.external_user_id)));
$$;
-- +goose StatementEnd
-- Inputs accepted before a leave cannot regain execution authority on rejoin.
ALTER FUNCTION manager_input_actor_allowed(uuid) RENAME TO manager_input_actor_policy_allowed;
-- +goose StatementBegin
CREATE FUNCTION manager_input_actor_allowed(input uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT manager_input_actor_policy_allowed(input) AND NOT EXISTS(
 SELECT 1 FROM manager_inputs i JOIN manager_sessions s ON s.id=i.session_id
 JOIN manager_conversations c ON c.id=s.conversation_id
 JOIN manager_channel_principals p ON p.id=i.channel_principal_id
 LEFT JOIN manager_channel_inbox n ON n.input_id=i.id
 WHERE i.id=input AND c.channel_chat_type='group' AND (
 EXISTS(SELECT 1 FROM manager_channel_groups g WHERE g.binding_id=c.channel_binding_id AND g.identity_epoch=c.channel_identity_epoch AND g.external_tenant_id=c.channel_tenant_id AND g.external_chat_id=c.external_chat_id AND g.watermark_ms>COALESCE(n.trigger_created_at_ms,(extract(epoch FROM i.created_at)*1000)::bigint))
 OR EXISTS(SELECT 1 FROM manager_channel_group_members m WHERE m.binding_id=c.channel_binding_id AND m.identity_epoch=c.channel_identity_epoch AND m.external_tenant_id=c.channel_tenant_id AND m.external_chat_id=c.external_chat_id AND m.member_id=p.external_user_id AND m.watermark_ms>COALESCE(n.trigger_created_at_ms,(extract(epoch FROM i.created_at)*1000)::bigint))));
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION manager_input_actor_allowed(uuid);
ALTER FUNCTION manager_input_actor_policy_allowed(uuid) RENAME TO manager_input_actor_allowed;
DROP FUNCTION manager_channel_actor_allowed(uuid,uuid,bigint);
ALTER FUNCTION manager_channel_actor_policy_allowed(uuid,uuid,bigint) RENAME TO manager_channel_actor_allowed;
DROP FUNCTION manager_channel_group_allowed(uuid,bigint,text,text,text);
DROP TABLE manager_channel_group_members,manager_channel_group_events,manager_channel_groups;
