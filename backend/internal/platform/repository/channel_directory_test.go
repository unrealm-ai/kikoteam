package repository

import (
	"context"
	"database/sql"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/controller"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func directoryFixture(t *testing.T) (*sql.DB, *service.ChannelService, *ChannelStore, service.ChannelBinding, string, string) {
	t.Helper()
	db, _, _, w, actor, agent, cookie := managerFixture(t)
	store := NewChannelStore(db)
	s := &service.ChannelService{Store: store, Secret: []byte(strings.Repeat("d", 32))}
	secret := "synthetic-directory-secret"
	b, err := s.Put(context.Background(), w, actor, agent, service.ChannelPut{AppID: "cli_directory", AppSecret: &secret, Enabled: true, History: service.ChannelHistory{Limit: 20}, Access: service.ChannelAccess{Mode: "all"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Apply(context.Background(), b.ID, service.ChannelApplied{Generation: b.Generation, RulesVersion: b.RulesVersion, Revision: b.Revision, Status: "connected", ObservedTenantID: "tenant-directory", ObservedBotID: "ou_bot"}); err != nil {
		t.Fatal(err)
	}
	b, err = s.Get(context.Background(), w, actor, agent)
	if err != nil {
		t.Fatal(err)
	}
	return db, s, store, b, actor, cookie
}
func directorySnapshot() service.DirectorySnapshot {
	return service.DirectorySnapshot{Users: []service.DirectoryUser{
		{ID: "ou_a", DisplayName: "Alice", Status: "active", DepartmentIDs: []string{"od_team"}, MembershipsKnown: true},
		{ID: "ou_b", DisplayName: "Bob", Status: "active", DepartmentIDs: []string{"od_child"}, MembershipsKnown: true},
		{ID: "ou_unknown", DisplayName: "Unknown", Status: "unknown", DepartmentIDs: []string{}, MembershipsKnown: false},
	}, Departments: []service.DirectoryDepartment{{ID: "od_team", DisplayName: "Team"}, {ID: "od_child", DisplayName: "Child", ParentID: "od_team"}}}
}
func publishDirectoryFixture(t *testing.T, s *ChannelStore, b service.ChannelBinding) service.DirectoryClaim {
	t.Helper()
	ctx := context.Background()
	if _, err := s.RequestDirectorySync(ctx, b); err != nil {
		t.Fatal(err)
	}
	c, err := s.ClaimDirectorySync(ctx)
	if err != nil || c == nil {
		t.Fatal(c, err)
	}
	if err = s.PublishDirectory(ctx, *c, directorySnapshot()); err != nil {
		t.Fatal(err)
	}
	return *c
}
func TestDirectorySnapshotQueriesIsolationAndPagination(t *testing.T) {
	db, api, store, b, actor, _ := directoryFixture(t)
	ctx := context.Background()
	initial, err := api.DirectoryStatus(ctx, b.WorkspaceID, actor, b.AgentID)
	if err != nil || initial.Status != "never" {
		t.Fatal(initial, err)
	}
	publishDirectoryFixture(t, store, b)
	status, err := api.DirectoryStatus(ctx, b.WorkspaceID, actor, b.AgentID)
	if err != nil || !status.Complete || status.UserCount != 3 || status.DepartmentCount != 2 || status.Status != "ready" {
		t.Fatal(status, err)
	}
	q := service.DirectoryQuery{Kind: "users", Limit: 1}
	p, err := api.DirectoryPage(ctx, b.WorkspaceID, actor, b.AgentID, q)
	if err != nil || p.NextCursor == nil || p.Items.([]service.DirectoryUser)[0].ID != "ou_a" {
		t.Fatal(p, err)
	}
	q.Cursor = *p.NextCursor
	next, err := api.DirectoryPage(ctx, b.WorkspaceID, actor, b.AgentID, q)
	if err != nil || next.Items.([]service.DirectoryUser)[0].ID != "ou_b" {
		t.Fatal(next, err)
	}
	q.Q = "Bob"
	if _, err = api.DirectoryPage(ctx, b.WorkspaceID, actor, b.AgentID, q); err == nil {
		t.Fatal("cursor reused across query")
	}
	q = service.DirectoryQuery{Kind: "departments", Limit: 50}
	p, err = api.DirectoryPage(ctx, b.WorkspaceID, actor, b.AgentID, q)
	if err != nil || len(p.Items.([]service.DirectoryDepartment)) != 1 || p.Items.([]service.DirectoryDepartment)[0].ID != "od_team" {
		t.Fatal(p, err)
	}
	q.ParentID = "od_team"
	p, err = api.DirectoryPage(ctx, b.WorkspaceID, actor, b.AgentID, q)
	if err != nil || p.Items.([]service.DirectoryDepartment)[0].ID != "od_child" {
		t.Fatal(p, err)
	}
	if _, err = api.DirectoryStatus(ctx, b.WorkspaceID, service.NewID(), b.AgentID); err == nil {
		t.Fatal("unauthorized directory exposed")
	}
	if _, err = api.DirectoryStatus(ctx, service.NewID(), actor, b.AgentID); err == nil {
		t.Fatal("cross workspace exposed")
	}
	var name sql.NullString
	if err = db.QueryRow(`SELECT platform_channel_directory_display_name($1,$2,$3,'ou_a')`, b.ID, b.IdentityEpoch, b.TenantID).Scan(&name); err != nil || name.String != "Alice" {
		t.Fatal(name, err)
	}
	if err = db.QueryRow(`SELECT platform_channel_directory_display_name($1,$2,'other-tenant','ou_a')`, b.ID, b.IdentityEpoch).Scan(&name); err != nil || name.Valid {
		t.Fatal(name, err)
	}
	if _, err = db.Exec(`UPDATE platform_channel_directory_state SET expires_at=now()-interval '1 second' WHERE binding_id=$1`, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = api.DirectoryPage(ctx, b.WorkspaceID, actor, b.AgentID, q); err == nil {
		t.Fatal("expired directory listed")
	}
	if err = db.QueryRow(`SELECT platform_channel_directory_display_name($1,$2,$3,'ou_a')`, b.ID, b.IdentityEpoch, b.TenantID).Scan(&name); err != nil || name.Valid {
		t.Fatal(name, err)
	}
}
func TestDirectoryLeaseAndAtomicPublication(t *testing.T) {
	db, _, store, b, _, _ := directoryFixture(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	claims := make(chan *service.DirectoryClaim, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); c, e := store.ClaimDirectorySync(ctx); claims <- c; errs <- e }()
	}
	wg.Wait()
	close(claims)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var claim *service.DirectoryClaim
	for c := range claims {
		if c != nil {
			if claim != nil {
				t.Fatal("parallel duplicate claim")
			}
			claim = c
		}
	}
	if claim == nil {
		t.Fatal("no claim")
	}
	bad := directorySnapshot()
	bad.Users = append(bad.Users, bad.Users[0])
	if err := store.PublishDirectory(ctx, *claim, bad); err == nil {
		t.Fatal("invalid partial snapshot published")
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM platform_channel_directory_users`).Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if err := store.PublishDirectory(ctx, *claim, directorySnapshot()); err != nil {
		t.Fatal(err)
	}
	if err := store.PublishDirectory(ctx, *claim, directorySnapshot()); err == nil {
		t.Fatal("completed claim replayed")
	}
	if c, err := store.ClaimDirectorySync(ctx); err != nil || c != nil {
		t.Fatal("hourly job ran early", c, err)
	}
	if _, err := store.RequestDirectorySync(ctx, b); err != nil {
		t.Fatal(err)
	}
	next, err := store.ClaimDirectorySync(ctx)
	if err != nil || next == nil {
		t.Fatal(next, err)
	}
	if _, err = db.Exec(`UPDATE platform_channel_bindings SET identity_epoch=identity_epoch+1,connection_generation=connection_generation+1,external_tenant_id='new-tenant' WHERE id=$1`, b.ID); err != nil {
		t.Fatal(err)
	}
	if err = store.PublishDirectory(ctx, *next, directorySnapshot()); err == nil {
		t.Fatal("old source published")
	}
	status, err := store.DirectoryStatus(ctx, b)
	if err != nil || status.Complete {
		t.Fatal(status, err)
	}
}
func TestDirectoryFailureExpiryAndDepartmentAuthorization(t *testing.T) {
	db, _, store, b, _, _ := directoryFixture(t)
	ctx := context.Background()
	publishDirectoryFixture(t, store, b)
	check := func(person, departments string, want *bool) {
		t.Helper()
		var match sql.NullBool
		if err := db.QueryRow(`SELECT platform_channel_directory_department_match($1,$2,$3::jsonb)`, b.ID, person, departments).Scan(&match); err != nil {
			t.Fatal(err)
		}
		if want == nil && match.Valid || want != nil && (!match.Valid || match.Bool != *want) {
			t.Fatalf("person %s match %+v", person, match)
		}
	}
	yes, no := true, false
	check("ou_a", `["od_team"]`, &yes)
	check("ou_b", `["od_team"]`, &no)
	check("ou_unknown", `["od_team"]`, nil)
	check("not_in_snapshot", `["od_team"]`, nil)
	check("not_in_snapshot", `[]`, &no)
	principal, conversation := service.NewID(), service.NewID()
	if _, err := db.Exec(`INSERT INTO manager_channel_principals(id,workspace_id,binding_id,provider,external_tenant_id,external_user_id) VALUES($1,$2,$3,'feishu',$4,'ou_a')`, principal, b.WorkspaceID, b.ID, b.TenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO manager_conversations(id,workspace_id,agent_id,channel_created_by,channel_binding_id,external_chat_id,channel_chat_type,channel_tenant_id) VALUES($1,$2,$3,$4,$5,'direct','direct',$6)`, conversation, b.WorkspaceID, b.AgentID, principal, b.ID, b.TenantID); err != nil {
		t.Fatal(err)
	}
	allowed := func(access string, want bool) {
		t.Helper()
		if _, err := db.Exec(`UPDATE platform_channel_bindings SET access=$2 WHERE id=$1`, b.ID, access); err != nil {
			t.Fatal(err)
		}
		var ok bool
		if err := db.QueryRow(`SELECT manager_channel_actor_allowed($1,$2,$3)`, principal, conversation, b.Generation).Scan(&ok); err != nil || ok != want {
			t.Fatal(access, ok, want, err)
		}
	}
	allowed(`{"mode":"allowlist","allow_department_ids":["od_team"]}`, true)
	allowed(`{"mode":"all","deny_department_ids":["od_team"]}`, false)
	allowed(`{"mode":"all","deny_department_ids":["od_child"]}`, true)
	if _, err := db.Exec(`UPDATE platform_channel_directory_users SET department_ids='["od_child"]' WHERE binding_id=$1 AND user_id='ou_a'`, b.ID); err != nil {
		t.Fatal(err)
	}
	allowed(`{"mode":"allowlist","allow_department_rules":[{"department_id":"od_team","include_descendants":false}]}`, false)
	allowed(`{"mode":"allowlist","allow_department_rules":[{"department_id":"od_team","include_descendants":true}]}`, true)
	allowed(`{"mode":"allowlist","allow_ids":["ou_a"],"deny_department_rules":[{"department_id":"od_team","include_descendants":true}]}`, false)
	allowed(`{"mode":"allowlist","allow_department_rules":[{"department_id":"od_team","include_descendants":true}],"deny_ids":["ou_a"]}`, false)
	if _, err := db.Exec(`UPDATE platform_channel_directory_users SET department_ids='["od_team"]' WHERE binding_id=$1 AND user_id='ou_a'`, b.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := store.RequestDirectorySync(ctx, b); err != nil {
		t.Fatal(err)
	}
	claim, err := store.ClaimDirectorySync(ctx)
	if err != nil || claim == nil {
		t.Fatal(claim, err)
	}
	if err = store.FailDirectory(ctx, *claim, "network", false); err != nil {
		t.Fatal(err)
	}
	check("ou_a", `["od_team"]`, &yes)
	if _, err = db.Exec(`UPDATE platform_channel_directory_state SET expires_at=now()-interval '1 second' WHERE binding_id=$1`, b.ID); err != nil {
		t.Fatal(err)
	}
	check("ou_a", `["od_team"]`, nil)
	allowed(`{"mode":"allowlist","allow_department_ids":["od_team"]}`, false)
	allowed(`{"mode":"all","deny_department_ids":["od_child"]}`, false)
	allowed(`{"mode":"all"}`, true)
	if _, err = store.RequestDirectorySync(ctx, b); err != nil {
		t.Fatal(err)
	}
	claim, err = store.ClaimDirectorySync(ctx)
	if err != nil || claim == nil {
		t.Fatal(claim, err)
	}
	if err = store.FailDirectory(ctx, *claim, "permission_denied", true); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = db.QueryRow(`SELECT count(*) FROM platform_channel_directory_users`).Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
}
func TestDirectoryHTTPAndSelectionValidation(t *testing.T) {
	db, api, store, b, actor, cookie := directoryFixture(t)
	ctx := context.Background()
	publishDirectoryFixture(t, store, b)
	mux := (&controller.Server{Channels: api, Auth: &service.AuthService{Store: &AuthStore{DB: db}}, Origin: "http://localhost"}).Handler()
	path := "/api/v1/workspaces/" + b.WorkspaceID + "/agents/" + b.AgentID + "/channels/feishu/directory"
	for _, test := range []struct {
		method, path string
		auth, csrf   bool
		want         int
	}{
		{"GET", path, false, false, 401}, {"GET", path, true, false, 200}, {"GET", path + "/users?limit=1", true, false, 200}, {"GET", path + "/departments", true, false, 200}, {"GET", path + "/users?limit=1&limit=2", true, false, 400}, {"POST", path + "/sync", true, false, 403}, {"POST", path + "/sync", true, true, 202},
	} {
		r := httptest.NewRequest(test.method, test.path, nil)
		if test.auth {
			r.Header.Set("Cookie", cookie)
		}
		if test.csrf {
			r.Header.Set("Origin", "http://localhost")
			r.Header.Set("X-CSRF-Protection", "1")
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, r)
		if rec.Code != test.want {
			t.Fatalf("%s %s got %d %s", test.method, test.path, rec.Code, rec.Body.String())
		}
	}
	usage := &service.AgentUsageService{Store: &AgentUsageStore{DB: db}}
	q, err := usage.Get(ctx, b.WorkspaceID, actor, b.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	q.UsageConfig.Access.Mode = "allowlist"
	q.UsageConfig.Access.Allow = []service.UsageSelector{{Channel: "feishu", Kind: "department", ID: "invented", BindingID: b.ID, IdentityEpoch: b.IdentityEpoch}}
	if _, err = usage.Update(ctx, b.WorkspaceID, actor, b.AgentID, q); err == nil {
		t.Fatal("invented department allowed")
	}
	q.UsageConfig.Access.Allow[0].ID = "od_team"
	if _, err = usage.Update(ctx, b.WorkspaceID, actor, b.AgentID, q); err != nil {
		t.Fatal(err)
	}

}

type fixtureDirectoryFetcher struct{ failure error }

func (f fixtureDirectoryFetcher) Fetch(_ context.Context, app, secret, tenant string) (service.DirectorySnapshot, error) {
	if app != "cli_directory" || secret != "synthetic-directory-secret" || tenant != "tenant-directory" {
		return service.DirectorySnapshot{}, errors.New("wrong credential source")
	}
	return directorySnapshot(), f.failure
}
func TestDirectoryWorkerAndPaginationVersionFence(t *testing.T) {
	_, api, store, b, actor, _ := directoryFixture(t)
	ctx := context.Background()
	worked, err := api.SyncNextDirectory(ctx, fixtureDirectoryFetcher{})
	if err != nil || !worked {
		t.Fatal(worked, err)
	}
	q := service.DirectoryQuery{Kind: "users", Limit: 1}
	first, err := api.DirectoryPage(ctx, b.WorkspaceID, actor, b.AgentID, q)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = api.RequestDirectorySync(ctx, b.WorkspaceID, actor, b.AgentID); err != nil {
		t.Fatal(err)
	}
	if _, err = api.SyncNextDirectory(ctx, fixtureDirectoryFetcher{}); err != nil {
		t.Fatal(err)
	}
	q.Cursor = *first.NextCursor
	if _, err = api.DirectoryPage(ctx, b.WorkspaceID, actor, b.AgentID, q); err == nil {
		t.Fatal("old snapshot cursor reused")
	}
	if _, err = store.RequestDirectorySync(ctx, b); err != nil {
		t.Fatal(err)
	}
	if _, err = api.SyncNextDirectory(ctx, fixtureDirectoryFetcher{failure: &service.DirectoryFetchError{Code: "scope_revoked", Invalidate: true}}); err == nil {
		t.Fatal("failure hidden")
	}
	state, err := api.DirectoryStatus(ctx, b.WorkspaceID, actor, b.AgentID)
	if err != nil || state.Complete || state.Status != "invalidated" {
		t.Fatal(state, err)
	}
}

func TestDirectoryMissingMemberCannotBypassDepartmentBlacklist(t *testing.T) {
	db, _, store, b, _, _ := directoryFixture(t)
	publishDirectoryFixture(t, store, b)
	principal, conversation := service.NewID(), service.NewID()
	if _, err := db.Exec(`INSERT INTO manager_channel_principals(id,workspace_id,binding_id,provider,external_tenant_id,external_user_id) VALUES($1,$2,$3,'feishu',$4,'new-user-not-synced')`, principal, b.WorkspaceID, b.ID, b.TenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO manager_conversations(id,workspace_id,agent_id,channel_created_by,channel_binding_id,external_chat_id,channel_chat_type,channel_tenant_id) VALUES($1,$2,$3,$4,$5,'missing-member-direct','direct',$6)`, conversation, b.WorkspaceID, b.AgentID, principal, b.ID, b.TenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE platform_channel_bindings SET access='{"mode":"all","deny_department_ids":["od_team"]}' WHERE id=$1`, b.ID); err != nil {
		t.Fatal(err)
	}
	var allowed bool
	if err := db.QueryRow(`SELECT manager_channel_actor_allowed($1,$2,$3)`, principal, conversation, b.Generation).Scan(&allowed); err != nil || allowed {
		t.Fatal("missing member bypassed department blacklist", allowed, err)
	}
}

func TestDirectoryExpiredLeaseRecoversAndOldCompletionIsFenced(t *testing.T) {
	db, _, store, b, _, _ := directoryFixture(t)
	ctx := context.Background()
	first, err := store.ClaimDirectorySync(ctx)
	if err != nil || first == nil {
		t.Fatal(first, err)
	}
	if _, err = db.Exec(`UPDATE platform_channel_directory_state SET lease_until=now()-interval '1 second' WHERE binding_id=$1`, b.ID); err != nil {
		t.Fatal(err)
	}
	second, err := store.ClaimDirectorySync(ctx)
	if err != nil || second == nil || second.Token == first.Token {
		t.Fatal(second, err)
	}
	if err = store.PublishDirectory(ctx, *first, directorySnapshot()); err == nil {
		t.Fatal("expired lease published")
	}
	if err = store.PublishDirectory(ctx, *second, directorySnapshot()); err != nil {
		t.Fatal(err)
	}
}
