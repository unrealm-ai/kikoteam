package repository

import (
	"context"
	"encoding/json"
	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"sync"
	"testing"
	"time"
)

func TestSharedHistoryLeasesAndBufferedPages(t *testing.T) {
	api, b, actor, refs := sharingFixture(t)
	db := api.Store.(*GroupSharingStore).DB
	ctx := context.Background()
	store := &managerrepo.Store{DB: db}
	reviewExec(t, db, `UPDATE platform_channel_bindings SET access='{"mode":"all"}' WHERE id=$1`, b.ID)
	p, e := api.Save(ctx, b.WorkspaceID, actor, b.AgentID, "", service.GroupSharingPut{Name: "pages", Mode: "directed", Enabled: true, Sources: refs[:1], Targets: refs[1:2]})
	if e != nil {
		t.Fatal(e)
	}
	run, input := sharingTask(t, db, b, refs[1])
	scope := explicitSharedPageScope(t, store, run)
	end := time.Now().Add(-time.Second).UnixMilli()
	q, e := store.RequestSharedGroupHistory(ctx, scope, manager.SharedHistoryRequest{StartMS: end - 10000, EndMS: end, Limit: 1})
	if e != nil {
		t.Fatal(e)
	}
	jobs, e := store.ClaimSharedHistory(ctx)
	if e != nil || len(jobs) != 1 {
		t.Fatal(jobs, e)
	}
	j := jobs[0]
	if _, e = store.AuthorizeSharedHistory(ctx, j.ID, manager.NewID()); e == nil {
		t.Fatal("wrong lease authorized")
	}
	a := manager.SharedHistoryItem{ID: "a", CreatedAtMS: end - 100, Text: "first", SenderType: "unknown", MessageType: "text"}
	z := manager.SharedHistoryItem{ID: "z", CreatedAtMS: end - 200, Text: "second", SenderType: "unknown", MessageType: "text"}
	complete := manager.SharedHistoryCompletion{LeaseToken: j.LeaseToken, Status: "completed", Page: manager.SharedHistoryPage{Items: []manager.SharedHistoryItem{a}, PendingItems: []manager.SharedHistoryItem{z}, Coverage: "recent_query_local_ids"}}
	if e = store.CompleteSharedHistory(ctx, j.ID, complete); e != nil {
		t.Fatal(e)
	}
	page, e := store.ReadSharedGroupHistory(ctx, q.ID, input)
	if e != nil || len(page.Items) != 1 || !page.HasMore {
		t.Fatal(page, e)
	}
	next, e := store.ContinueSharedGroupHistory(ctx, q.ID, input)
	if e != nil {
		t.Fatal(e)
	}
	again, e := store.ContinueSharedGroupHistory(ctx, q.ID, input)
	if e != nil || again != next {
		t.Fatal(next, again, e)
	}
	jobs, e = store.ClaimSharedHistory(ctx)
	if e != nil || len(jobs) != 1 || len(jobs[0].PendingItems) != 1 || jobs[0].PendingItems[0].ID != "z" {
		t.Fatal(jobs, e)
	}
	if e = api.Delete(ctx, b.WorkspaceID, actor, b.AgentID, p.ID, p.Version); e != nil {
		t.Fatal(e)
	}
	if _, e = store.AuthorizeSharedHistory(ctx, jobs[0].ID, jobs[0].LeaseToken); e == nil {
		t.Fatal("revoked page authorized")
	}
	complete.LeaseToken = jobs[0].LeaseToken
	if e = store.CompleteSharedHistory(ctx, jobs[0].ID, complete); e == nil {
		t.Fatal("revoked completion accepted")
	}
	if _, e = store.ReadSharedGroupHistory(ctx, q.ID, input); e == nil {
		t.Fatal("revoked cached body exposed")
	}
}
func TestSharedWebHistoryIsBoundedAndPersistsLineage(t *testing.T) {
	api, b, actor, refs := sharingFixture(t)
	db := api.Store.(*GroupSharingStore).DB
	ctx := context.Background()
	store := &managerrepo.Store{DB: db}
	reviewExec(t, db, `UPDATE platform_channel_bindings SET access='{"mode":"all"}' WHERE id=$1`, b.ID)
	group := service.NewID()
	reviewExec(t, db, `INSERT INTO platform_web_groups(id,workspace_id,title,created_by)VALUES($1,$2,'source',$3)`, group, b.WorkspaceID, actor)
	reviewExec(t, db, `INSERT INTO platform_web_group_agents(group_id,workspace_id,agent_id,status)VALUES($1,$2,$3,'active')`, group, b.WorkspaceID, b.AgentID)
	source := service.SelectionRef{Kind: "group", Provider: "web", WorkspaceID: b.WorkspaceID, AgentID: b.AgentID, ID: group, Namespace: "web_group_id", ExternalID: group, GroupEpoch: 1, AgentEpoch: 1}
	_, e := api.Save(ctx, b.WorkspaceID, actor, b.AgentID, "", service.GroupSharingPut{Name: "web to im", Mode: "directed", Enabled: true, Sources: []service.SelectionRef{source}, Targets: refs[1:2]})
	if e != nil {
		t.Fatal(e)
	}
	end := time.Now().Add(-time.Second).UnixMilli()
	for i := 1; i <= 3; i++ {
		reviewExec(t, db, `INSERT INTO manager_web_group_messages(id,workspace_id,group_id,seq,author_kind,sender_id,display_name,text,created_at)VALUES($1,$2,$3,$4,'user',$5,'Member',$6,to_timestamp($7::double precision/1000))`, service.NewID(), b.WorkspaceID, group, i, actor, "message", end-int64(400-i*100))
	}
	run, input := sharingTask(t, db, b, refs[1])
	scope := explicitSharedPageScope(t, store, run)
	q, e := store.RequestSharedGroupHistory(ctx, scope, manager.SharedHistoryRequest{StartMS: end - 1000, EndMS: end, Limit: 2})
	if e != nil {
		t.Fatal(e)
	}
	jobs, e := store.ClaimSharedHistory(ctx)
	if e != nil || len(jobs) != 0 {
		t.Fatal("web leaked to transport", jobs, e)
	}
	page, e := store.ReadSharedGroupHistory(ctx, q.ID, input)
	if e != nil || len(page.Items) != 2 || !page.HasMore {
		t.Fatal(page, e)
	}
	next, e := store.ContinueSharedGroupHistory(ctx, q.ID, input)
	if e != nil {
		t.Fatal(e)
	}
	last, e := store.ReadSharedGroupHistory(ctx, next, input)
	if e != nil || len(last.Items) != 1 || last.HasMore {
		t.Fatal(last, e)
	}
	var n int
	if e = db.QueryRow(`SELECT count(*) FROM manager_group_history_reads WHERE input_id=$1`, input).Scan(&n); e != nil || n != 2 {
		t.Fatal(n, e)
	}
	raw, e := json.Marshal(page)
	if e != nil {
		t.Fatal(e)
	}
	var decoded map[string]any
	if e = json.Unmarshal(raw, &decoded); e != nil {
		t.Fatal(e)
	}
	if _, ok := decoded["next_provider_cursor"]; ok {
		t.Fatal("provider cursor exposed")
	}
	reviewExec(t, db, `UPDATE platform_web_group_agents SET status='removed',admission_epoch=admission_epoch+1 WHERE group_id=$1`, group)
	if _, e = store.ReadSharedGroupHistory(ctx, q.ID, input); e == nil {
		t.Fatal("removed source still readable")
	}
}

func TestSharedHistoryInitialRequestsCannotBypassInputBudget(t *testing.T) {
	api, b, actor, refs := sharingFixture(t)
	db := api.Store.(*GroupSharingStore).DB
	ctx := context.Background()
	store := &managerrepo.Store{DB: db}
	reviewExec(t, db, `UPDATE platform_channel_bindings SET access='{"mode":"all"}' WHERE id=$1`, b.ID)
	if _, e := api.Save(ctx, b.WorkspaceID, actor, b.AgentID, "", service.GroupSharingPut{Name: "budget", Mode: "directed", Enabled: true, Sources: refs[:1], Targets: refs[1:2]}); e != nil {
		t.Fatal(e)
	}
	run, input := sharingTask(t, db, b, refs[1])
	scope := explicitSharedPageScope(t, store, run)
	end := time.Now().Add(-time.Second).UnixMilli()
	var wg sync.WaitGroup
	results := make(chan error, 24)
	for range 24 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.RequestSharedGroupHistory(ctx, scope, manager.SharedHistoryRequest{StartMS: end - 1000, EndMS: end, Limit: 1})
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 20 {
		t.Fatal("budget bypass", successes)
	}
	var count int
	if e := db.QueryRow(`SELECT count(*) FROM manager_group_history_queries WHERE input_id=$1`, input).Scan(&count); e != nil || count != 20 {
		t.Fatal(count, e)
	}
}

// Self grants are separate from the one explicit edge these page tests exercise.
func explicitSharedPageScope(t *testing.T, store *managerrepo.Store, run manager.DelegationIdentity) manager.SharedHistoryScope {
	t.Helper()
	ctx := context.Background()
	contexts, e := store.ListSharedHistoryContexts(ctx, run)
	if e != nil {
		t.Fatal(e)
	}
	var out manager.SharedHistoryScope
	count := 0
	for _, c := range contexts {
		scope, err := store.SharedHistoryGrant(ctx, run, c.Ref)
		if err != nil {
			t.Fatal(err)
		}
		if scope.PolicyID != "" {
			out = scope
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected one explicit edge, got %d", count)
	}
	return out
}
