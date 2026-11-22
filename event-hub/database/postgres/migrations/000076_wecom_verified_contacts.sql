-- +goose Up
-- Carry verified identity evidence after the same bot confirms a new connection.
-- Challenges and in-flight delivery/request targets keep their original generation.
-- +goose StatementBegin
CREATE FUNCTION platform_wecom_carry_contacts(b platform_channel_bindings, previous_generation bigint, confirmed_generation bigint)
RETURNS void LANGUAGE plpgsql AS $$
BEGIN
 -- During upgrade there may be independent proofs from several generations.
 -- Import only unambiguous pairs. On a normal rotation the last confirmed
 -- generation already contains the reconciled set.
 WITH pairs AS (
   SELECT DISTINCT ON (search_user_id,callback_user_id) m.* FROM platform_wecom_identity_mappings m
   WHERE m.binding_id=b.id AND m.identity_epoch=b.identity_epoch AND m.connection_generation<confirmed_generation
     AND (previous_generation IS NULL OR m.connection_generation=previous_generation)
   ORDER BY search_user_id,callback_user_id,connection_generation DESC
 ), candidates AS (
   SELECT p.*,count(*) OVER (PARTITION BY search_user_id) AS callbacks,
          count(*) OVER (PARTITION BY callback_user_id) AS people FROM pairs p
 )
 INSERT INTO platform_wecom_identity_mappings(binding_id,identity_epoch,connection_generation,search_user_id,callback_user_id,verification_id,verified_at)
 SELECT binding_id,identity_epoch,confirmed_generation,search_user_id,callback_user_id,verification_id,verified_at FROM candidates
 WHERE callbacks=1 AND people=1 ON CONFLICT DO NOTHING;

 INSERT INTO platform_wecom_contacts(binding_id,identity_epoch,connection_generation,search_user_id,display_name,department_paths,expires_at)
 SELECT DISTINCT ON (c.search_user_id) b.id,b.identity_epoch,confirmed_generation,c.search_user_id,c.display_name,c.department_paths,c.expires_at
 FROM platform_wecom_identity_mappings dest
 JOIN platform_wecom_identity_mappings src USING(binding_id,identity_epoch,search_user_id,callback_user_id)
 JOIN platform_wecom_contacts c USING(binding_id,identity_epoch,search_user_id)
 WHERE dest.binding_id=b.id AND dest.identity_epoch=b.identity_epoch AND dest.connection_generation=confirmed_generation
   AND src.connection_generation=c.connection_generation AND src.connection_generation<confirmed_generation
   AND (previous_generation IS NULL OR src.connection_generation=previous_generation)
 ORDER BY c.search_user_id,c.connection_generation DESC
 ON CONFLICT DO NOTHING;

 -- Refresh only saved recipients backed by the same proven pair in both
 -- generations. Requests that were already issued retain their dispatch fences.
 UPDATE platform_agent_human_recipients h SET recipients=renewed.items,version=h.version+1
 FROM (
   SELECT h0.version,jsonb_agg(CASE WHEN dest.search_user_id IS NULL THEN r.item
     ELSE jsonb_set(r.item,'{ref,connection_generation}',to_jsonb(confirmed_generation)) END ORDER BY r.ord) AS items
   FROM platform_agent_human_recipients h0
   CROSS JOIN LATERAL jsonb_array_elements(h0.recipients) WITH ORDINALITY r(item,ord)
   LEFT JOIN platform_wecom_identity_mappings src ON src.binding_id=b.id AND src.identity_epoch=b.identity_epoch
     AND src.callback_user_id=r.item->'ref'->>'external_id'
     AND src.connection_generation::text=r.item->'ref'->>'connection_generation' AND src.connection_generation<confirmed_generation
     AND r.item->'ref' @> jsonb_build_object('provider','wecom','kind','user','identity_namespace','user_id',
       'binding_id',b.id,'identity_epoch',b.identity_epoch,'tenant_id',b.app_id,'workspace_id',b.workspace_id,'agent_id',b.agent_id)
   LEFT JOIN platform_wecom_identity_mappings dest ON dest.binding_id=src.binding_id AND dest.identity_epoch=src.identity_epoch
     AND dest.connection_generation=confirmed_generation AND dest.callback_user_id=src.callback_user_id AND dest.search_user_id=src.search_user_id
   WHERE h0.agent_id=b.agent_id GROUP BY h0.version HAVING count(dest.search_user_id)>0
 ) renewed WHERE h.agent_id=b.agent_id AND h.version=renewed.version;
END;
$$;
-- +goose StatementEnd

-- Include bindings waiting for re-authentication at upgrade. Their old
-- confirmed generation remains inaccessible until trusted ingress resumes.
SELECT platform_wecom_carry_contacts(b,NULL,b.identity_verified_generation)
FROM platform_channel_bindings b
WHERE b.provider='wecom' AND b.app_id=b.external_tenant_id AND b.app_id=b.bot_open_id
 AND EXISTS (SELECT FROM platform_wecom_identity_mappings m WHERE m.binding_id=b.id
   AND m.identity_epoch=b.identity_epoch AND m.connection_generation<b.identity_verified_generation);

-- +goose StatementBegin
CREATE FUNCTION platform_wecom_on_identity_confirmed() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 PERFORM platform_wecom_carry_contacts(NEW,OLD.identity_verified_generation,NEW.connection_generation);
 RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER platform_wecom_confirmed_contacts AFTER UPDATE OF identity_verified_generation ON platform_channel_bindings
FOR EACH ROW WHEN (NEW.provider='wecom' AND NEW.enabled AND NEW.app_id=NEW.external_tenant_id AND NEW.app_id=NEW.bot_open_id
 AND NEW.identity_verified_generation=NEW.connection_generation AND OLD.identity_verified_generation<>NEW.identity_verified_generation)
EXECUTE FUNCTION platform_wecom_on_identity_confirmed();

-- +goose Down
DROP TRIGGER platform_wecom_confirmed_contacts ON platform_channel_bindings;
DROP FUNCTION platform_wecom_on_identity_confirmed();
DROP FUNCTION platform_wecom_carry_contacts(platform_channel_bindings,bigint,bigint);
