package repository

import (
	"context"
	"encoding/json"
	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/controller"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPeopleSelectorsDirectoryAndSourceIsolation(t *testing.T) {
	db, channels, dir, b, actor, _ := directoryFixture(t)
	publishDirectoryFixture(t, dir, b)
	s := &service.PeopleSelectorService{Secret: []byte(strings.Repeat("x", 32)), Channels: channels, Store: NewPeopleSelectorStore(db, []byte(strings.Repeat("s", 32)))}
	ctx := context.Background()
	q := service.PeopleQuery{Provider: "feishu", Purpose: "usage", Kind: "user", Limit: 1}
	p, e := s.Search(ctx, b.WorkspaceID, actor, b.AgentID, q)
	if e != nil || len(p.Items) != 1 || p.Items[0].DisplayName != "Alice" || p.NextCursor == nil {
		t.Fatal(p, e)
	}
	ref := p.Items[0].Ref
	resolved, e := s.Resolve(ctx, b.WorkspaceID, actor, b.AgentID, "usage", []service.SelectionRef{ref})
	if e != nil || !resolved.Items[0].Selectable {
		t.Fatal(resolved, e)
	}
	q.Cursor = *p.NextCursor
	p, e = s.Search(ctx, b.WorkspaceID, actor, b.AgentID, q)
	if e != nil || p.Items[0].DisplayName != "Bob" {
		t.Fatal(p, e)
	}
	q.Cursor = ""
	q.Kind = "department"
	q.Limit = 50
	p, e = s.Search(ctx, b.WorkspaceID, actor, b.AgentID, q)
	if e != nil || len(p.Items) != 1 || p.Items[0].DisplayName != "Team" {
		t.Fatal(p, e)
	}
	bad := ref
	bad.WorkspaceID = service.NewID()
	if _, e = s.Resolve(ctx, b.WorkspaceID, actor, b.AgentID, "usage", []service.SelectionRef{bad}); e == nil {
		t.Fatal("cross workspace ref accepted")
	}
	if _, e = db.Exec(`UPDATE platform_channel_bindings SET identity_epoch=identity_epoch+1 WHERE id=$1`, b.ID); e != nil {
		t.Fatal(e)
	}
	resolved, e = s.Resolve(ctx, b.WorkspaceID, actor, b.AgentID, "usage", []service.SelectionRef{ref})
	if e != nil || resolved.Items[0].Selectable || resolved.Items[0].DisabledReason != "selector_source_changed" {
		t.Fatal(resolved, e)
	}
}
func TestPeopleSelectorsWebSearchPaginationAndReadOnly(t *testing.T) {
	db, channels, _, b, actor, _ := directoryFixture(t)
	store := NewPeopleSelectorStore(db, []byte(strings.Repeat("p", 32)))
	s := &service.PeopleSelectorService{Secret: []byte(strings.Repeat("x", 32)), Channels: channels, Store: store}
	ctx := context.Background()
	for _, name := range []string{"Same Name", "Same Name"} {
		id := service.NewID()
		if _, e := db.Exec(`INSERT INTO platform_accounts(id,email,name,password_hash) VALUES($1,$2,$3,'hash')`, id, id+"@example.test", name); e != nil {
			t.Fatal(e)
		}
		if _, e := db.Exec(`INSERT INTO platform_members(workspace_id,user_id,role,status) VALUES($1,$2,'member','active')`, b.WorkspaceID, id); e != nil {
			t.Fatal(e)
		}
	}
	q := service.PeopleQuery{Provider: "web", Purpose: "usage", Kind: "user", Q: "Same", Limit: 1}
	p, e := s.Search(ctx, b.WorkspaceID, actor, b.AgentID, q)
	if e != nil || len(p.Items) != 1 || p.NextCursor == nil {
		t.Fatal(p, e)
	}
	first := p.Items[0].Ref.ExternalID
	q.Cursor = *p.NextCursor
	p, e = s.Search(ctx, b.WorkspaceID, actor, b.AgentID, q)
	if e != nil || len(p.Items) != 1 || p.Items[0].Ref.ExternalID == first || p.NextCursor != nil {
		t.Fatal(p, e)
	}
	q.Purpose = "sharing"
	if _, e = s.Search(ctx, b.WorkspaceID, actor, b.AgentID, q); e == nil {
		t.Fatal("cross purpose cursor accepted")
	}
	q.Cursor = ""
	q.Purpose = "usage"
	if _, e = s.Search(ctx, b.WorkspaceID, first, b.AgentID, q); e == nil {
		t.Fatal("member may select employee scope")
	}
}
func TestPeopleSelectorsWecomSearchRequiresBindingAndNoDepartments(t *testing.T) {
	db, wecom, b, actor := wecomContactFixture(t)
	s := &service.PeopleSelectorService{Secret: []byte(strings.Repeat("x", 32)), Channels: wecom.Channels, Wecom: wecom, Store: NewPeopleSelectorStore(db, []byte(strings.Repeat("w", 32)))}
	ctx := context.Background()
	q := service.PeopleQuery{Provider: "wecom", Purpose: "usage", Kind: "user", Q: "Alice", Limit: 50}
	p, e := s.Search(ctx, b.WorkspaceID, actor, b.AgentID, q)
	if e != nil || len(p.Items) != 2 {
		t.Fatal(p, e)
	}
	for _, v := range p.Items {
		if v.Selectable || v.Ref.ExternalID != "" || v.DisabledReason != "identity_mapping_required" {
			t.Fatal(v)
		}
	}
	q.Kind = "department"
	if _, e = s.Search(ctx, b.WorkspaceID, actor, b.AgentID, q); e == nil {
		t.Fatal("wecom department query accepted")
	}
	q.Kind = "user"
	q.Q = ""
	if _, e = s.Search(ctx, b.WorkspaceID, actor, b.AgentID, q); e == nil {
		t.Fatal("wecom directory browse accepted")
	}
	sources, e := s.Sources(ctx, b.WorkspaceID, actor, b.AgentID, "usage")
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range sources {
		if v.Provider == "wecom" && (v.SupportsBrowse || v.SupportsDepartmentTree || !v.RequiresIdentityMapping) {
			t.Fatal(v)
		}
	}
}

func TestPeopleSelectorsVerifiedWecomAndHTTP(t *testing.T) {
	db, wecom, b, actor := wecomContactFixture(t)
	api := &service.PeopleSelectorService{Secret: []byte(strings.Repeat("a", 32)), Channels: wecom.Channels, Wecom: wecom, Store: NewPeopleSelectorStore(db, []byte(strings.Repeat("b", 32)))}
	ctx := context.Background()
	v := contactStart(t, wecom, b, actor, "search-A")
	inbox := &managerrepo.Store{DB: db, ConsumeWecomControl: ConsumeWecomControl(wecom.Channels.Secret)}
	if _, e := inbox.ReceiveChannel(ctx, contactInbound(b, v.Code, "callback-A")); e != nil {
		t.Fatal(e)
	}
	q := service.PeopleQuery{Provider: "wecom", Purpose: "usage", Kind: "user", Q: "Alice", Limit: 50}
	p, e := api.Search(ctx, b.WorkspaceID, actor, b.AgentID, q)
	if e != nil || !p.Items[0].Selectable || p.Items[0].Ref.ExternalID != "callback-A" {
		t.Fatal(p, e)
	}
	ref := p.Items[0].Ref
	p, e = api.Resolve(ctx, b.WorkspaceID, actor, b.AgentID, "usage", []service.SelectionRef{ref})
	if e != nil || !p.Items[0].Selectable {
		t.Fatal(p, e)
	}
	saved := []service.UsageSelector{{Channel: "wecom", Kind: "user", ID: "callback-A", BindingID: b.ID, IdentityEpoch: b.IdentityEpoch}}
	p, e = api.ResolveSaved(ctx, b.WorkspaceID, actor, b.AgentID, "usage", saved)
	if e != nil || !p.Items[0].Selectable {
		t.Fatal(p, e)
	}
	ref.ExternalID = "search-A"
	p, e = api.Resolve(ctx, b.WorkspaceID, actor, b.AgentID, "usage", []service.SelectionRef{ref})
	if e != nil || p.Items[0].Selectable {
		t.Fatal("search ID became authorized", p, e)
	}
	// Real HTTP auth gate must reject anonymous requests before search side effects.
	auth := &service.AuthService{Store: &AuthStore{DB: db}}
	server := (&controller.Server{Auth: auth, PeopleSelectors: api}).Handler()
	req := httptest.NewRequest("GET", "/api/v1/workspaces/"+b.WorkspaceID+"/agents/"+b.AgentID+"/selectors/people?provider=wecom&purpose=usage&q=Alice", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatal(rec.Code, rec.Body.String())
	}
}

func TestPeopleSelectorHTTPQueriesAndSavedRefs(t *testing.T) {
	db, channels, dir, b, actor, cookie := directoryFixture(t)
	publishDirectoryFixture(t, dir, b)
	api := &service.PeopleSelectorService{Channels: channels, Secret: []byte(strings.Repeat("c", 32)), Store: NewPeopleSelectorStore(db, []byte(strings.Repeat("d", 32)))}
	h := (&controller.Server{Auth: &service.AuthService{Store: &AuthStore{DB: db}}, PeopleSelectors: api, Origin: "http://localhost"}).Handler()
	base := "/api/v1/workspaces/" + b.WorkspaceID + "/agents/" + b.AgentID + "/selectors/"
	for _, tc := range []struct {
		path   string
		status int
	}{{"people?provider=feishu&purpose=usage&q=Alice", 200}, {"people?provider=web&purpose=usage", 200}, {"people/sources?purpose=usage", 200}, {"departments?provider=feishu&purpose=usage", 200}, {"people?provider=web&provider=feishu&purpose=usage", 400}, {"people?provider=web&purpose=evil", 400}} {
		req := httptest.NewRequest("GET", base+tc.path, nil)
		req.Header.Set("Cookie", cookie)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tc.status {
			t.Fatalf("%s %d %s", tc.path, rec.Code, rec.Body.String())
		}
	}
	p, e := api.ResolveSaved(context.Background(), b.WorkspaceID, actor, b.AgentID, "usage", []service.UsageSelector{{Channel: "feishu", Kind: "user", ID: "ou_a", BindingID: b.ID, IdentityEpoch: b.IdentityEpoch}})
	if e != nil || !p.Items[0].Selectable || p.Items[0].DisplayName != "Alice" {
		t.Fatal(p, e)
	}
}

func TestPeopleSelectorWecomPendingVerificationCanResume(t *testing.T) {
	db, wecom, b, actor := wecomContactFixture(t)
	api := &service.PeopleSelectorService{Secret: []byte(strings.Repeat("v", 32)), Channels: wecom.Channels, Wecom: wecom, Store: NewPeopleSelectorStore(db, []byte(strings.Repeat("v", 32)))}
	ctx := context.Background()
	started := contactStart(t, wecom, b, actor, "search-A")
	// Reopening the picker searches afresh; it must return the original pending
	// request ID, not force a new Start that conflicts with the live request.
	page, err := api.Search(ctx, b.WorkspaceID, actor, b.AgentID, service.PeopleQuery{Provider: "wecom", Purpose: "usage", Kind: "user", Q: "Alice", Limit: 50})
	if err != nil || len(page.Items) != 2 {
		t.Fatal(page, err)
	}
	row := page.Items[0]
	if row.VerificationID != started.ID || row.VerificationStatus != "pending" || row.Selectable || row.Ref.ExternalID != "" {
		t.Fatal(row)
	}
	resumed, err := wecom.Get(ctx, b.WorkspaceID, actor, b.AgentID, row.VerificationID, false)
	if err != nil || resumed.ID != started.ID || resumed.Code != started.Code {
		t.Fatal(resumed, err)
	}
	encoded, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"code"`) || strings.Contains(string(encoded), started.Code) {
		t.Fatalf("search leaked code: %s", encoded)
	}
	if _, err = wecom.Get(ctx, b.WorkspaceID, service.NewID(), b.AgentID, row.VerificationID, false); err == nil {
		t.Fatal("unprivileged actor resumed verification")
	}
	if _, err = db.Exec(`UPDATE platform_channel_bindings SET connection_generation=connection_generation+1 WHERE id=$1`, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = wecom.Get(ctx, b.WorkspaceID, actor, b.AgentID, row.VerificationID, false); err == nil {
		t.Fatal("old generation verification resumed")
	}
}
