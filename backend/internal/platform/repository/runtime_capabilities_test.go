package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/integration/skillasset"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type runtimeProbe struct {
	before      func()
	changed     bool
	extra       bool
	unavailable bool
}

func (p *runtimeProbe) Probe(ctx context.Context, _ service.Plugin, _ string) ([]service.MCPTool, error) {
	if p.before != nil {
		p.before()
	}
	if p.unavailable {
		return nil, errors.New("fixture unavailable")
	}
	schema := json.RawMessage(`{"type":"object","properties":{}}`)
	if p.changed {
		schema = json.RawMessage(`{"type":"object","properties":{"new":{"type":"string"}}}`)
	}
	h := sha256.Sum256(schema)
	d := hex.EncodeToString(h[:])
	tools := []service.MCPTool{{Name: "lookup", InputSchema: schema, SchemaDigest: d}, {Name: "delete", InputSchema: schema, SchemaDigest: d}}
	if p.extra {
		tools = append(tools, service.MCPTool{Name: "third", InputSchema: schema, SchemaDigest: d})
	}
	return tools, ctx.Err()
}

func TestRuntimeCapabilitiesPinsScopesRevocationAndSchema(t *testing.T) {
	db, models, w, actor := modelFixture(t)
	ctx := context.Background()
	model := saveTestModel(t, models, w, actor)
	agent, err := (&service.AgentService{Store: NewAgentStore(db)}).Create(ctx, w, actor, service.AgentCreate{Name: "Runtime", ModelProfileID: model.ID})
	if err != nil {
		t.Fatal(err)
	}
	assets, err := skillasset.NewStore(ctx, skillasset.Config{LocalRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = assets.Close() })
	skills := &service.SkillService{Store: NewSkillStore(db), Assets: assets}
	publish := func(body string, previous service.Skill) service.Skill {
		t.Helper()
		imp, e := skills.Import(ctx, w, actor, service.SkillImportRequest{Source: "manual", Name: "report", Description: "Report", Body: body})
		if e != nil {
			t.Fatal(e)
		}
		req := service.SkillPublish{ImportID: imp.ID, Version: previous.Version}
		if previous.ID == "" {
			req.Name = "Report"
		}
		v, e := skills.Publish(ctx, w, actor, previous.ID, req)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	v1 := publish("immutable-v1", service.Skill{})
	probe := &runtimeProbe{}
	plugins := &service.PluginService{Store: NewPluginStore(db), Secret: []byte(strings.Repeat("r", 40)), Probe: probe}
	plug, err := plugins.Create(ctx, w, actor, service.PluginCreate{Name: "Remote", Endpoint: "https://example.test/mcp", AuthType: "bearer"})
	if err != nil {
		t.Fatal(err)
	}
	secret := "fixture-runtime-credential"
	account, err := plugins.PutAccount(ctx, w, actor, agent.ID, plug.ID, service.PluginAccountPut{Name: "Employee account", PluginVersionID: plug.VersionID, Secret: &secret})
	if err != nil {
		t.Fatal(err)
	}
	packages := &service.PackageService{Store: NewPackageStore(db)}
	pin := service.PackagePlugin{PluginID: plug.ID, VersionID: plug.VersionID}
	p, err := packages.Create(ctx, w, actor, agent.ID, service.PackageCreate{Name: "Allowed", Description: "Original business scope", Skills: []service.PackageSkill{{SkillID: v1.ID, VersionID: v1.CurrentVersion.ID}}, Plugins: []service.PackagePlugin{pin}})
	if err != nil {
		t.Fatal(err)
	}
	other := service.NewID()
	insertWorkspaceAccount(t, db, other, "runtime-other@example.test")
	if _, err = db.ExecContext(ctx, `INSERT INTO platform_members(workspace_id,user_id,role,status) VALUES($1,$2,'member','active')`, w, other); err != nil {
		t.Fatal(err)
	}
	// Both principals may use the employee; capability package intersection
	// below must still exclude the other principal's extra package.
	usage := &service.AgentUsageService{Store: &AgentUsageStore{DB: db}}
	policy, err := usage.Get(ctx, w, actor, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	policy.UsageConfig.Access.Mode = "allowlist"
	policy.UsageConfig.Access.Allow = []service.UsageSelector{{Channel: "web", Kind: "user", ID: actor}, {Channel: "web", Kind: "user", ID: other}}
	if _, err = usage.Update(ctx, w, actor, agent.ID, policy); err != nil {
		t.Fatal(err)
	}
	_, err = packages.Create(ctx, w, actor, agent.ID, service.PackageCreate{Name: "Other member only", Scope: &service.PackageScope{Mode: "whitelist_with_blacklist", AllowMemberIDs: []string{other}}, Plugins: []service.PackagePlugin{{PluginID: plug.ID, VersionID: plug.VersionID}}})
	if err != nil {
		t.Fatal(err)
	}
	store := &managerrepo.Store{DB: db, SnapshotCapabilities: SnapshotCapabilities}
	managerSvc := &manager.Service{Store: store}
	c, err := managerSvc.CreateConversation(ctx, w, actor, manager.CreateConversation{AgentID: agent.ID})
	if err != nil {
		t.Fatal(err)
	}
	public, _, err := managerSvc.CreateSession(ctx, w, actor, "", manager.CreateSession{ConversationID: c.ID, Mode: "task"})
	if err != nil {
		t.Fatal(err)
	}
	if public.Configuration.Capabilities != nil {
		t.Fatal("private package/account snapshot leaked")
	}
	session, err := store.Session(ctx, w, actor, public.ID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := session.Configuration.Capabilities
	if snapshot == nil || len(snapshot.Packages) != 1 || len(snapshot.Accounts) != 1 {
		t.Fatalf("incorrect frozen grants: %+v", snapshot)
	}
	runtime := &service.RuntimeService{Packages: packages, Skills: skills, Plugins: plugins}
	assertGranted := func(caller string, skillVersion string) {
		t.Helper()
		out, e := runtime.Resolve(ctx, w, caller, agent.ID, snapshot, public.ID, true)
		if e != nil {
			t.Fatal(e)
		}
		if len(out.Skills) != 1 || out.Skills[0].VersionID != skillVersion || out.Skills[0].Scope != "workspace" || len(out.Plugins) != 1 || len(out.Plugins[0].Tools) != 2 || out.Plugins[0].Tools[0].Name != "lookup" {
			t.Fatalf("grant expanded/drifted: %+v", out)
		}
		if out.Plugins[0].Headers["Authorization"] != "Bearer "+secret {
			t.Fatal("employee credential not resolved")
		}
		if len(out.PackageSummaries) != 1 || out.PackageSummaries[0] != (service.RuntimePackageSummary{Name: "Allowed", Description: "Original business scope"}) {
			t.Fatalf("wrong package summaries for %s: %+v", caller, out.PackageSummaries)
		}
	}
	assertGranted(actor, v1.CurrentVersion.ID)
	assertGranted(other, v1.CurrentVersion.ID) // The other package never enters this Session.
	v2 := publish("immutable-v2", v1)
	off := false
	secondPack, err := packages.Create(ctx, w, actor, agent.ID, service.PackageCreate{Name: "Additional", Enabled: &off, Skills: []service.PackageSkill{{SkillID: v1.ID}}})
	if err != nil || secondPack.Skills[0].VersionID != v1.CurrentVersionID {
		t.Fatal("publishing drifted the employee pin", secondPack, err)
	}
	err = packages.Upgrade(ctx, w, actor, agent.ID, service.ResourceUpgrade{Kind: "skill", ResourceID: v1.ID, FromVersionID: v1.CurrentVersionID, ToVersionID: v2.CurrentVersionID})
	if err != nil {
		t.Fatal(err)
	}
	p, err = packages.Get(ctx, w, actor, agent.ID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	secondPack, err = packages.Get(ctx, w, actor, agent.ID, secondPack.ID)
	if err != nil || secondPack.Skills[0].VersionID != v2.CurrentVersionID {
		t.Fatal("employee package did not upgrade", secondPack, err)
	}
	probe.extra = true
	assertGranted(actor, v1.CurrentVersion.ID)
	for _, mode := range []string{"chat", "task"} {
		fresh, _, e := managerSvc.CreateSession(ctx, w, actor, "", manager.CreateSession{ConversationID: c.ID, Mode: mode})
		if e != nil {
			t.Fatal(e)
		}
		row, e := store.Session(ctx, w, actor, fresh.ID)
		if e != nil {
			t.Fatal(e)
		}
		caps := row.Configuration.Capabilities
		if caps == nil || len(caps.Packages) != 1 || caps.Packages[0].Skills[0].VersionID != v2.CurrentVersion.ID {
			t.Fatalf("new %s used stale skill: %#v", mode, caps)
		}
		if mode == "task" {
			probe.unavailable = true
			out, e := runtime.Resolve(ctx, w, actor, agent.ID, caps, fresh.ID, true)
			if e != nil || len(out.Plugins) != 0 || len(out.Problems) == 0 {
				t.Fatal("failed discovery", e, out)
			}
			probe.unavailable = false
			out, e = runtime.Resolve(ctx, w, actor, agent.ID, caps, fresh.ID, true)
			if e != nil || len(out.Plugins) != 1 || len(out.Plugins[0].Tools) != 3 {
				t.Fatal("fresh discovery", e, out)
			}
		}
	}
	probe.extra = false
	newName, newDescription := "Renamed", "Updated business scope"
	p, err = packages.Update(ctx, w, actor, agent.ID, p.ID, service.PackagePatch{Version: p.Version, Name: &newName, Description: &newDescription})
	if err != nil {
		t.Fatal(err)
	}
	assertGranted(actor, v1.CurrentVersion.ID)
	probe.changed = true
	out, err := runtime.Resolve(ctx, w, actor, agent.ID, snapshot, public.ID, true)
	if err != nil || len(out.Plugins) != 0 {
		t.Fatal("schema drift authorized", err)
	}
	probe.changed = false
	probe.before = func() {
		probe.before = nil
		off := false
		var e error
		p, e = packages.Update(ctx, w, actor, agent.ID, p.ID, service.PackagePatch{Version: p.Version, Enabled: &off})
		if e != nil {
			t.Error(e)
		}
	}
	out, err = runtime.Resolve(ctx, w, actor, agent.ID, snapshot, public.ID, true)
	if err != nil || len(out.Plugins) != 0 || len(out.Skills) != 0 || len(out.PackageSummaries) != 0 {
		t.Fatal("revocation during discovery was ignored", err)
	}
	on := true
	p, err = packages.Update(ctx, w, actor, agent.ID, p.ID, service.PackagePatch{Version: p.Version, Enabled: &on})
	if err != nil {
		t.Fatal(err)
	}
	if err = plugins.RevokeAccount(ctx, w, actor, agent.ID, plug.ID, account.Version); err != nil {
		t.Fatal(err)
	}
	out, err = runtime.Resolve(ctx, w, actor, agent.ID, snapshot, public.ID, false)
	if err != nil || len(out.Plugins) != 0 {
		t.Fatal("revoked account authorized", err)
	}
	_, err = plugins.PutAccount(ctx, w, actor, agent.ID, plug.ID, service.PluginAccountPut{Name: "Changed account", PluginVersionID: plug.VersionID, Secret: &secret, Version: account.Version + 1})
	if err != nil {
		t.Fatal(err)
	}
	out, err = runtime.Resolve(ctx, w, actor, agent.ID, snapshot, public.ID, false)
	if err != nil || len(out.Plugins) != 0 {
		t.Fatal("old Session silently changed account", err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE platform_members SET status='disabled' WHERE workspace_id=$1 AND user_id=$2`, w, other); err != nil {
		t.Fatal(err)
	}
	if _, err = runtime.Resolve(ctx, w, other, agent.ID, snapshot, public.ID, false); err == nil {
		t.Fatal("removed member retained capabilities")
	}
}

func TestAnonymousPluginDiscoveryAndDefinitionUpgrade(t *testing.T) {
	ctx := context.Background()
	db, models, w, actor := modelFixture(t)
	model := saveTestModel(t, models, w, actor)
	agent, err := (&service.AgentService{Store: NewAgentStore(db)}).Create(ctx, w, actor, service.AgentCreate{Name: "Analyst", ModelProfileID: model.ID})
	if err != nil {
		t.Fatal(err)
	}
	probe := &runtimeProbe{unavailable: true}
	plugins := &service.PluginService{Store: NewPluginStore(db), Probe: probe}
	plugin, err := plugins.Create(ctx, w, actor, service.PluginCreate{Name: "Anonymous", Endpoint: "https://example.test/mcp", AuthType: "none"})
	if err != nil {
		t.Fatal(err)
	}
	packages := &service.PackageService{Store: NewPackageStore(db)}
	_, err = packages.Create(ctx, w, actor, agent.ID, service.PackageCreate{Name: "Search", Plugins: []service.PackagePlugin{{PluginID: plugin.ID}}})
	if err != nil {
		t.Fatal(err)
	}
	setup, err := (&TemplateStore{DB: db}).Setup(ctx, w, actor, agent.ID)
	if err != nil || len(setup["issues"].([]map[string]any)) != 0 {
		t.Fatal("anonymous discovery must not block employee setup", setup, err)
	}
	store := &managerrepo.Store{DB: db, SnapshotCapabilities: SnapshotCapabilities}
	managerSvc := &manager.Service{Store: store}
	c, err := managerSvc.CreateConversation(ctx, w, actor, manager.CreateConversation{AgentID: agent.ID})
	if err != nil {
		t.Fatal(err)
	}
	run := &service.RuntimeService{Packages: packages, Plugins: plugins}
	newSession := func() (string, *service.ExecutionCapabilities) {
		t.Helper()
		session, _, e := managerSvc.CreateSession(ctx, w, actor, "", manager.CreateSession{ConversationID: c.ID, Mode: "task"})
		if e != nil {
			t.Fatal(e)
		}
		private, e := store.Session(ctx, w, actor, session.ID)
		if e != nil {
			t.Fatal(e)
		}
		return session.ID, private.Configuration.Capabilities
	}
	first, old := newSession()
	out, err := run.Resolve(ctx, w, actor, agent.ID, old, first, true)
	if err != nil || len(out.Plugins) != 0 || len(out.Problems) == 0 {
		t.Fatal(out, err)
	}
	probe.unavailable = false
	out, err = run.Resolve(ctx, w, actor, agent.ID, old, first, true)
	if err != nil || len(out.Plugins) != 1 || len(out.Plugins[0].Tools) != 2 || out.Plugins[0].VersionID != plugin.VersionID {
		t.Fatal(out, err)
	}
	name := "Public search"
	next, err := plugins.Update(ctx, w, actor, plugin.ID, service.PluginPatch{Name: &name, Version: plugin.Version})
	if err != nil {
		t.Fatal(err)
	}
	probe.extra = true
	err = packages.Upgrade(ctx, w, actor, agent.ID, service.ResourceUpgrade{Kind: "plugin", ResourceID: plugin.ID, FromVersionID: plugin.VersionID, ToVersionID: next.VersionID})
	if err != nil {
		t.Fatal(err)
	}
	second, fresh := newSession()
	out, err = run.Resolve(ctx, w, actor, agent.ID, fresh, second, true)
	if err != nil || len(out.Plugins) != 1 || out.Plugins[0].VersionID != next.VersionID || len(out.Plugins[0].Tools) != 3 {
		t.Fatal(out, err)
	}
	accounts, err := plugins.SharedAccounts(ctx, w, actor)
	if err != nil || len(accounts) != 2 {
		t.Fatal(accounts, err)
	}
	for _, account := range accounts {
		if (account.AgentCount == 1) != (account.PluginVersionID == next.VersionID) {
			t.Fatal("employee count must follow the current definition", account)
		}
	}
	out, err = run.Resolve(ctx, w, actor, agent.ID, old, first, true)
	if err != nil || len(out.Plugins) != 1 || out.Plugins[0].VersionID != plugin.VersionID || len(out.Plugins[0].Tools) != 2 {
		t.Fatal(out, err)
	}

	// An employee-owned anonymous connection is provisioned lazily. Revocation is final.
	revision := int64(0)
	_, err = plugins.ConnectionSource(ctx, w, actor, agent.ID, plugin.ID, next.VersionID, "agent", &revision)
	if err != nil {
		t.Fatal(err)
	}
	setup, err = (&TemplateStore{DB: db}).Setup(ctx, w, actor, agent.ID)
	if err != nil || len(setup["issues"].([]map[string]any)) != 0 {
		t.Fatal(setup, err)
	}
	third, private := newSession()
	out, err = run.Resolve(ctx, w, actor, agent.ID, private, third, true)
	if err != nil || len(out.Plugins) != 1 || out.Plugins[0].Source != "agent" {
		t.Fatal(out, err)
	}
	if err = plugins.RevokeAccount(ctx, w, actor, agent.ID, plugin.ID, out.Plugins[0].AccountVersion, next.VersionID); err != nil {
		t.Fatal(err)
	}
	_, revoked := newSession()
	if len(revoked.Accounts) != 0 {
		t.Fatal("revoked anonymous account recreated", revoked.Accounts)
	}

	authType := "bearer"
	protected, err := plugins.Update(ctx, w, actor, plugin.ID, service.PluginPatch{AuthType: &authType, Version: next.Version})
	if err != nil {
		t.Fatal(err)
	}
	err = packages.Upgrade(ctx, w, actor, agent.ID, service.ResourceUpgrade{Kind: "plugin", ResourceID: plugin.ID, FromVersionID: next.VersionID, ToVersionID: protected.VersionID})
	var issue *service.Error
	if !errors.As(err, &issue) || issue.Code != "plugin_account_required" {
		t.Fatal("upgraded without a target-version credential", err)
	}
	current, err := packages.List(ctx, w, actor, agent.ID, 1, 20)
	if err != nil || current.Items[0].Plugins[0].VersionID != next.VersionID {
		t.Fatal("failed upgrade changed bindings", current, err)
	}
}
