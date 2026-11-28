package repository

import (
	"context"
	"testing"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestPackageConversationGroupCategoriesAndDeny(t *testing.T) {
	src := packageConversationSource{channel: "feishu", binding: "binding", epoch: 2, person: "person", group: "group", otherBindingEpochs: map[string]int64{"ding-binding": 2}}
	person := service.UsageSelector{Channel: "feishu", BindingID: "binding", IdentityEpoch: 2, Kind: "user", ID: "person"}
	group := service.UsageSelector{Channel: "feishu", BindingID: "binding", IdentityEpoch: 2, Kind: "group", ID: "group"}
	stale := person
	stale.IdentityEpoch = 1
	staleGroup := group
	staleGroup.IdentityEpoch = 1
	staleOtherGroup := staleGroup
	staleOtherGroup.Channel, staleOtherGroup.BindingID = "dingtalk", "ding-binding"
	otherGroup := group
	otherGroup.ID = "other"
	otherChannel := person
	otherChannel.Channel = "dingtalk"
	for _, tc := range []struct {
		name, mode  string
		allow, deny []service.UsageSelector
		want        bool
	}{
		{name: "empty allow denies", mode: "allowlist"},
		{name: "person only", mode: "allowlist", allow: []service.UsageSelector{person}, want: true},
		{name: "group only", mode: "allowlist", allow: []service.UsageSelector{group}, want: true},
		{name: "both match", mode: "allowlist", allow: []service.UsageSelector{person, group}, want: true},
		{name: "person cannot bypass unmatched group", mode: "allowlist", allow: []service.UsageSelector{person, otherGroup}},
		{name: "group cannot bypass unmatched person", mode: "allowlist", allow: []service.UsageSelector{otherChannel, group}},
		{name: "stale cannot grant", mode: "allowlist", allow: []service.UsageSelector{stale}},
		{name: "replaced binding deny is ignored", mode: "inherit_with_blacklist", deny: []service.UsageSelector{stale}, want: true},
		{name: "current deny still wins", mode: "inherit_with_blacklist", deny: []service.UsageSelector{stale, person}},
		{name: "replaced group is dropped from whitelist intersection", mode: "allowlist", allow: []service.UsageSelector{person, staleGroup}, want: true},
		{name: "replaced group on another binding is dropped", mode: "allowlist", allow: []service.UsageSelector{person, staleOtherGroup}, want: true},
		{name: "group deny blocks", mode: "inherit_with_blacklist", deny: []service.UsageSelector{group}},
		{name: "other channel deny irrelevant", mode: "inherit_with_blacklist", deny: []service.UsageSelector{otherChannel}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var tx packageTx
			got, err := tx.packageConversationRuleAllows(context.Background(), src, service.PackageConversationRule{
				Enabled: true, Mode: tc.mode, Allow: tc.allow, Deny: tc.deny,
			})
			if err != nil || got != tc.want {
				t.Fatalf("allowed=%v want=%v err=%v", got, tc.want, err)
			}
		})
	}
}

func TestPackageConversationSceneRuntimeSnapshotAndDirectory(t *testing.T) {
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
	run := service.WithChannelExecution(ctx, service.ChannelExecution{PrincipalID: principal, ConversationID: conversation, Generation: b.Generation, InputID: input})
	store := NewPackageStore(db)
	packs := &service.PackageService{Store: store}
	disableDefaultEmployeePackage(t, packs, b.WorkspaceID, actor, b.AgentID)
	dept := service.UsageSelector{Channel: "feishu", Kind: "department", ID: "od_team", BindingID: b.ID, IdentityEpoch: b.IdentityEpoch, IncludeDescendants: true}
	scope := service.PackageConversationScope{
		Private: service.PackageConversationRule{Enabled: true, Mode: "allowlist", Allow: []service.UsageSelector{dept}},
		Group:   service.PackageConversationRule{Mode: "inherit_with_blacklist"},
	}
	pack, err := packs.Create(ctx, b.WorkspaceID, actor, b.AgentID, service.PackageCreate{Name: "By conversation scene", ConversationScope: &scope})
	if err != nil {
		t.Fatal(err)
	}
	var snap service.ExecutionCapabilities
	if err := store.WithWorkspace(ctx, b.WorkspaceID, func(tx service.PackageTx) error {
		var err error
		snap, err = SnapshotCapabilities(run, tx.(*packageTx).model.tx, b.WorkspaceID, principal, b.AgentID)
		return err
	}); err != nil || len(snap.Packages) != 1 {
		t.Fatalf("snapshot expected private directory match: %d %v", len(snap.Packages), err)
	}
	runtime := &service.RuntimeService{Packages: packs}
	effective := func(want int) {
		t.Helper()
		res, err := runtime.Effective(run, b.WorkspaceID, principal, b.AgentID, &snap)
		if err != nil || len(res) != want {
			t.Fatalf("effective=%d want=%d err=%v", len(res), want, err)
		}
	}
	effective(1)
	scope.Private.Enabled = false
	pack, err = packs.Update(ctx, b.WorkspaceID, actor, b.AgentID, pack.ID, service.PackagePatch{Version: pack.Version, ConversationScope: &scope})
	if err != nil {
		t.Fatal(err)
	}
	effective(0)
	scope.Private = service.PackageConversationRule{Enabled: true, Mode: "inherit_with_blacklist"}
	pack, err = packs.Update(ctx, b.WorkspaceID, actor, b.AgentID, pack.ID, service.PackagePatch{Version: pack.Version, ConversationScope: &scope})
	if err != nil {
		t.Fatal(err)
	}
	// Widening the current scope must not repair a now-unverifiable pinned allow.
	exec(`UPDATE platform_channel_directory_state SET expires_at=now()-interval '1 second' WHERE binding_id=$1`, b.ID)
	effective(0)
	dept.ID = "od_child"
	pack.ConversationScope.Private.Deny = []service.UsageSelector{dept}
	check := func(actor string, p service.CapabilityPackage, want bool) {
		t.Helper()
		err := store.WithWorkspace(ctx, b.WorkspaceID, func(tx service.PackageTx) error {
			ok, err := tx.(*packageTx).PackageConversationScopeAllows(run, p, actor)
			if err != nil || ok != want {
				t.Fatalf("scene allowed=%v want=%v err=%v", ok, want, err)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	check(principal, pack, false) // Unknown directory deny fails closed.
	pack.ConversationScope.Private.Deny = nil
	check(service.NewID(), pack, false) // Runtime actor cannot stand in for principal.
	check(principal, pack, true)
	exec(`UPDATE manager_conversations SET channel_chat_type='group' WHERE id=$1`, conversation)
	check(principal, pack, false) // Actual conversation changes scene, not caller input.
	pack.ConversationScope.Group.Enabled = true
	check(principal, pack, true)
}

func TestPackageConversationManualSpacePersonalScene(t *testing.T) {
	actor := service.NewID()
	scope := &service.PackageConversationScope{Private: service.PackageConversationRule{Enabled: true, Mode: "allowlist", Allow: []service.UsageSelector{{Channel: "web", Kind: "user", ID: actor}}}, Group: service.PackageConversationRule{Enabled: false, Mode: "allowlist"}}
	p := service.CapabilityPackage{Enabled: true, ConversationScope: scope}
	var tx packageTx
	ctx := service.WithTaskSpaceExecution(context.Background(), service.TaskSpaceExecution{TaskSpaceID: service.NewID(), InputID: service.NewID()})
	// This is only the package layer; real runtime must additionally authorize the space/input.
	ok, e := tx.PackageConversationScopeAllows(ctx, p, actor)
	if e != nil || !ok {
		t.Fatal("personal scene denied", ok, e)
	}
	p.ConversationScope.Private.Enabled = false
	ok, e = tx.PackageConversationScopeAllows(ctx, p, actor)
	if e != nil || ok {
		t.Fatal("private switch bypassed", ok, e)
	}
}

func TestPackageConversationGroupUsesPickerEligibility(t *testing.T) {
	db, _, _, b, actor, _ := directoryFixture(t)
	ctx := context.Background()
	cache := &ChannelGroupCache{DB: db}
	if _, err := cache.PublishGroups(ctx, b, actor, service.ChannelGroupPage{Items: []service.ChannelGroupItem{{ID: "oc_new_group", Name: "New group"}}}); err != nil {
		t.Fatal(err)
	}
	packages := &service.PackageService{Store: NewPackageStore(db)}
	ref := service.UsageSelector{Channel: "feishu", Kind: "group", ID: "oc_new_group", BindingID: b.ID, IdentityEpoch: b.IdentityEpoch}
	scope := service.PackageConversationScope{
		Private: service.PackageConversationRule{Mode: "inherit_with_blacklist"},
		Group:   service.PackageConversationRule{Enabled: true, Mode: "allowlist", Allow: []service.UsageSelector{ref}},
	}
	if _, err := packages.Create(ctx, b.WorkspaceID, actor, b.AgentID, service.PackageCreate{Name: "Discovered group", ConversationScope: &scope}); err != nil {
		t.Fatal("valid Feishu group should not need a previously observed message", err)
	}
	reviewExec(t, db, `UPDATE platform_channel_group_candidates SET expires_at=now()-interval '1 second' WHERE binding_id=$1`, b.ID)
	if _, err := packages.Create(ctx, b.WorkspaceID, actor, b.AgentID, service.PackageCreate{Name: "Expired candidate", ConversationScope: &scope}); err == nil {
		t.Fatal("expired discovery must not create a new grant")
	}
	groupService := &service.WebGroupService{Store: &WebGroupStore{DB: db}}
	group, err := groupService.Save(ctx, b.WorkspaceID, actor, "", service.WebGroupPut{Title: "Private unrelated group"})
	if err != nil {
		t.Fatal(err)
	}
	scope.Group.Allow = []service.UsageSelector{{Channel: "web", Kind: "group", ID: group.ID}}
	if _, err := packages.Create(ctx, b.WorkspaceID, actor, b.AgentID, service.PackageCreate{Name: "Unrelated group", ConversationScope: &scope}); err == nil {
		t.Fatal("unrelated Web group must not create a new grant")
	}
}
