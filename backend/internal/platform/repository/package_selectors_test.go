package repository

import (
	"context"
	"database/sql"
	"errors"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"testing"
)

func TestPackageSelectorsChannelSnapshotAndRevocation(t *testing.T) {
	db, _, directory, b, actor, _ := directoryFixture(t)
	publishDirectoryFixture(t, directory, b)
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE platform_channel_bindings SET access='{"mode":"all"}' WHERE id=$1`, b.ID)
	principal, conversation, session, input, turn := service.NewID(), service.NewID(), service.NewID(), service.NewID(), service.NewID()
	exec(`INSERT INTO manager_channel_principals(id,workspace_id,binding_id,provider,external_tenant_id,external_user_id,identity_epoch) VALUES($1,$2,$3,'feishu',$4,'ou_a',$5)`, principal, b.WorkspaceID, b.ID, b.TenantID, b.IdentityEpoch)
	exec(`INSERT INTO manager_conversations(id,workspace_id,agent_id,channel_created_by,channel_binding_id,external_chat_id,channel_chat_type,channel_tenant_id,channel_identity_epoch) VALUES($1,$2,$3,$4,$5,'chat','direct',$6,$7)`, conversation, b.WorkspaceID, b.AgentID, principal, b.ID, b.TenantID, b.IdentityEpoch)
	exec(`INSERT INTO manager_sessions(id,workspace_id,agent_id,conversation_id,channel_created_by,mode,configuration) VALUES($1,$2,$3,$4,$5,'chat','{}')`, session, b.WorkspaceID, b.AgentID, conversation, principal)
	exec(`INSERT INTO manager_inputs(id,workspace_id,session_id,channel_principal_id,channel_generation,admission_epoch,seq,text) SELECT $1,$2,$3,$4,$5,admission_epoch,1,'hello' FROM platform_agents WHERE id=$6`, input, b.WorkspaceID, session, principal, b.Generation, b.AgentID)
	exec(`INSERT INTO manager_turns(id,workspace_id,session_id,input_id,turn_no,assignment_id,generation,status) VALUES($1,$2,$3,$4,1,$5,1,'running')`, turn, b.WorkspaceID, session, input, service.NewID())
	exec(`UPDATE manager_sessions SET active_turn_id=$2 WHERE id=$1`, session, turn)
	exec(`UPDATE manager_inputs SET status='started',turn_id=$2 WHERE id=$1`, input, turn)
	c := service.ChannelExecution{PrincipalID: principal, ConversationID: conversation, Generation: b.Generation, InputID: input}
	run := service.WithChannelExecution(ctx, c)
	packs := &service.PackageService{Store: NewPackageStore(db)}
	ref := service.UsageSelector{Channel: "feishu", Kind: "user", ID: "ou_a", BindingID: b.ID, IdentityEpoch: b.IdentityEpoch}
	scope := service.PackageScope{Mode: "whitelist_with_blacklist", Allow: []service.UsageSelector{ref, {Channel: "web", Kind: "user", ID: actor}}}
	pack, err := packs.Create(ctx, b.WorkspaceID, actor, b.AgentID, service.PackageCreate{Name: "Mixed people", Scope: &scope})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := func() service.ExecutionCapabilities {
		t.Helper()
		tx, e := db.BeginTx(ctx, nil)
		if e != nil {
			t.Fatal(e)
		}
		defer func() {
			if e := tx.Rollback(); e != nil && !errors.Is(e, sql.ErrTxDone) {
				t.Error(e)
			}
		}()
		v, e := SnapshotCapabilities(run, tx, b.WorkspaceID, principal, b.AgentID)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	snap := snapshot()
	if len(snap.Packages) != 1 {
		t.Fatal("selected channel excluded", snap)
	}
	runtime := &service.RuntimeService{Packages: packs}
	effective := func(want int) {
		t.Helper()
		v, e := runtime.Effective(run, b.WorkspaceID, principal, b.AgentID, &snap)
		if e != nil || len(v) != want {
			t.Fatalf("effective=%d want=%d err=%v", len(v), want, e)
		}
	}
	effective(1)
	scope.Deny = []service.UsageSelector{ref}
	pack, err = packs.Update(ctx, b.WorkspaceID, actor, b.AgentID, pack.ID, service.PackagePatch{Version: pack.Version, Scope: &scope})
	if err != nil {
		t.Fatal(err)
	}
	effective(0)
	if len(snapshot().Packages) != 0 {
		t.Fatal("denied channel admitted")
	}
	scope.Deny = nil
	scope.Allow = []service.UsageSelector{{Channel: "feishu", Kind: "department", ID: "od_team", BindingID: b.ID, IdentityEpoch: b.IdentityEpoch, IncludeDescendants: true}}
	pack, err = packs.Update(ctx, b.WorkspaceID, actor, b.AgentID, pack.ID, service.PackagePatch{Version: pack.Version, Scope: &scope})
	if err != nil {
		t.Fatal(err)
	}
	effective(1)
	scope.Mode = "inherit_with_blacklist"
	scope.Allow = nil
	scope.Deny = []service.UsageSelector{{Channel: "feishu", Kind: "department", ID: "od_child", BindingID: b.ID, IdentityEpoch: b.IdentityEpoch}}
	pack, err = packs.Update(ctx, b.WorkspaceID, actor, b.AgentID, pack.ID, service.PackagePatch{Version: pack.Version, Scope: &scope})
	if err != nil {
		t.Fatal(err)
	}
	effective(1)
	exec(`UPDATE platform_channel_directory_state SET expires_at=now()-interval '1 second' WHERE binding_id=$1`, b.ID)
	effective(0)
	stale := ref
	stale.IdentityEpoch++
	scope.Deny = nil
	scope.Allow = []service.UsageSelector{stale}
	if _, err = packs.Update(ctx, b.WorkspaceID, actor, b.AgentID, pack.ID, service.PackagePatch{Version: pack.Version, Scope: &scope}); err == nil {
		t.Fatal("stale epoch saved")
	}
	if _, err = packs.Create(ctx, b.WorkspaceID, actor, "", service.PackageCreate{Name: "Template with employee identity", Scope: &scope}); err == nil {
		t.Fatal("template accepts employee identity")
	}
}
