package postgres

import (
	"github.com/unrealm-ai/kikoteam/event-hub/internal/testpg"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

func TestDirectoryRelationshipUpgrade53PreservesIdentity(t *testing.T) {
	_, db := testpg.New(t)
	c, ctx := borrowed(t, db)
	old := fstest.MapFS{}
	entries, err := fs.ReadDir(Files(), ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".sql") && entry.Name() < "000054" {
			raw, e := fs.ReadFile(Files(), entry.Name())
			if e != nil {
				t.Fatal(e)
			}
			old[entry.Name()] = &fstest.MapFile{Data: raw}
		}
	}
	if err = Migrate(ctx, c, old, quiet()); err != nil {
		t.Fatal(err)
	}
	_, err = c.ExecContext(ctx, `DO $$ DECLARE w uuid:=gen_random_uuid();a uuid:=gen_random_uuid();m uuid:=gen_random_uuid();b uuid:=gen_random_uuid();BEGIN
 INSERT INTO platform_workspaces(id,name)VALUES(w,'directory-upgrade');
 INSERT INTO platform_model_profiles(id,workspace_id,name,protocol,base_url,model_id,version,has_key,validated,credential,credential_revision,vision_status,tested_at)VALUES(m,w,'test','openai_responses','https://example.invalid','test',1,false,true,''::bytea,1,'unknown',now());
 INSERT INTO platform_agents(id,workspace_id,name,model_profile_id,version,config_version,admission_epoch,usage_config)VALUES(a,w,'test',m,1,1,1,'{"access":{"mode":"all","allow":[],"deny":[]}}');
 INSERT INTO platform_channel_bindings(id,workspace_id,agent_id,provider,connection_generation,rules_version,config_revision,app_id,credential_ciphertext,trigger,history,access)VALUES(b,w,a,'dingtalk',1,1,1,'app',''::bytea,'{}','{}','{}');
 INSERT INTO platform_channel_directory_state(binding_id,identity_epoch,connection_generation,external_tenant_id)VALUES(b,1,1,'corp');
 INSERT INTO platform_channel_directory_users(binding_id,user_id,display_name,status,department_ids,memberships_known)VALUES(b,'stable-user','Alice','active','["1"]',true);
 INSERT INTO platform_channel_directory_departments(binding_id,department_id,display_name,parent_id)VALUES(b,'1','Team','');
 END $$;`)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = Migrate(ctx, c, Files(), quiet()); err != nil {
			t.Fatal(err)
		}
	}
	var valid bool
	err = c.QueryRowContext(ctx, `SELECT (SELECT user_id='stable-user' AND display_name='Alice' AND status='active' AND department_ids='["1"]'::jsonb AND memberships_known AND direct_manager_id IS NULL AND NOT direct_manager_known AND aliases='[]'::jsonb FROM platform_channel_directory_users) AND (SELECT department_id='1' AND parent_id='' AND leader_ids='[]'::jsonb AND NOT leaders_known FROM platform_channel_directory_departments)`).Scan(&valid)
	if err != nil || !valid {
		t.Fatal("upgrade changed identity or fabricated relationship facts", valid, err)
	}
}
