package repository

import (
	"os"
	"strings"
	"testing"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestWecomContactUpgradeRestoresOnlyUnambiguousHistoricalPairs(t *testing.T) {
	db, models, workspace, actor := modelFixtureThrough(t, "000075_shared_email_invitations.sql")
	model := saveTestModel(t, models, workspace, actor)
	agent, binding := service.NewID(), service.NewID()
	reviewExec(t, db, `INSERT INTO platform_agents(id,workspace_id,name,model_profile_id,version,config_version,admission_epoch) VALUES($1,$2,'bot',$3,1,1,1)`, agent, workspace, model.ID)
	reviewExec(t, db, `INSERT INTO platform_channel_bindings(id,workspace_id,agent_id,provider,enabled,connection_generation,rules_version,config_revision,app_id,bot_open_id,external_tenant_id,credential_ciphertext,trigger,history,access,identity_verified_generation)
	VALUES($1,$2,$3,'wecom',true,5,1,1,'bot','bot','bot',''::bytea,'{}','{}','{}',4)`, binding, workspace, agent)
	for _, pair := range []struct {
		gen              int
		search, callback string
	}{
		{1, "keep", "callback"}, {2, "keep", "callback"},
		{1, "changed", "old-user"}, {2, "changed", "new-user"},
		{1, "old-person", "same-user"}, {2, "different-person", "same-user"},
		{4, "current", "verified-now"},
	} {
		id := service.NewID()
		reviewExec(t, db, `INSERT INTO platform_wecom_verifications(id,binding_id,identity_epoch,connection_generation,search_user_id,created_by,code_hash,code_ciphertext,status,callback_user_id,expires_at) VALUES($1::uuid,$2,1,$3,$4,$5,$1::uuid::text,''::bytea,'verified',$6,now())`, id, binding, pair.gen, pair.search, actor, pair.callback)
		reviewExec(t, db, `INSERT INTO platform_wecom_identity_mappings(binding_id,identity_epoch,connection_generation,search_user_id,callback_user_id,verification_id) VALUES($1,1,$2,$3,$4,$5)`, binding, pair.gen, pair.search, pair.callback, id)
	}
	reviewExec(t, db, `INSERT INTO platform_wecom_contacts(binding_id,identity_epoch,connection_generation,search_user_id,display_name,expires_at) VALUES($1,1,1,'keep','old display',now()-interval '1 day'),($1,1,2,'keep','last verified display',now()-interval '1 hour')`, binding)
	src, err := os.ReadFile("../../../../event-hub/database/postgres/migrations/000076_wecom_verified_contacts.sql")
	if err != nil {
		t.Fatal(err)
	}
	reviewExec(t, db, strings.Split(string(src), "-- +goose Down")[0])
	check := func(gen, want int) {
		t.Helper()
		var count int
		if err := db.QueryRow(`SELECT count(*) FROM platform_wecom_identity_mappings WHERE binding_id=$1 AND connection_generation=$2`, binding, gen).Scan(&count); err != nil || count != want {
			t.Fatal(gen, count, err)
		}
	}
	check(4, 2)
	check(5, 0)
	reviewExec(t, db, `UPDATE platform_channel_bindings SET identity_verified_generation=connection_generation WHERE id=$1`, binding)
	check(5, 2)
	var retained bool
	if err = db.QueryRow(`SELECT display_name='last verified display' AND expires_at<now() FROM platform_wecom_contacts WHERE binding_id=$1 AND connection_generation=5 AND search_user_id='keep'`, binding).Scan(&retained); err != nil || !retained {
		t.Fatal(retained, err)
	}
}
