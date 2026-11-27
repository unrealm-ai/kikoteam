package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"time"

	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/controller"
	"testing"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestEmployeeMaintainersWebAuthorizationAndRevocation(t *testing.T) {
	db, models, workspace, owner := modelFixture(t)
	m := saveTestModel(t, models, workspace, owner)
	ctx := context.Background()
	people := []string{service.NewID(), service.NewID(), service.NewID()}
	for _, person := range people {
		insertWorkspaceAccount(t, db, person, person+"@example.test")
		if _, err := db.Exec(`INSERT INTO platform_members(workspace_id,user_id,role) VALUES($1,$2,'member')`, workspace, person); err != nil {
			t.Fatal(err)
		}
	}
	creator, maintainer, delegate := people[0], people[1], people[2]
	s := &service.AgentService{Store: NewAgentStore(db)}
	a, err := s.Create(ctx, workspace, creator, service.AgentCreate{Name: "Test employee", ModelProfileID: m.ID})
	if err != nil || !a.CanManage {
		t.Fatal(a, err)
	}
	usage, err := (&AgentUsageStore{DB: db}).ReadUsage(ctx, workspace, creator, a.ID)
	if err != nil || usage.UsageConfig.Access.Mode != "all" || len(usage.UsageConfig.Access.Deny) != 0 {
		t.Fatal(usage, err)
	}
	if _, err = s.Maintainers(ctx, workspace, maintainer, a.ID); err == nil {
		t.Fatal("ordinary member can see the employee's maintenance list")
	}
	ref := func(user string) service.SelectionRef {
		return service.SelectionRef{Kind: "user", Provider: "web", WorkspaceID: workspace, AgentID: a.ID, ExternalID: user}
	}
	list := func(actor string, version int64, p ...service.SelectionRef) service.EmployeeMaintainers {
		t.Helper()
		out, err := s.SaveMaintainers(ctx, workspace, actor, a.ID, service.EmployeeMaintainersUpdate{Version: version, People: p})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	v := list(creator, 1, ref(maintainer), ref(creator), ref(owner), ref(maintainer))
	if len(v.People) != 1 || v.People[0].Ref.ExternalID != maintainer {
		t.Fatal(v)
	}
	v = list(maintainer, v.Version, ref(delegate))
	name := "Updated"
	patch := service.AgentPatch{Version: a.Version, Name: &name}
	if _, err = s.Update(ctx, workspace, maintainer, a.ID, patch); err == nil {
		t.Fatal("removed maintainer can edit")
	}
	a, err = s.Update(ctx, workspace, delegate, a.ID, patch)
	if err != nil || a.Name != name || !a.CanManage {
		t.Fatal(a, err)
	}
	packages := &service.PackageService{Store: NewPackageStore(db)}
	request := service.PackageCreate{Name: "Internal package", Skills: []service.PackageSkill{}, Plugins: []service.PackagePlugin{}}
	if _, err = packages.Create(ctx, workspace, delegate, a.ID, request); err != nil {
		t.Fatal("maintainer cannot configure employee capability package", err)
	}
	if _, err = packages.Create(ctx, workspace, delegate, "", request); err == nil {
		t.Fatal("maintainer can create a shared workspace package")
	}
	var added, removed int
	if err = db.QueryRow(`SELECT jsonb_array_length(added),jsonb_array_length(removed) FROM platform_agent_maintainer_audit
 WHERE agent_id=$1 AND version=$2 AND actor_account_id=$3`, a.ID, v.Version, maintainer).Scan(&added, &removed); err != nil || added != 1 || removed != 1 {
		t.Fatal("maintenance audit missing changes", added, removed, err)
	}
	if _, err = s.SaveMaintainers(ctx, workspace, delegate, a.ID, service.EmployeeMaintainersUpdate{Version: v.Version - 1, People: []service.SelectionRef{}}); err == nil {
		t.Fatal("stale write accepted")
	}
	if _, err = db.Exec(`DELETE FROM platform_members WHERE workspace_id=$1 AND user_id=$2`, workspace, delegate); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Maintainers(ctx, workspace, delegate, a.ID); err == nil {
		t.Fatal("former member retained access")
	}
	v, err = s.Maintainers(ctx, workspace, creator, a.ID)
	if err != nil || len(v.People) != 0 {
		t.Fatal(v, err)
	}
}

func TestEmployeeMaintainersResolveChannelAndRejectDepartment(t *testing.T) {
	db, channels, directory, b, actor, _ := directoryFixture(t)
	publishDirectoryFixture(t, directory, b)
	ctx := context.Background()
	people := &service.PeopleSelectorService{Secret: []byte("selector-test-secret-at-least-32-bytes"), Channels: channels, Store: NewPeopleSelectorStore(db, nil)}
	s := &service.AgentService{Store: NewAgentStore(db)}
	q := service.PeopleQuery{Provider: "feishu", Purpose: "maintenance", Kind: "user", Limit: 50}
	page, err := people.Search(ctx, b.WorkspaceID, actor, b.AgentID, q)
	if err != nil || len(page.Items) != 3 || page.Items[2].Selectable {
		t.Fatal(page, err)
	}
	person := page.Items[0].Ref
	q.Kind = "department"
	if _, err = people.Search(ctx, b.WorkspaceID, actor, b.AgentID, q); err == nil {
		t.Fatal("maintenance can select department")
	}
	for _, bad := range []service.SelectionRef{
		{Kind: "department", Provider: person.Provider, WorkspaceID: b.WorkspaceID, AgentID: b.AgentID, ExternalID: person.ExternalID},
		{Kind: "user", Provider: "web", WorkspaceID: service.NewID(), AgentID: b.AgentID, ExternalID: actor},
		{Kind: "user", Provider: person.Provider, WorkspaceID: b.WorkspaceID, AgentID: b.AgentID, BindingID: service.NewID(), ExternalID: person.ExternalID},
	} {
		if _, err = s.SaveMaintainers(ctx, b.WorkspaceID, actor, b.AgentID, service.EmployeeMaintainersUpdate{Version: 1, People: []service.SelectionRef{bad}}); err == nil {
			t.Fatal("invalid maintainer accepted", bad)
		}
	}
	v, err := s.SaveMaintainers(ctx, b.WorkspaceID, actor, b.AgentID, service.EmployeeMaintainersUpdate{Version: 1, People: []service.SelectionRef{person}})
	if err != nil || len(v.People) != 1 || v.People[0].Ref.BindingID != b.ID {
		t.Fatal(v, err)
	}
	if _, err = db.Exec(`UPDATE platform_channel_bindings SET identity_epoch=identity_epoch+1 WHERE id=$1`, b.ID); err != nil {
		t.Fatal(err)
	}
	v, err = s.Maintainers(ctx, b.WorkspaceID, actor, b.AgentID)
	if err != nil || len(v.People) != 1 || v.People[0].Selectable {
		t.Fatal(v, err)
	}
}

func TestEmployeeMaintainersHTTPWhitelistContract(t *testing.T) {
	db, models, workspace, owner := modelFixture(t)
	m := saveTestModel(t, models, workspace, owner)
	creator, target := service.NewID(), service.NewID()
	for _, person := range []string{creator, target} {
		insertWorkspaceAccount(t, db, person, person+"@example.test")
		if _, err := db.Exec(`INSERT INTO platform_members(workspace_id,user_id,role) VALUES($1,$2,'member')`, workspace, person); err != nil {
			t.Fatal(err)
		}
	}
	for token, user := range map[string]string{"creator-test-key": creator, "target-test-key": target} {
		if _, err := db.Exec(`INSERT INTO platform_sessions(token_hash,account_id,expires_at) VALUES($1,$2,$3)`, service.HashToken(token), user, time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	h := (&controller.Server{Auth: &service.AuthService{Store: &AuthStore{DB: db}}, Agents: &service.AgentService{Store: NewAgentStore(db)}, Origin: "http://localhost"}).Handler()
	call := func(method, path, body, token string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("X-CSRF-Protection", "1")
		r.Header.Set("Cookie", "kiko_session="+token)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, rec.Code, want, rec.Body.String())
		}
		return rec
	}
	base := "/api/v1/workspaces/" + workspace + "/agents"
	res := call("POST", base, `{"name":"HTTP Employee","model_profile_id":"`+m.ID+`"}`, "creator-test-key", 201)
	var agent service.Agent
	if err := json.Unmarshal(res.Body.Bytes(), &agent); err != nil {
		t.Fatal(err)
	}
	path := base + "/" + agent.ID + "/maintainers"
	call("GET", path, "", "target-test-key", 403)
	call("PUT", path, `{"version":1,"people":[],"mode":"inherit"}`, "creator-test-key", 400)
	call("PUT", path, `{"version":1,"people":[],"deny":[]}`, "creator-test-key", 400)
	input := service.EmployeeMaintainersUpdate{Version: 1, People: []service.SelectionRef{
		{Kind: "user", Provider: "web", WorkspaceID: workspace, AgentID: agent.ID, ExternalID: target},
	}}
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	call("PUT", path, string(body), "creator-test-key", 200)
	call("GET", path, "", "target-test-key", 200)
	call("PUT", path, `{"version":2,"people":[]}`, "target-test-key", 200)
	call("PATCH", base+"/"+agent.ID, `{"version":1,"name":"Denied"}`, "target-test-key", 403)
}

func TestEmployeeMaintainersDingTalkDirectory(t *testing.T) {
	db, manager, _, workspace, actor, employee, _ := managerFixture(t)
	directory := NewChannelStore(db)
	channels := &service.ChannelService{Store: directory, Secret: []byte("selector-test-secret-at-least-32-bytes")}
	ctx := context.Background()
	secret := "synthetic-ding-secret"
	b, err := channels.PutProvider(ctx, workspace, actor, employee, "dingtalk", service.ChannelPut{
		AppID: "ding-app", AppSecret: &secret, Enabled: true, History: service.ChannelHistory{Limit: 20}, Access: service.ChannelAccess{Mode: "all"},
	})
	if err != nil {
		t.Fatal(err)
	}
	inbox := manager.Store.(*managerrepo.Store)
	inbound := inboundFixture(b.ID, "test-maintainer-ding", "user1")
	inbound.BotID = "ding-bot"
	inbound.SenderIDType = "user_id"
	inbound.SenderTenant = inbound.ExternalTenantID
	if _, err = inbox.ReceiveChannel(ctx, inbound); err != nil {
		t.Fatal(err)
	}
	if err = inbox.ProcessChannelInbox(ctx, manager); err != nil {
		t.Fatal(err)
	}
	b, err = channels.GetProvider(ctx, b.WorkspaceID, actor, b.AgentID, "dingtalk")
	if err != nil {
		t.Fatal(err)
	}
	publishDirectoryFixture(t, directory, b)
	picker := &service.PeopleSelectorService{Secret: []byte("selector-test-secret-at-least-32-bytes"), Channels: channels, Store: NewPeopleSelectorStore(db, nil)}
	page, err := picker.Search(ctx, b.WorkspaceID, actor, b.AgentID, service.PeopleQuery{Provider: "dingtalk", Purpose: "maintenance", Kind: "user", Limit: 50})
	if err != nil || len(page.Items) == 0 || !page.Items[0].Selectable {
		t.Fatal(page, err)
	}
	grants := &service.AgentService{Store: NewAgentStore(db)}
	result, err := grants.SaveMaintainers(ctx, b.WorkspaceID, actor, b.AgentID, service.EmployeeMaintainersUpdate{Version: 1, People: []service.SelectionRef{page.Items[0].Ref}})
	if err != nil || len(result.People) != 1 || result.People[0].Ref.Provider != "dingtalk" {
		t.Fatal(result, err)
	}
}

func TestEmployeeMaintainersWeComRequiresVerifiedCallbackIdentity(t *testing.T) {
	db, wecom, b, actor := wecomContactFixture(t)
	ctx := context.Background()
	v := contactStart(t, wecom, b, actor, "search-A")
	inbox := &managerrepo.Store{DB: db, ConsumeWecomControl: ConsumeWecomControl(wecom.Channels.Secret)}
	if _, err := inbox.ReceiveChannel(ctx, contactInbound(b, v.Code, "callback-A")); err != nil {
		t.Fatal(err)
	}
	picker := &service.PeopleSelectorService{Secret: []byte("selector-test-secret-at-least-32-bytes"), Channels: wecom.Channels, Wecom: wecom, Store: NewPeopleSelectorStore(db, nil)}
	page, err := picker.Search(ctx, b.WorkspaceID, actor, b.AgentID, service.PeopleQuery{Provider: "wecom", Purpose: "maintenance", Kind: "user", Q: "Alice", Limit: 50})
	if err != nil || len(page.Items) == 0 || page.Items[0].Ref.ExternalID != "callback-A" {
		t.Fatal(page, err)
	}
	agents := &service.AgentService{Store: NewAgentStore(db)}
	ref := page.Items[0].Ref
	bad := ref
	bad.ExternalID = "search-A"
	if _, err = agents.SaveMaintainers(ctx, b.WorkspaceID, actor, b.AgentID, service.EmployeeMaintainersUpdate{Version: 1, People: []service.SelectionRef{bad}}); err == nil {
		t.Fatal("unverified directory ID was accepted as a maintainer")
	}
	result, err := agents.SaveMaintainers(ctx, b.WorkspaceID, actor, b.AgentID, service.EmployeeMaintainersUpdate{Version: 1, People: []service.SelectionRef{ref}})
	if err != nil || len(result.People) != 1 || result.People[0].Ref.ExternalID != "callback-A" {
		t.Fatal(result, err)
	}
}

func TestEmployeeMaintainersMigrationBackfillsWorkspaceOwner(t *testing.T) {
	db, models, workspace, owner := modelFixtureThrough(t, "000104_workspace_owner_model_payer.sql")
	model := saveTestModel(t, models, workspace, owner)
	agent := service.NewID()
	if _, err := db.Exec(`INSERT INTO platform_agents(id,workspace_id,name,model_profile_id,version,config_version,admission_epoch)
 VALUES($1,$2,'Legacy employee',$3,1,1,1)`, agent, workspace, model.ID); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../../../event-hub/database/postgres/migrations/000105_employee_maintainers.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, _, _ := strings.Cut(string(raw), "-- +goose Down")
	if _, err = db.Exec(up); err != nil {
		t.Fatal(err)
	}
	var creator string
	if err = db.QueryRow(`SELECT creator_account_id FROM platform_agents WHERE id=$1`, agent).Scan(&creator); err != nil || creator != owner {
		t.Fatal(creator, err)
	}
	out, err := (&service.AgentService{Store: NewAgentStore(db)}).Maintainers(t.Context(), workspace, owner, agent)
	if err != nil || out.Version != 1 || len(out.People) != 0 {
		t.Fatal(out, err)
	}
}

func assignDifferentTestCreator(t *testing.T, db *sql.DB, workspace, agent string) {
	t.Helper()
	creator := service.NewID()
	insertWorkspaceAccount(t, db, creator, creator+"@example.test")
	if _, err := db.Exec(`INSERT INTO platform_members(workspace_id,user_id,role) VALUES($1,$2,'member')`, workspace, creator); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE platform_agents SET creator_account_id=$2 WHERE id=$1`, agent, creator); err != nil {
		t.Fatal(err)
	}
}
