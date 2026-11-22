package postgres

import (
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/unrealm-ai/kikoteam/event-hub/internal/testpg"
)

func TestChannelDeliveryFormatUpgradePreservesQueuedText(t *testing.T) {
	_, db := testpg.New(t)
	c, ctx := borrowed(t, db)
	old := fstest.MapFS{}
	entries, err := fs.ReadDir(Files(), ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".sql") || entry.Name() >= "000059" {
			continue
		}
		raw, e := fs.ReadFile(Files(), entry.Name())
		if e != nil {
			t.Fatal(e)
		}
		old[entry.Name()] = &fstest.MapFile{Data: raw}
	}
	if err = Migrate(ctx, c, old, quiet()); err != nil {
		t.Fatal(err)
	}
	_, err = c.ExecContext(ctx, `DO $$ DECLARE w uuid:=gen_random_uuid(); a uuid:=gen_random_uuid(); m uuid:=gen_random_uuid(); b uuid:=gen_random_uuid(); n uuid:=gen_random_uuid(); BEGIN
 INSERT INTO platform_workspaces(id,name) VALUES(w,'delivery-upgrade');
 INSERT INTO platform_model_profiles(id,workspace_id,name,protocol,base_url,model_id,version,has_key,validated,credential,credential_revision,vision_status,tested_at) VALUES(m,w,'test','openai_responses','https://example.invalid','test',1,false,true,''::bytea,1,'unknown',now());
 INSERT INTO platform_agents(id,workspace_id,name,model_profile_id,version,config_version,admission_epoch,usage_config) VALUES(a,w,'test',m,1,1,1,'{"access":{"mode":"all","allow":[],"deny":[]}}');
 INSERT INTO platform_channel_bindings(id,workspace_id,agent_id,provider,connection_generation,rules_version,config_revision,app_id,credential_ciphertext,trigger,history,access) VALUES(b,w,a,'feishu',1,1,1,'app',''::bytea,'{}','{}','{}');
 INSERT INTO manager_channel_inbox(id,binding_id,connection_generation,config_revision,rules_version,provider_event_id,provider_message_id,fingerprint,external_tenant_id,external_chat_id,chat_type,sender_id,text,message_type,bot_mentioned) VALUES(n,b,1,1,1,'evt','msg','hash','tenant','chat','direct','sender','请求','text',false);
 INSERT INTO manager_channel_deliveries(id,inbox_id,binding_id,connection_generation,kind,text) VALUES(gen_random_uuid(),n,b,1,'reply','**存量正文** ✅');
 END $$;`)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = Migrate(ctx, c, Files(), quiet()); err != nil {
			t.Fatal(err)
		}
	}
	var format, status, body string
	if err = c.QueryRowContext(ctx, `SELECT content_format,status,text FROM manager_channel_deliveries`).Scan(&format, &status, &body); err != nil || format != "text" || status != "pending" || body != "**存量正文** ✅" {
		t.Fatal(format, status, body, err)
	}
}
