package repository

import (
	"context"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"testing"
)

func TestDirectoryRelationshipsReplaceAndRemainMetadata(t *testing.T) {
	_, api, store, b, actor, _ := directoryFixture(t)
	ctx := context.Background()
	publish := func(v service.DirectorySnapshot) {
		t.Helper()
		if _, err := store.RequestDirectorySync(ctx, b); err != nil {
			t.Fatal(err)
		}
		claim, err := store.ClaimDirectorySync(ctx)
		if err != nil || claim == nil {
			t.Fatal(claim, err)
		}
		if err = store.PublishDirectory(ctx, *claim, v); err != nil {
			t.Fatal(err)
		}
	}
	read := func() service.DirectoryUser {
		t.Helper()
		page, err := api.DirectoryPage(ctx, b.WorkspaceID, actor, b.AgentID, service.DirectoryQuery{Kind: "users", Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		return page.Items.([]service.DirectoryUser)[0]
	}
	v := directorySnapshot()
	boss := "boss"
	v.Users[0].DirectManagerID = &boss
	v.Users[0].DirectManagerKnown = true
	v.Users[0].Aliases = []service.DirectoryAlias{{ID: "union-person", Namespace: "union_id", ApplicationID: b.AppID}}
	v.Departments[0].LeaderIDs = []string{"boss"}
	v.Departments[0].LeadersKnown = true
	publish(v)
	u := read()
	if u.ID != "ou_a" || u.DirectManagerID == nil || *u.DirectManagerID != "boss" || len(u.Aliases) != 1 {
		t.Fatal(u)
	}
	page, err := api.DirectoryPage(ctx, b.WorkspaceID, actor, b.AgentID, service.DirectoryQuery{Kind: "departments", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	d := page.Items.([]service.DirectoryDepartment)[0]
	if !d.LeadersKnown || len(d.LeaderIDs) != 1 {
		t.Fatal(d)
	}
	// Metadata is not a new searchable identity.
	page, err = api.DirectoryPage(ctx, b.WorkspaceID, actor, b.AgentID, service.DirectoryQuery{Kind: "users", Limit: 10, Q: "union-person"})
	if err != nil || len(page.Items.([]service.DirectoryUser)) != 0 {
		t.Fatal(page, err)
	}
	v.Users[0].DirectManagerID = nil
	v.Users[0].Aliases = nil
	v.Departments[0].LeaderIDs = nil
	publish(v)
	u = read()
	if !u.DirectManagerKnown || u.DirectManagerID != nil || len(u.Aliases) != 0 {
		t.Fatal("known-empty update retained stale data", u)
	}
	v.Users[0].DirectManagerKnown = false
	v.Departments[0].LeadersKnown = false
	publish(v)
	u = read()
	if u.DirectManagerKnown || u.DirectManagerID != nil {
		t.Fatal("new unknown snapshot retained old fact", u)
	}
}
