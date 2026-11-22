package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/controller"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func sharingFixture(t *testing.T) (*service.GroupSharingService, service.ChannelBinding, string, []service.SelectionRef) {
	t.Helper()
	db, _, _, b, actor, _ := directoryFixture(t)
	cache := &ChannelGroupCache{DB: db}
	if _, err := cache.PublishGroups(context.Background(), b, actor, service.ChannelGroupPage{Items: []service.ChannelGroupItem{{ID: "oc_a", Name: "A"}, {ID: "oc_b", Name: "B"}, {ID: "oc_c", Name: "C"}}}); err != nil {
		t.Fatal(err)
	}
	selector := &service.GroupSelectorService{Store: NewGroupSelectorStore(db), Secret: []byte(strings.Repeat("s", 32))}
	page, err := selector.Search(context.Background(), b.WorkspaceID, actor, b.AgentID, service.GroupSelectionQuery{Limit: 20})
	if err != nil || len(page.Items) != 3 {
		t.Fatal(page, err)
	}
	refs := []service.SelectionRef{}
	for _, g := range page.Items {
		refs = append(refs, g.Ref)
	}
	return &service.GroupSharingService{Store: &GroupSharingStore{DB: db}}, b, actor, refs
}
func TestSharingPolicyCRUDVersionsDirectionsAndNoSideEffects(t *testing.T) {
	api, b, actor, refs := sharingFixture(t)
	ctx := context.Background()
	db := api.Store.(*GroupSharingStore).DB
	q := service.GroupSharingPut{Name: "Source to receiver", Mode: "directed", Enabled: true, Sources: refs[:1], Targets: refs[1:2]}
	preview, err := api.Preview(ctx, b.WorkspaceID, actor, b.AgentID, q)
	if err != nil || len(preview.Edges) != 1 {
		t.Fatal(preview, err)
	}
	first, err := api.Save(ctx, b.WorkspaceID, actor, b.AgentID, "", q)
	if err != nil || first.Version != 1 {
		t.Fatal(first, err)
	}
	q.Name = "Next edge"
	q.Sources = refs[1:2]
	q.Targets = refs[2:]
	second, err := api.Save(ctx, b.WorkspaceID, actor, b.AgentID, "", q)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM platform_group_sharing_edges`).Scan(&count); err != nil || count != 2 {
		t.Fatal("unexpected transitive edge", count, err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM platform_group_sharing_edges WHERE source_ref->>'external_id'=$1 AND target_ref->>'external_id'=$2`, refs[0].ExternalID, refs[2].ExternalID).Scan(&count); err != nil || count != 0 {
		t.Fatal("A to C implicitly granted", count, err)
	}
	list, err := api.List(ctx, b.WorkspaceID, actor, b.AgentID, "", 1, 1)
	if err != nil || len(list.Items) != 1 || list.Total != 2 {
		t.Fatal(list, err)
	}
	q.Version = second.Version
	q.Name = "Renamed"
	updated, err := api.Save(ctx, b.WorkspaceID, actor, b.AgentID, second.ID, q)
	if err != nil || updated.Version != 2 {
		t.Fatal(updated, err)
	}
	if _, err = api.Save(ctx, b.WorkspaceID, actor, b.AgentID, second.ID, q); err == nil {
		t.Fatal("stale update accepted")
	}
	if err = api.Delete(ctx, b.WorkspaceID, actor, b.AgentID, second.ID, 1); err == nil {
		t.Fatal("stale delete accepted")
	}
	if err = api.Delete(ctx, b.WorkspaceID, actor, b.AgentID, second.ID, 2); err != nil {
		t.Fatal(err)
	}
	if _, err = api.Get(ctx, b.WorkspaceID, actor, b.AgentID, second.ID); err == nil {
		t.Fatal("deleted policy visible")
	}
	if _, err = api.Get(ctx, b.WorkspaceID, service.NewID(), b.AgentID, first.ID); err == nil {
		t.Fatal("unauthorized get")
	}
	if _, err = api.Get(ctx, b.WorkspaceID, actor, service.NewID(), first.ID); err == nil {
		t.Fatal("cross employee get")
	}
	if err = db.QueryRow(`SELECT (SELECT count(*) FROM manager_conversations)+(SELECT count(*) FROM manager_inputs)+(SELECT count(*) FROM manager_channel_inbox)`).Scan(&count); err != nil || count != 0 {
		t.Fatal("configuration created runtime state", count, err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM platform_group_sharing_policy_events`).Scan(&count); err != nil || count != 4 {
		t.Fatal("missing configuration audit", count, err)
	}
}
func TestSharingPolicyStaleEdgesCanDisableButNotExpandOrReenable(t *testing.T) {
	api, b, actor, refs := sharingFixture(t)
	ctx := context.Background()
	db := api.Store.(*GroupSharingStore).DB
	q := service.GroupSharingPut{Name: "Original", Mode: "directed", Enabled: true, Sources: refs[:1], Targets: refs[1:2]}
	p, err := api.Save(ctx, b.WorkspaceID, actor, b.AgentID, "", q)
	if err != nil {
		t.Fatal(err)
	}
	reviewExec(t, db, `UPDATE platform_channel_group_candidates SET expires_at=now()-interval '1 second' WHERE id=$1`, refs[0].ID)
	q.Version = p.Version
	q.Name = "Keep existing"
	p, err = api.Save(ctx, b.WorkspaceID, actor, b.AgentID, p.ID, q)
	if err != nil {
		t.Fatal(err)
	}
	q.Version = p.Version
	q.Targets = refs[1:]
	if _, err = api.Save(ctx, b.WorkspaceID, actor, b.AgentID, p.ID, q); err == nil {
		t.Fatal("stale source authorized new target")
	}
	q.Targets = refs[1:2]
	q.Enabled = false
	p, err = api.Save(ctx, b.WorkspaceID, actor, b.AgentID, p.ID, q)
	if err != nil {
		t.Fatal(err)
	}
	q.Version = p.Version
	q.Enabled = true
	if _, err = api.Save(ctx, b.WorkspaceID, actor, b.AgentID, p.ID, q); err == nil {
		t.Fatal("reenabled stale source")
	}
}
func TestSharingPolicyConcurrentVersionCAS(t *testing.T) {
	api, b, actor, refs := sharingFixture(t)
	ctx := context.Background()
	q := service.GroupSharingPut{Name: "Original", Mode: "mutual", Enabled: true, Sources: refs, Targets: []service.SelectionRef{}}
	p, err := api.Save(ctx, b.WorkspaceID, actor, b.AgentID, "", q)
	if err != nil {
		t.Fatal(err)
	}
	q.Version = p.Version
	q.Name = "Updated"
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := api.Save(ctx, b.WorkspaceID, actor, b.AgentID, p.ID, q); results <- e }()
	}
	wg.Wait()
	close(results)
	success := 0
	for e := range results {
		if e == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatal("version CAS", success)
	}
	var count int
	if err = api.Store.(*GroupSharingStore).DB.QueryRow(`SELECT count(*) FROM platform_group_sharing_edges WHERE policy_id=$1`, p.ID).Scan(&count); err != nil || count != 6 {
		t.Fatal(count, err)
	}
}

func TestSharingPolicyTypedPeopleAndCrossChannel(t *testing.T) {
	db, channels, directory, b, actor, _ := directoryFixture(t)
	ctx := context.Background()
	publishDirectoryFixture(t, directory, b)
	groupCache := &ChannelGroupCache{DB: db}
	if _, err := groupCache.PublishGroups(ctx, b, actor, service.ChannelGroupPage{Items: []service.ChannelGroupItem{{ID: "oc_source", Name: "Source"}}}); err != nil {
		t.Fatal(err)
	}
	groups := &service.GroupSelectorService{Store: NewGroupSelectorStore(db), Secret: []byte(strings.Repeat("s", 32))}
	page, err := groups.Search(ctx, b.WorkspaceID, actor, b.AgentID, service.GroupSelectionQuery{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	people := &service.PeopleSelectorService{Channels: channels, Store: NewPeopleSelectorStore(db, []byte(strings.Repeat("s", 32))), Secret: []byte(strings.Repeat("s", 32))}
	person, err := people.Search(ctx, b.WorkspaceID, actor, b.AgentID, service.PeopleQuery{Provider: "feishu", Purpose: "sharing", Kind: "user", Q: "Alice", Limit: 20})
	if err != nil || len(person.Items) != 1 {
		t.Fatal(person, err)
	}
	api := &service.GroupSharingService{Store: &GroupSharingStore{DB: db}}
	webPerson := service.SelectionRef{Kind: "user", Provider: "web", WorkspaceID: b.WorkspaceID, AgentID: b.AgentID, ExternalID: actor}
	q := service.GroupSharingPut{Name: "Mixed recipients", Mode: "directed", Enabled: true, Sources: []service.SelectionRef{page.Items[0].Ref}, Targets: []service.SelectionRef{webPerson, person.Items[0].Ref}}
	created, err := api.Save(ctx, b.WorkspaceID, actor, b.AgentID, "", q)
	if err != nil || len(created.Targets) != 2 {
		t.Fatal(created, err)
	}
	q.Targets[1].Namespace = "user_id"
	if _, err = api.Save(ctx, b.WorkspaceID, actor, b.AgentID, "", q); err == nil {
		t.Fatal("forged person namespace accepted")
	}
	q.Targets = []service.SelectionRef{webPerson}
	q.Sources[0].ID = "not-a-uuid"
	if _, err = api.Save(ctx, b.WorkspaceID, actor, b.AgentID, "", q); err == nil {
		t.Fatal("malformed group candidate accepted")
	}
}

func TestSharingPolicyHTTPCRUDAndPermissions(t *testing.T) {
	db, _, _, b, actor, cookie := directoryFixture(t)
	ctx := context.Background()
	if _, err := (&ChannelGroupCache{DB: db}).PublishGroups(ctx, b, actor, service.ChannelGroupPage{Items: []service.ChannelGroupItem{{ID: "oc_a", Name: "A"}, {ID: "oc_b", Name: "B"}}}); err != nil {
		t.Fatal(err)
	}
	groups := &service.GroupSelectorService{Store: NewGroupSelectorStore(db), Secret: []byte(strings.Repeat("s", 32))}
	p, err := groups.Search(ctx, b.WorkspaceID, actor, b.AgentID, service.GroupSelectionQuery{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	q := service.GroupSharingPut{Name: "HTTP policy", Mode: "directed", Enabled: true, Sources: []service.SelectionRef{p.Items[0].Ref}, Targets: []service.SelectionRef{p.Items[1].Ref}}
	h := (&controller.Server{Auth: &service.AuthService{Store: &AuthStore{DB: db}}, SharingPolicies: &service.GroupSharingService{Store: &GroupSharingStore{DB: db}}, Origin: "http://localhost"}).Handler()
	base := "/api/v1/workspaces/" + b.WorkspaceID + "/agents/" + b.AgentID + "/sharing-policies"
	request := func(method, path string, body any, auth, csrf bool) *httptest.ResponseRecorder {
		t.Helper()
		raw, e := json.Marshal(body)
		if e != nil {
			t.Fatal(e)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		if auth {
			req.Header.Set("Cookie", cookie)
		}
		if csrf {
			req.Header.Set("X-CSRF-Protection", "1")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if r := request("POST", base, q, false, true); r.Code != 401 {
		t.Fatal(r.Code, r.Body.String())
	}
	if r := request("POST", base, q, true, false); r.Code != 403 {
		t.Fatal(r.Code, r.Body.String())
	}
	if r := request("POST", base+"/preview", q, true, true); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	r := request("POST", base, q, true, true)
	if r.Code != 201 {
		t.Fatal(r.Code, r.Body.String())
	}
	var created service.GroupSharingPolicy
	if err = json.Unmarshal(r.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if r = request("GET", base+"?page=1&page=2", nil, true, false); r.Code != 400 {
		t.Fatal(r.Code, r.Body.String())
	}
	q.Version = created.Version
	q.Name = "Revised"
	if r = request("PUT", base+"/"+created.ID, q, true, true); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	if r = request("DELETE", base+"/"+created.ID+"?version=1", nil, true, true); r.Code != 409 {
		t.Fatal(r.Code, r.Body.String())
	}
	if r = request("DELETE", base+"/"+created.ID+"?version=2", nil, true, true); r.Code != 204 {
		t.Fatal(r.Code, r.Body.String())
	}
}
