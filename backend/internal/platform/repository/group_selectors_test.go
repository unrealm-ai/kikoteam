package repository

import (
	"context"
	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/controller"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGroupSelectorSearchScopeAndNoSideEffects(t *testing.T) {
	db, _, _, b, actor, _ := directoryFixture(t)
	ctx := context.Background()
	cache := &ChannelGroupCache{DB: db}
	_, err := cache.PublishGroups(ctx, b, actor, service.ChannelGroupPage{Items: []service.ChannelGroupItem{{ID: "oc_a", Name: "Same name"}, {ID: "oc_b", Name: "Same name"}}})
	if err != nil {
		t.Fatal(err)
	}
	api := &service.GroupSelectorService{Store: NewGroupSelectorStore(db), Secret: []byte(strings.Repeat("s", 32))}
	q := service.GroupSelectionQuery{Limit: 1, Q: "Same"}
	page, err := api.Search(ctx, b.WorkspaceID, actor, b.AgentID, q)
	if err != nil || len(page.Items) != 1 || page.NextCursor == nil {
		t.Fatal(page, err)
	}
	first := page.Items[0].Ref
	q.Cursor = *page.NextCursor
	page, err = api.Search(ctx, b.WorkspaceID, actor, b.AgentID, q)
	if err != nil || len(page.Items) != 1 || page.NextCursor != nil || page.Items[0].Ref.ID == first.ID {
		t.Fatal(page, err)
	}
	q.Q = "other"
	if _, err = api.Search(ctx, b.WorkspaceID, actor, b.AgentID, q); err == nil {
		t.Fatal("cursor crossed filter")
	}
	q.Q = "Same"
	q.Provider = "web"
	if _, err = api.Search(ctx, b.WorkspaceID, actor, b.AgentID, q); err == nil {
		t.Fatal("cursor crossed provider")
	}
	got, err := api.Resolve(ctx, b.WorkspaceID, actor, b.AgentID, []service.SelectionRef{first})
	if err != nil || len(got.Items) != 1 {
		t.Fatal(got, err)
	}
	forged := first
	forged.ExternalID = "oc_other"
	if _, err = api.Resolve(ctx, b.WorkspaceID, actor, b.AgentID, []service.SelectionRef{forged}); err == nil {
		t.Fatal("forged reference accepted")
	}
	forged = first
	forged.AgentID = service.NewID()
	if _, err = api.Resolve(ctx, b.WorkspaceID, actor, b.AgentID, []service.SelectionRef{forged}); err == nil {
		t.Fatal("foreign agent accepted")
	}
	reviewExec(t, db, `UPDATE platform_channel_bindings SET connection_generation=connection_generation+1 WHERE id=$1`, b.ID)
	if _, err = api.Resolve(ctx, b.WorkspaceID, actor, b.AgentID, []service.SelectionRef{first}); err == nil {
		t.Fatal("old generation accepted")
	}
	var count int
	if err = db.QueryRow(`SELECT (SELECT count(*) FROM manager_conversations)+(SELECT count(*) FROM manager_sessions)+(SELECT count(*) FROM manager_inputs)+(SELECT count(*) FROM manager_channel_inbox)`).Scan(&count); err != nil || count != 0 {
		t.Fatal("selection created runtime state", count, err)
	}
}

func TestGroupSelectorWebAdminNotMemberAndEpoch(t *testing.T) {
	db, _, _, w, owner, agent, _ := managerFixture(t)
	ctx := context.Background()
	groups := &service.WebGroupService{Store: &WebGroupStore{DB: db}}
	g, err := groups.Save(ctx, w, owner, "", service.WebGroupPut{Title: "Shared", Agents: []service.WebGroupAgent{{AgentID: agent}}})
	if err != nil {
		t.Fatal(err)
	}
	admin := service.NewID()
	insertWorkspaceAccount(t, db, admin, "selector-admin@example.test")
	reviewExec(t, db, `INSERT INTO platform_members(workspace_id,user_id,role)VALUES($1,$2,'admin')`, w, admin)
	api := &service.GroupSelectorService{Store: NewGroupSelectorStore(db), Secret: []byte(strings.Repeat("s", 32))}
	page, err := api.Search(ctx, w, admin, agent, service.GroupSelectionQuery{Provider: "web", Q: "Shared", Limit: 20})
	if err != nil || len(page.Items) != 1 {
		t.Fatal(page, err)
	}
	if _, err = groups.Get(ctx, w, admin, g.ID); err == nil {
		t.Fatal("metadata granted message/group access")
	}
	ref := page.Items[0].Ref
	if _, err = api.Resolve(ctx, w, admin, agent, []service.SelectionRef{ref}); err != nil {
		t.Fatal(err)
	}
	reviewExec(t, db, `UPDATE platform_web_group_agents SET admission_epoch=admission_epoch+1 WHERE group_id=$1 AND agent_id=$2`, g.ID, agent)
	if _, err = api.Resolve(ctx, w, admin, agent, []service.SelectionRef{ref}); err == nil {
		t.Fatal("rejoin revived old reference")
	}
	reviewExec(t, db, `UPDATE platform_web_group_agents SET status='removed' WHERE group_id=$1 AND agent_id=$2`, g.ID, agent)
	page, err = api.Search(ctx, w, admin, agent, service.GroupSelectionQuery{Limit: 20})
	if err != nil || len(page.Items) != 0 {
		t.Fatal(page, err)
	}
	reviewExec(t, db, `UPDATE platform_members SET status='disabled' WHERE workspace_id=$1 AND user_id=$2`, w, admin)
	if _, err = api.Search(ctx, w, admin, agent, service.GroupSelectionQuery{Limit: 20}); err == nil {
		t.Fatal("revoked admin listed groups")
	}
}

func TestGroupSelectorWecomRecentObservedAndNoticeTarget(t *testing.T) {
	db, _, b, actor := wecomContactFixture(t)
	ctx := context.Background()
	api := &service.GroupSelectorService{Store: NewGroupSelectorStore(db), Secret: []byte(strings.Repeat("s", 32))}
	observed, recent, duplicate := service.NewID(), service.NewID(), service.NewID()
	reviewExec(t, db, `INSERT INTO platform_channel_observed_chats(id,binding_id,identity_epoch,external_tenant_id,external_chat_id,display_name)VALUES($1,$2,$3,$4,'chat_same','Same')`, observed, b.ID, b.IdentityEpoch, b.TenantID)
	for _, v := range []struct{ id, chat string }{{recent, "chat_recent"}, {duplicate, "chat_same"}} {
		reviewExec(t, db, `INSERT INTO platform_channel_group_candidates(id,binding_id,identity_epoch,external_tenant_id,connection_generation,config_revision,external_chat_id,display_name,expires_at)VALUES($1,$2,$3,$4,$5,$6,$7,'Same',now()+interval '5 minutes')`, v.id, b.ID, b.IdentityEpoch, b.TenantID, b.Generation, b.Revision, v.chat)
	}
	page, err := api.Search(ctx, b.WorkspaceID, actor, b.AgentID, service.GroupSelectionQuery{Provider: "wecom", Limit: 20})
	if err != nil || len(page.Items) != 2 {
		t.Fatal(page, err)
	}
	var recentRef service.SelectionRef
	for _, v := range page.Items {
		if v.Ref.ID == duplicate {
			t.Fatal("duplicate evidence returned")
		}
		if v.Ref.ID == recent {
			recentRef = v.Ref
			if v.Coverage != "recent_window" {
				t.Fatal(v)
			}
		}
	}
	if recentRef.ID == "" {
		t.Fatal("recent group missing")
	}
	if _, err = api.Resolve(ctx, b.WorkspaceID, actor, b.AgentID, []service.SelectionRef{recentRef}); err != nil {
		t.Fatal(err)
	}
	target, err := (&managerrepo.Store{DB: db}).ResolveTriggerGroupCandidate(ctx, b.WorkspaceID, actor, b.AgentID, manager.TriggerGroupResolve{Provider: "wecom", CandidateID: recent})
	if err != nil || target.ConversationID == "" {
		t.Fatal(target, err)
	}
	reviewExec(t, db, `UPDATE platform_channel_group_candidates SET expires_at=now()-interval '1 second' WHERE binding_id=$1`, b.ID)
	if _, err = api.Resolve(ctx, b.WorkspaceID, actor, b.AgentID, []service.SelectionRef{recentRef}); err == nil {
		t.Fatal("expired recent candidate resolved")
	}
	if _, err = (&managerrepo.Store{DB: db}).ResolveTriggerGroupCandidate(ctx, b.WorkspaceID, actor, b.AgentID, manager.TriggerGroupResolve{Provider: "wecom", CandidateID: recent}); err == nil {
		t.Fatal("expired notice candidate resolved")
	}
	page, err = api.Search(ctx, b.WorkspaceID, actor, b.AgentID, service.GroupSelectionQuery{Provider: "wecom", Limit: 20})
	if err != nil || len(page.Items) != 1 || page.Items[0].Ref.ID != observed {
		t.Fatal(page, err)
	}
}

func TestGroupSelectorSavedNamesAndUnavailableIdentity(t *testing.T) {
	db, _, _, b, actor, _ := directoryFixture(t)
	ctx := context.Background()
	cache := &ChannelGroupCache{DB: db}
	_, err := cache.PublishGroups(ctx, b, actor, service.ChannelGroupPage{Items: []service.ChannelGroupItem{{ID: "oc_saved", Name: "Original group"}}})
	if err != nil {
		t.Fatal(err)
	}
	api := &service.GroupSelectorService{Store: NewGroupSelectorStore(db), Secret: []byte(strings.Repeat("s", 32))}
	v := service.UsageSelector{Channel: "feishu", Kind: "group", ID: "oc_saved", BindingID: b.ID, IdentityEpoch: b.IdentityEpoch}
	page, err := api.ResolveSaved(ctx, b.WorkspaceID, actor, b.AgentID, []service.UsageSelector{v})
	if err != nil || len(page.Items) != 1 || !page.Items[0].Selectable || page.Items[0].Name != "Original group" {
		t.Fatal(page, err)
	}
	reviewExec(t, db, `UPDATE platform_channel_group_candidates SET expires_at=now()-interval '1 second' WHERE binding_id=$1`, b.ID)
	page, err = api.ResolveSaved(ctx, b.WorkspaceID, actor, b.AgentID, []service.UsageSelector{v})
	if err != nil || len(page.Items) != 1 || page.Items[0].Selectable || page.Items[0].Status != "unavailable" || page.Items[0].Name != "Original group" {
		t.Fatal(page, err)
	}
	reviewExec(t, db, `UPDATE platform_channel_bindings SET identity_epoch=identity_epoch+1 WHERE id=$1`, b.ID)
	page, err = api.ResolveSaved(ctx, b.WorkspaceID, actor, b.AgentID, []service.UsageSelector{v})
	if err != nil || len(page.Items) != 1 || page.Items[0].Selectable || page.Items[0].Ref.IdentityEpoch != v.IdentityEpoch || page.Items[0].Ref.ExternalID != v.ID || page.Items[0].Name != v.ID {
		t.Fatal("remapped rotated source", page, err)
	}
	foreign := v
	foreign.BindingID = service.NewID()
	page, err = api.ResolveSaved(ctx, b.WorkspaceID, actor, b.AgentID, []service.UsageSelector{foreign})
	if err != nil || len(page.Items) != 1 || page.Items[0].Name != foreign.ID || page.Items[0].Ref.BindingID != foreign.BindingID {
		t.Fatal(page, err)
	}
	if _, err = api.ResolveSaved(ctx, b.WorkspaceID, service.NewID(), b.AgentID, []service.UsageSelector{v}); err == nil {
		t.Fatal("unauthorized saved group resolution")
	}
	var count int
	if err = db.QueryRow(`SELECT (SELECT count(*) FROM manager_conversations)+(SELECT count(*) FROM manager_inputs)`).Scan(&count); err != nil || count != 0 {
		t.Fatal("saved resolution created runtime state", count, err)
	}
}

func TestGroupSelectorSavedWebNameRequiresEmployeeAssociation(t *testing.T) {
	db, _, _, w, actor, agent, _ := managerFixture(t)
	ctx := context.Background()
	groups := &service.WebGroupService{Store: &WebGroupStore{DB: db}}
	g, err := groups.Save(ctx, w, actor, "", service.WebGroupPut{Title: "Remembered", Agents: []service.WebGroupAgent{{AgentID: agent}}})
	if err != nil {
		t.Fatal(err)
	}
	private, err := groups.Save(ctx, w, actor, "", service.WebGroupPut{Title: "Unrelated private group"})
	if err != nil {
		t.Fatal(err)
	}
	reviewExec(t, db, `UPDATE platform_web_group_agents SET status='removed' WHERE group_id=$1`, g.ID)
	api := &service.GroupSelectorService{Store: NewGroupSelectorStore(db), Secret: []byte(strings.Repeat("s", 32))}
	page, err := api.ResolveSaved(ctx, w, actor, agent, []service.UsageSelector{{Channel: "web", Kind: "group", ID: g.ID}, {Channel: "web", Kind: "group", ID: private.ID}})
	if err != nil || len(page.Items) != 2 || page.Items[0].Selectable || page.Items[0].Name != "Remembered" || page.Items[1].Name != private.ID || page.Items[1].Selectable {
		t.Fatal(page, err)
	}
	if _, err = api.ResolveSaved(ctx, w, actor, agent, []service.UsageSelector{{Channel: "web", Kind: "user", ID: actor}}); err == nil {
		t.Fatal("accepted person in group resolver")
	}
}

func TestGroupSelectorSavedHTTPContract(t *testing.T) {
	db, _, _, b, _, cookie := directoryFixture(t)
	api := &service.GroupSelectorService{Store: NewGroupSelectorStore(db), Secret: []byte(strings.Repeat("s", 32))}
	h := (&controller.Server{Auth: &service.AuthService{Store: &AuthStore{DB: db}}, GroupSelectors: api, Origin: "http://localhost"}).Handler()
	path := "/api/v1/workspaces/" + b.WorkspaceID + "/agents/" + b.AgentID + "/selectors/groups/resolve"
	for _, tc := range []struct {
		body       string
		auth, csrf bool
		status     int
	}{
		{`{"selectors":[]}`, true, true, 200},
		{`{"selectors":[],"refs":[]}`, true, true, 400},
		{`{"selectors":[],"unexpected":true}`, true, true, 400},
		{`{"selectors":[]}`, false, true, 401},
		{`{"selectors":[]}`, true, false, 403},
	} {
		req := httptest.NewRequest("POST", path, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		if tc.auth {
			req.Header.Set("Cookie", cookie)
		}
		if tc.csrf {
			req.Header.Set("X-CSRF-Protection", "1")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tc.status {
			t.Fatal(tc, rec.Code, rec.Body.String())
		}
	}
}
