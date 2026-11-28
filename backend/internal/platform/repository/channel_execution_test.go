package repository

import (
	"context"
	"database/sql"
	"errors"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"testing"
)

func TestChannelRuntimeIdentityAndPackageIntersection(t *testing.T) {
	db, models, w, owner := modelFixture(t)
	ctx := context.Background()
	model := saveTestModel(t, models, w, owner)
	employee, err := (&service.AgentService{Store: NewAgentStore(db)}).Create(ctx, w, owner, service.AgentCreate{Name: "Channel worker", ModelProfileID: model.ID})
	if err != nil {
		t.Fatal(err)
	}
	binding, principal, conversation, session, input, turn := service.NewID(), service.NewID(), service.NewID(), service.NewID(), service.NewID(), service.NewID()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO platform_channel_bindings(id,workspace_id,agent_id,provider,connection_generation,rules_version,config_revision,enabled,app_id,external_tenant_id,bot_open_id,credential_ciphertext,trigger,history,access) VALUES($1,$2,$3,'feishu',1,1,1,true,'app-one','tenant-one','bot', '\x01','{}','{}','{"mode":"all"}')`, binding, w, employee.ID)
	exec(`INSERT INTO manager_channel_principals(id,workspace_id,binding_id,provider,external_tenant_id,external_user_id) VALUES($1,$2,$3,'feishu','tenant-one','open-one')`, principal, w, binding)
	exec(`INSERT INTO manager_conversations(id,workspace_id,agent_id,channel_created_by,channel_binding_id,external_chat_id,channel_chat_type,channel_tenant_id) VALUES($1,$2,$3,$4,$5,'chat-one','direct','tenant-one')`, conversation, w, employee.ID, principal, binding)
	exec(`INSERT INTO manager_sessions(id,workspace_id,agent_id,conversation_id,channel_created_by,mode,configuration) VALUES($1,$2,$3,$4,$5,'chat','{}')`, session, w, employee.ID, conversation, principal)
	exec(`INSERT INTO manager_inputs(id,workspace_id,session_id,channel_principal_id,channel_generation,admission_epoch,seq,text) SELECT $1,$2,$3,$4,1,admission_epoch,1,'hello' FROM platform_agents WHERE id=$5`, input, w, session, principal, employee.ID)
	exec(`INSERT INTO manager_turns(id,workspace_id,session_id,input_id,turn_no,assignment_id,generation,status) VALUES($1,$2,$3,$4,1,$5,1,'running')`, turn, w, session, input, service.NewID())
	exec(`UPDATE manager_sessions SET active_turn_id=$2 WHERE id=$1`, session, turn)
	exec(`UPDATE manager_inputs SET status='started',turn_id=$2 WHERE id=$1`, input, turn)
	identity := service.ChannelExecution{PrincipalID: principal, ConversationID: conversation, Generation: 1, InputID: input}
	channelCtx := service.WithChannelExecution(ctx, identity)
	key := func(c context.Context, actor string) (string, error) {
		return models.RuntimeModelKey(c, w, actor, model.ID, model.Protocol, model.BaseURL, model.ModelID)
	}
	if got, e := key(channelCtx, principal); e != nil || got != "test-private-api-key" {
		t.Fatalf("independent channel cannot use model: %v", e)
	}
	exec(`UPDATE platform_members SET status='disabled' WHERE workspace_id=$1 AND user_id=$2`, w, owner)
	if _, e := key(channelCtx, principal); e != nil {
		t.Fatal("channel depends on owner membership", e)
	}
	exec(`UPDATE platform_members SET status='active' WHERE workspace_id=$1 AND user_id=$2`, w, owner)
	if _, e := key(ctx, principal); e == nil {
		t.Fatal("channel treated as Web member")
	}
	if _, e := key(channelCtx, owner); e == nil {
		t.Fatal("owner substituted for channel identity")
	}
	var members int
	if e := db.QueryRow(`SELECT count(*) FROM platform_members WHERE user_id=$1`, principal).Scan(&members); e != nil || members != 0 {
		t.Fatal("channel got Web membership", e)
	}
	for name, mutate := range map[string]func(*service.ChannelExecution){
		"other input":        func(c *service.ChannelExecution) { c.InputID = service.NewID() },
		"other conversation": func(c *service.ChannelExecution) { c.ConversationID = service.NewID() },
		"stale generation":   func(c *service.ChannelExecution) { c.Generation++ },
		"no input":           func(c *service.ChannelExecution) { c.InputID = "" },
		"other principal":    func(c *service.ChannelExecution) { c.PrincipalID = service.NewID() },
	} {
		t.Run(name, func(t *testing.T) {
			c := identity
			mutate(&c)
			if _, e := key(service.WithChannelExecution(ctx, c), c.PrincipalID); e == nil {
				t.Fatal("invalid runtime identity accepted")
			}
		})
	}
	for _, change := range []struct {
		name, query, restore string
		args                 []any
	}{
		{"binding disabled", `UPDATE platform_channel_bindings SET enabled=false WHERE id=$1`, `UPDATE platform_channel_bindings SET enabled=true WHERE id=$1`, []any{binding}},
		{"tenant not verified", `UPDATE platform_channel_bindings SET external_tenant_id='' WHERE id=$1`, `UPDATE platform_channel_bindings SET external_tenant_id='tenant-one' WHERE id=$1`, []any{binding}},
		{"tenant replaced", `UPDATE platform_channel_bindings SET external_tenant_id='tenant-two' WHERE id=$1`, `UPDATE platform_channel_bindings SET external_tenant_id='tenant-one' WHERE id=$1`, []any{binding}},
		{"user revoked", `UPDATE platform_channel_bindings SET access='{"mode":"all","deny_ids":["open-one"]}' WHERE id=$1`, `UPDATE platform_channel_bindings SET access='{"mode":"all"}' WHERE id=$1`, []any{binding}},
		{"employee disabled", `UPDATE platform_agents SET enabled=false WHERE id=$1`, `UPDATE platform_agents SET enabled=true WHERE id=$1`, []any{employee.ID}},
		{"input not running", `UPDATE manager_inputs SET status='queued' WHERE id=$1`, `UPDATE manager_inputs SET status='started' WHERE id=$1`, []any{input}},
	} {
		t.Run(change.name, func(t *testing.T) {
			exec(change.query, change.args...)
			defer exec(change.restore, change.args...)
			if _, e := key(channelCtx, principal); e == nil {
				t.Fatal("revoked runtime retained key")
			}
		})
	}
	packs := &service.PackageService{Store: NewPackageStore(db)}
	disableDefaultEmployeePackage(t, packs, w, owner, employee.ID)
	pack, e := packs.Create(ctx, w, owner, employee.ID, service.PackageCreate{Name: "Inherited"})
	if e != nil {
		t.Fatal(e)
	}
	runtime := &service.RuntimeService{Packages: packs}
	snapshot := &service.ExecutionCapabilities{Packages: []service.CapabilityPackage{pack}}
	effective := func() []service.CapabilityPackage {
		t.Helper()
		p, e := runtime.Effective(channelCtx, w, principal, employee.ID, snapshot)
		if e != nil {
			t.Fatal(e)
		}
		return p
	}
	if len(effective()) != 1 {
		t.Fatal("unqualified inheritance denied")
	}
	snapshot.Packages[0].Skills = []service.PackageSkill{{SkillID: service.NewID(), VersionID: service.NewID()}}
	if got := effective(); len(got) != 1 || len(got[0].Skills) != 0 {
		t.Fatal("removed resources retained in intersection")
	}
	snapshot.Packages[0] = pack

	scope := service.PackageScope{Mode: "whitelist_with_blacklist", AllowMemberIDs: []string{owner}}
	pack, e = packs.Update(ctx, w, owner, employee.ID, pack.ID, service.PackagePatch{Version: pack.Version, Scope: &scope})
	if e != nil {
		t.Fatal(e)
	}
	if len(effective()) != 0 {
		t.Fatal("Web whitelist gave channel authority")
	}
	snapshot.Packages = []service.CapabilityPackage{pack}
	scope = service.PackageScope{Mode: "inherit_with_blacklist"}
	pack, e = packs.Update(ctx, w, owner, employee.ID, pack.ID, service.PackagePatch{Version: pack.Version, Scope: &scope})
	if e != nil {
		t.Fatal(e)
	}
	if len(effective()) != 0 {
		t.Fatal("current broadening expanded frozen grant")
	}
	snapshot.Packages = []service.CapabilityPackage{pack}
	scope.DenyMemberIDs = []string{owner}
	pack, e = packs.Update(ctx, w, owner, employee.ID, pack.ID, service.PackagePatch{Version: pack.Version, Scope: &scope})
	if e != nil {
		t.Fatal(e)
	}
	if len(effective()) != 0 {
		t.Fatal("unresolvable Web blacklist bypassed")
	}
	admission := identity
	admission.InputID = ""
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if e := tx.Rollback(); e != nil && !errors.Is(e, sql.ErrTxDone) {
			t.Error(e)
		}
	}()
	snap, e := SnapshotCapabilities(service.WithChannelExecution(ctx, admission), tx, w, principal, employee.ID)
	if e != nil || len(snap.Packages) != 0 {
		t.Fatal("channel snapshot included Web-scoped package", e)
	}
}
