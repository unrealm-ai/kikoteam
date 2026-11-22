package repository

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/controller"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestPlatformWebGroupsMembershipCandidatesAndEpochs(t *testing.T) {
	db, _, _, w, owner, agent, _ := managerFixture(t)
	api := service.WebGroupService{Store: &WebGroupStore{DB: db}}
	member, outsider := service.NewID(), service.NewID()
	for _, id := range []string{member, outsider} {
		insertWorkspaceAccount(t, db, id, id+"@example.test")
		if _, err := db.Exec(`INSERT INTO platform_members(workspace_id,user_id,role) VALUES($1,$2,'member')`, w, id); err != nil {
			t.Fatal(err)
		}
	}
	q := service.WebGroupPut{Title: "Shared room", Members: []service.WebGroupMember{{UserID: member, Role: "member"}}, Agents: []service.WebGroupAgent{{AgentID: agent}}}
	g, err := api.Save(context.Background(), w, owner, "", q)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Members) != 2 || len(g.Agents) != 1 {
		t.Fatal(g)
	}
	other, err := api.Save(context.Background(), w, owner, "", service.WebGroupPut{Title: "Other room"})
	if err != nil {
		t.Fatal(err)
	}
	allowed := func(group, user string, want bool) {
		t.Helper()
		var yes bool
		if err := db.QueryRow(`SELECT platform_web_group_member_allowed($1,$2,$3)`, w, group, user).Scan(&yes); err != nil || yes != want {
			t.Fatal(group, user, yes, err)
		}
	}
	allowed(g.ID, member, true)
	allowed(other.ID, member, false)
	allowed(g.ID, outsider, false)
	page, err := api.List(context.Background(), w, member, "", 1, 20)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != g.ID {
		t.Fatal(page, err)
	}
	if _, err = api.Get(context.Background(), w, outsider, g.ID); err == nil {
		t.Fatal("nonmember saw private group")
	}
	update := service.WebGroupPut{Version: g.Version, Title: g.Title, Members: g.Members, Agents: g.Agents}
	if _, err = api.Save(context.Background(), w, member, g.ID, update); err == nil {
		t.Fatal("member managed group")
	}
	update.Members = []service.WebGroupMember{{UserID: owner, Role: "owner"}}
	g, err = api.Save(context.Background(), w, owner, g.ID, update)
	if err != nil {
		t.Fatal(err)
	}
	allowed(g.ID, member, false)
	if _, err = api.Save(context.Background(), w, owner, g.ID, update); err == nil {
		t.Fatal("stale revision accepted")
	}
	var removedEpoch int64
	if err = db.QueryRow(`SELECT admission_epoch FROM platform_web_group_members WHERE group_id=$1 AND user_id=$2`, g.ID, member).Scan(&removedEpoch); err != nil {
		t.Fatal(err)
	}
	update.Version = g.Version
	update.Members = append(update.Members, service.WebGroupMember{UserID: member, Role: "member"})
	g, err = api.Save(context.Background(), w, owner, g.ID, update)
	if err != nil {
		t.Fatal(err)
	}
	allowed(g.ID, member, true)
	var epoch int64
	if err = db.QueryRow(`SELECT admission_epoch FROM platform_web_group_members WHERE group_id=$1 AND user_id=$2`, g.ID, member).Scan(&epoch); err != nil || epoch <= removedEpoch {
		t.Fatal("rejoin revived old membership", epoch, err)
	}
	var usable bool
	if err = db.QueryRow(`SELECT platform_web_group_agent_allowed($1,$2,$3,$4)`, w, g.ID, agent, member).Scan(&usable); err != nil || !usable {
		t.Fatal("member employee use", usable, err)
	}
	if _, err = db.Exec(`UPDATE platform_agents SET enabled=false WHERE id=$1`, agent); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT platform_web_group_agent_allowed($1,$2,$3,$4)`, w, g.ID, agent, member).Scan(&usable); err != nil || usable {
		t.Fatal("disabled employee admitted", usable, err)
	}
	update.Version = g.Version
	update.Status = "archived"
	if _, err = api.Save(context.Background(), w, owner, g.ID, update); err != nil {
		t.Fatal(err)
	}
	allowed(g.ID, member, false)
}

func TestPlatformWebGroupsHTTPAndCrossWorkspace(t *testing.T) {
	db, _, _, w, owner, _, cookie := managerFixture(t)
	api := &service.WebGroupService{Store: &WebGroupStore{DB: db}}
	mux := (&controller.Server{WebGroups: api, Auth: &service.AuthService{Store: &AuthStore{DB: db}}, Origin: "http://localhost"}).Handler()
	path := "/api/v1/workspaces/" + w + "/web-groups"
	request := func(method, url, body string, auth, csrf bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, url, strings.NewReader(body))
		if auth {
			r.Header.Set("Cookie", cookie)
		}
		if csrf {
			r.Header.Set("Origin", "http://localhost")
			r.Header.Set("X-CSRF-Protection", "1")
		}
		out := httptest.NewRecorder()
		mux.ServeHTTP(out, r)
		return out
	}
	if r := request("GET", path, "", false, false); r.Code != 401 {
		t.Fatal(r.Code)
	}
	if r := request("POST", path, `{"title":"Room"}`, true, false); r.Code != 403 {
		t.Fatal(r.Code)
	}
	r := request("POST", path, `{"title":"Room"}`, true, true)
	if r.Code != 201 {
		t.Fatal(r.Code, r.Body.String())
	}
	var g service.WebGroup
	if err := json.Unmarshal(r.Body.Bytes(), &g); err != nil {
		t.Fatal(err)
	}
	if r = request("GET", path+"?q=Room&page=1&page_size=10", "", true, false); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	if r = request("GET", path+"?q=Room&q=Other", "", true, false); r.Code != 400 {
		t.Fatal(r.Code)
	}
	if r = request("GET", path+"/"+g.ID, "", true, false); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	if _, err := api.Get(context.Background(), service.NewID(), owner, g.ID); err == nil {
		t.Fatal("cross workspace visible")
	}
}

func TestWebGroupIndependentRolesLeaveAndUnifiedGroupScope(t *testing.T) {
	db, _, _, w, workspaceOwner, agent, _ := managerFixture(t)
	api := service.WebGroupService{Store: &WebGroupStore{DB: db}}
	creator, admin := service.NewID(), service.NewID()
	for _, u := range []string{creator, admin} {
		insertWorkspaceAccount(t, db, u, u+"@test.invalid")
		if _, err := db.Exec(`INSERT INTO platform_members(workspace_id,user_id,role) VALUES($1,$2,'member')`, w, u); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`UPDATE platform_agents SET usage_config='{"access":{"mode":"allowlist","allow":[],"deny":[],"group_members_enabled":true}}' WHERE id=$1`, agent); err != nil {
		t.Fatal(err)
	}
	g, err := api.Save(context.Background(), w, creator, "", service.WebGroupPut{Title: "Independent", Members: []service.WebGroupMember{{UserID: admin, Role: "admin"}}, Agents: []service.WebGroupAgent{{AgentID: agent}}})
	if err != nil {
		t.Fatal(err)
	}
	q := service.WebGroupPut{Version: g.Version, Title: g.Title, Members: g.Members, Agents: g.Agents}
	if _, err = api.Get(context.Background(), w, workspaceOwner, g.ID); err == nil {
		t.Fatal("workspace owner read unjoined group")
	}
	if _, err = api.Save(context.Background(), w, workspaceOwner, g.ID, q); err == nil {
		t.Fatal("workspace owner managed unjoined group")
	}
	allowed := func(want bool) {
		t.Helper()
		var yes bool
		if e := db.QueryRow(`SELECT platform_web_group_agent_allowed($1,$2,$3,$4)`, w, g.ID, agent, admin).Scan(&yes); e != nil || yes != want {
			t.Fatal(yes, e)
		}
	}
	allowed(true)
	if _, err = db.Exec(`UPDATE platform_agents SET usage_config=jsonb_set(usage_config,'{access,deny}',jsonb_build_array(jsonb_build_object('channel','web','kind','user','id',$2::text))) WHERE id=$1`, agent, admin); err != nil {
		t.Fatal(err)
	}
	allowed(false)
	if _, err = db.Exec(`UPDATE platform_agents SET usage_config='{"access":{"mode":"allowlist","allow":[],"deny":[],"group_members_enabled":false}}' WHERE id=$1`, agent); err != nil {
		t.Fatal(err)
	}
	allowed(false)
	q.Status = "archived"
	if _, err = api.Save(context.Background(), w, admin, g.ID, q); err == nil {
		t.Fatal("group admin archived without owner")
	}
	q.Status = "active"
	if err = api.Leave(context.Background(), w, creator, g.ID); err == nil {
		t.Fatal("owner left without transfer")
	}
	if err = api.Leave(context.Background(), w, admin, g.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = api.Get(context.Background(), w, admin, g.ID); err == nil {
		t.Fatal("departed admin opened group")
	}
	allowed(false)
}
