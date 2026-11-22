package repository

import (
	"context"
	"encoding/json"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"strings"
	"sync"
	"testing"
)

type pluginProbeFixture struct{ before func() }

func (p pluginProbeFixture) Probe(_ context.Context, _ service.Plugin, _ string) ([]service.MCPTool, error) {
	if p.before != nil {
		p.before()
	}
	return []service.MCPTool{{Name: "read", Description: "Read", InputSchema: json.RawMessage(`{"type":"object"}`), SchemaDigest: "test-digest"}}, nil
}
func TestPluginVersionsAccountsAndIsolation(t *testing.T) {
	db, models, w, actor := modelFixture(t)
	ctx := context.Background()
	m := saveTestModel(t, models, w, actor)
	employee, e := (&service.AgentService{Store: NewAgentStore(db)}).Create(ctx, w, actor, service.AgentCreate{Name: "Employee", ModelProfileID: m.ID})
	if e != nil {
		t.Fatal(e)
	}
	s := &service.PluginService{Store: NewPluginStore(db), Secret: []byte("test-only-plugin-secret-32-characters"), Probe: pluginProbeFixture{}}
	p, e := s.Create(ctx, w, actor, service.PluginCreate{Name: "MCP", Endpoint: "https://example.com/mcp", AuthType: "bearer"})
	if e != nil {
		t.Fatal(e)
	}
	old := p.VersionID
	secret := "super-secret-value"
	account, e := s.PutAccount(ctx, w, actor, employee.ID, p.ID, service.PluginAccountPut{Name: "Business", PluginVersionID: old, Secret: &secret})
	if e != nil {
		t.Fatal(e)
	}
	wire, e := json.Marshal(account)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(wire), secret) {
		t.Fatal("secret serialized")
	}
	var cipher []byte
	if e = db.QueryRow(`SELECT credential FROM platform_plugin_connections WHERE id=$1`, account.ID).Scan(&cipher); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(cipher), secret) {
		t.Fatal("plaintext persisted")
	}
	endpoint := "https://example.com/new"
	p, e = s.Update(ctx, w, actor, p.ID, service.PluginPatch{Version: p.Version, Endpoint: &endpoint})
	if e != nil || p.VersionID == old {
		t.Fatal(p, e)
	}
	versions, e := s.Versions(ctx, w, actor, p.ID, 1, 20)
	if e != nil || len(versions.Items) != 2 || versions.Items[1].Endpoint != "https://example.com/mcp" {
		t.Fatal(versions, e)
	}
	if _, e = s.PutAccount(ctx, w, actor, employee.ID, p.ID, service.PluginAccountPut{Name: "Business", PluginVersionID: p.VersionID, Version: account.Version}); e == nil {
		t.Fatal("reused secret across changed endpoint")
	}
	member := service.NewID()
	insertWorkspaceAccount(t, db, member, "pluginmember@example.test")
	if _, e = db.Exec(`INSERT INTO platform_members(workspace_id,user_id,role,status,version)VALUES($1,$2,'member','active',1)`, w, member); e != nil {
		t.Fatal(e)
	}
	if _, e = s.GetAccount(ctx, w, member, employee.ID, p.ID); e == nil {
		t.Fatal("member sees account-scoped tools")
	}
	if _, e = s.Get(ctx, w, member, p.ID); e != nil {
		t.Fatal(e)
	}
	other, e := (&service.WorkspaceService{Store: NewWorkspaceStore(db)}).Create(ctx, actor, "other", "")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Get(ctx, other.ID, actor, p.ID); e == nil {
		t.Fatal("cross-workspace plugin")
	}
	if e = s.RevokeAccount(ctx, w, actor, employee.ID, p.ID, account.Version); e != nil {
		t.Fatal(e)
	}
	account, e = s.GetAccount(ctx, w, actor, employee.ID, p.ID)
	if e != nil || account.Enabled || account.Validated || len(account.Tools) != 0 {
		t.Fatal(account, e)
	}
}
func TestPluginAccountConcurrentSave(t *testing.T) {
	db, models, w, a := modelFixture(t)
	ctx := context.Background()
	m := saveTestModel(t, models, w, a)
	employee, e := (&service.AgentService{Store: NewAgentStore(db)}).Create(ctx, w, a, service.AgentCreate{Name: "Employee", ModelProfileID: m.ID})
	if e != nil {
		t.Fatal(e)
	}
	s := &service.PluginService{Store: NewPluginStore(db), Secret: []byte("test-only-plugin-secret-32-characters"), Probe: pluginProbeFixture{}}
	p, e := s.Create(ctx, w, a, service.PluginCreate{Name: "MCP", Endpoint: "https://example.com/mcp", AuthType: "none"})
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	results := make(chan error, 6)
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := s.PutAccount(ctx, w, a, employee.ID, p.ID, service.PluginAccountPut{Name: "Business", PluginVersionID: p.VersionID})
			results <- e
		}()
	}
	wg.Wait()
	close(results)
	ok := 0
	for e := range results {
		if e == nil {
			ok++
		}
	}
	if ok != 1 {
		t.Fatalf("%d successful creates", ok)
	}
}
func TestPluginProbeRechecksMembership(t *testing.T) {
	db, models, w, owner := modelFixture(t)
	ctx := context.Background()
	m := saveTestModel(t, models, w, owner)
	employee, e := (&service.AgentService{Store: NewAgentStore(db)}).Create(ctx, w, owner, service.AgentCreate{Name: "Employee", ModelProfileID: m.ID})
	if e != nil {
		t.Fatal(e)
	}
	a := service.NewID()
	insertWorkspaceAccount(t, db, a, "probe-admin@example.test")
	if _, e = db.Exec(`INSERT INTO platform_members(workspace_id,user_id,role,status,version)VALUES($1,$2,'admin','active',1)`, w, a); e != nil {
		t.Fatal(e)
	}
	s := &service.PluginService{Store: NewPluginStore(db), Secret: []byte("test-only-plugin-secret-32-characters")}
	p, e := s.Create(ctx, w, a, service.PluginCreate{Name: "MCP", Endpoint: "https://example.com", AuthType: "none"})
	if e != nil {
		t.Fatal(e)
	}
	s.Probe = pluginProbeFixture{before: func() {
		if _, e := db.Exec(`UPDATE platform_members SET role='member' WHERE workspace_id=$1 AND user_id=$2`, w, a); e != nil {
			t.Error(e)
		}
	}}
	if _, e = s.PutAccount(ctx, w, a, employee.ID, p.ID, service.PluginAccountPut{Name: "Business", PluginVersionID: p.VersionID}); e == nil {
		t.Fatal("revoked admin saved account")
	}
}
