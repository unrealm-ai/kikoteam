package postgres

import (
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/unrealm-ai/kikoteam/event-hub/internal/testpg"
)

func TestTaskSpaceUpgrade36PreservesConversationData(t *testing.T) {
	_, db := testpg.New(t)
	c, ctx := borrowed(t, db)
	old := fstest.MapFS{}
	entries, err := fs.ReadDir(Files(), ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".sql") && entry.Name() < "000037" {
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
	_, err = c.ExecContext(ctx, `DO $$ DECLARE w uuid:=gen_random_uuid(); u uuid:=gen_random_uuid(); a uuid:=gen_random_uuid(); m uuid:=gen_random_uuid(); conv uuid:=gen_random_uuid(); chat uuid:=gen_random_uuid(); task uuid:=gen_random_uuid(); sess uuid:=gen_random_uuid(); engine uuid:=gen_random_uuid(); BEGIN
 INSERT INTO platform_accounts(id,email,name) VALUES(u,'upgrade@example.test','Upgrade');
 INSERT INTO platform_workspaces(id,name)VALUES(w,'upgrade');
 INSERT INTO platform_members(workspace_id,user_id,role)VALUES(w,u,'owner');
 INSERT INTO platform_model_profiles(id,workspace_id,name,protocol,base_url,model_id,version,has_key,validated,credential,credential_revision,vision_status,tested_at)VALUES(m,w,'test','openai_responses','https://example.invalid','test',1,false,true,''::bytea,1,'unknown',now());
 INSERT INTO platform_agents(id,workspace_id,name,model_profile_id,version,config_version,admission_epoch,usage_config)VALUES(a,w,'test',m,1,1,1,'{"access":{"mode":"all","allow":[],"deny":[]}}');
 INSERT INTO manager_conversations(id,workspace_id,agent_id,created_by)VALUES(conv,w,a,u);
 INSERT INTO manager_sessions(id,workspace_id,agent_id,conversation_id,created_by,mode,configuration)VALUES(chat,w,a,conv,u,'chat','{}');
 INSERT INTO manager_tasks(id,workspace_id,conversation_id,source_chat_session_id)VALUES(task,w,conv,chat);
 INSERT INTO manager_sessions(id,workspace_id,agent_id,conversation_id,task_id,created_by,mode,configuration,native_host_id,native_session_id)VALUES(sess,w,a,conv,task,u,'task','{}','host-before','native-before');
 INSERT INTO manager_inputs(id,workspace_id,session_id,submitted_by,admission_epoch,seq,text)VALUES(gen_random_uuid(),w,sess,u,1,1,'preserve me');
 INSERT INTO manager_engines(id,host_id,endpoint,capacity,accepting)VALUES(engine,'host-before','http://localhost:9000',4,false);
 INSERT INTO manager_conversation_sandboxes(id,workspace_id,conversation_id,manager_engine_id,host_id,state)VALUES(gen_random_uuid(),w,conv,engine,'host-before','stopped');
 END $$;`)
	if err != nil {
		t.Fatal(err)
	}
	var before string
	if err = c.QueryRowContext(ctx, `SELECT jsonb_build_object('tasks',(SELECT jsonb_agg(to_jsonb(k)) FROM manager_tasks k),'sessions',(SELECT jsonb_agg(to_jsonb(s) ORDER BY s.id) FROM manager_sessions s),'inputs',(SELECT jsonb_agg(to_jsonb(i)) FROM manager_inputs i),'sandboxes',(SELECT jsonb_agg(to_jsonb(b)) FROM manager_conversation_sandboxes b))::text`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = Migrate(ctx, c, Files(), quiet()); err != nil {
			t.Fatal(err)
		}
	}
	// Compare every pre-upgrade column, independently of newly added nullable
	// fields. Missing/changed old columns or rows still fail exact JSON equality.
	var after string
	if err = c.QueryRowContext(ctx, `SELECT jsonb_build_object('tasks',(SELECT jsonb_agg((SELECT jsonb_object_agg(e.key,e.value) FROM jsonb_each(to_jsonb(k)) e WHERE ($1::jsonb->'tasks'->0)?e.key)) FROM manager_tasks k),'sessions',(SELECT jsonb_agg((SELECT jsonb_object_agg(e.key,e.value) FROM jsonb_each(to_jsonb(s)) e WHERE ($1::jsonb->'sessions'->0)?e.key) ORDER BY s.id) FROM manager_sessions s),'inputs',(SELECT jsonb_agg((SELECT jsonb_object_agg(e.key,e.value) FROM jsonb_each(to_jsonb(i)) e WHERE ($1::jsonb->'inputs'->0)?e.key)) FROM manager_inputs i),'sandboxes',(SELECT jsonb_agg((SELECT jsonb_object_agg(e.key,e.value) FROM jsonb_each(to_jsonb(b)) e WHERE ($1::jsonb->'sandboxes'->0)?e.key)) FROM manager_conversation_sandboxes b))::text`, before).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("upgrade rewrote prior conversation/task/runtime data")
	}

	// A clean replay must produce exactly the same constraints/index definitions
	// and migration version as the populated upgrade, including validated flags.
	_, cleanDB := testpg.New(t)
	clean, cleanCtx := borrowed(t, cleanDB)
	if err = Migrate(cleanCtx, clean, Files(), quiet()); err != nil {
		t.Fatal(err)
	}
	const catalog = `SELECT jsonb_build_object('version',(SELECT max(version_id) FROM goose_db_version WHERE is_applied),'constraints',(SELECT jsonb_agg(jsonb_build_object('table',c.relname,'name',p.conname,'validated',p.convalidated,'definition',pg_get_constraintdef(p.oid)) ORDER BY c.relname,p.conname) FROM pg_constraint p JOIN pg_class c ON c.oid=p.conrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public'),'indexes',(SELECT jsonb_agg(jsonb_build_object('table',t.relname,'name',x.relname,'valid',i.indisvalid,'ready',i.indisready,'definition',pg_get_indexdef(i.indexrelid)) ORDER BY t.relname,x.relname) FROM pg_index i JOIN pg_class t ON t.oid=i.indrelid JOIN pg_class x ON x.oid=i.indexrelid JOIN pg_namespace n ON n.oid=t.relnamespace WHERE n.nspname='public'))::text`
	var upgradedCatalog, cleanCatalog string
	if err = c.QueryRowContext(ctx, catalog).Scan(&upgradedCatalog); err != nil {
		t.Fatal(err)
	}
	if err = clean.QueryRowContext(cleanCtx, catalog).Scan(&cleanCatalog); err != nil {
		t.Fatal(err)
	}
	if upgradedCatalog != cleanCatalog {
		t.Fatal("populated upgrade constraints/indexes/version differ from clean replay")
	}
	var valid bool
	if err = c.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM manager_task_spaces)=0 AND NOT EXISTS(SELECT 1 FROM manager_tasks k JOIN manager_conversations c ON c.id=k.conversation_id WHERE k.agent_id<>c.agent_id OR k.task_space_id IS NOT NULL) AND (SELECT bool_and(manager_input_actor_allowed(id)) FROM manager_inputs)`).Scan(&valid); err != nil || !valid {
		t.Fatal("backfill or old authorization changed", valid, err)
	}
}
