package repository

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/controller"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestWecomContactPermissionHTTPReadOnlyScopedAndFresh(t *testing.T) {
	db, s, b, _ := wecomContactFixture(t)
	actor, token := setupAdminSessionWithoutCreatorRights(t, db, b.WorkspaceID)
	calls := 0
	outcomes := []error{nil, service.Fail("wecom_contacts_authorization_denied", 502, "sensitive"), service.Fail("wecom_contacts_unavailable", 502, "sensitive"), service.Fail("wecom_contacts_authentication_failed", 502, "sensitive"), service.Fail("wecom_contacts_unsupported", 502, "sensitive"), nil}
	s.Provider = contactProviderFunc(func(_ context.Context, app, secret, q string) (service.WecomContactSearch, error) {
		if app != b.AppID || secret == "" || !strings.HasPrefix(q, "KikoPermissionProbeNoSuchContact") {
			t.Fatal("probe must use the configured bot and a fixed synthetic query")
		}
		i := calls
		calls++
		if i == 5 {
			return service.WecomContactSearch{Items: []service.WecomContact{{SearchUserID: "sensitive", DisplayName: "sensitive"}}}, nil
		}
		return service.WecomContactSearch{}, outcomes[i]
	})
	api := (&controller.Server{Channels: s.Channels, WecomContacts: s, Auth: &service.AuthService{Store: &AuthStore{DB: db}}}).Handler()
	path := "/api/v1/workspaces/" + b.WorkspaceID + "/agents/" + b.AgentID + "/channels/wecom/contacts/permission"
	request := func(path string, authenticated bool) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("GET", path, nil)
		if authenticated {
			r.Header.Set("Cookie", "kiko_session="+token)
		}
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)
		return w
	}
	checkStatus := func(expected string, revision int64, checked bool) {
		t.Helper()
		w := request(path, true)
		var result service.WecomContactPermission
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "sensitive") {
			t.Fatal(w.Code, w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.Status != expected || result.BindingID != b.ID || result.Generation != b.Generation || result.Revision != revision || (result.CheckedAt != nil) != checked {
			t.Fatalf("wanted %s at revision %d, got %+v (%v)", expected, revision, result, err)
		}
	}
	adjust := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	if w := request(path, false); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := request(path+"?q=arbitrary", true); w.Code != 400 {
		t.Fatal(w.Code)
	}
	adjust("UPDATE platform_channel_bindings SET identity_verified_generation=0 WHERE id=$1", b.ID)
	checkStatus("not_checked", b.Revision, false)
	if calls != 0 {
		t.Fatal("ineligible check called provider")
	}
	adjust("UPDATE platform_channel_bindings SET identity_verified_generation=connection_generation WHERE id=$1", b.ID)
	checkStatus("available", b.Revision, true)
	checkStatus("available", b.Revision, true)
	if calls != 1 {
		t.Fatal("same revision did not reuse the empty successful result")
	}
	// Revoked admins cannot see even an in-memory result.
	adjust("UPDATE platform_members SET role='member' WHERE workspace_id=$1 AND user_id=$2", b.WorkspaceID, actor)
	if w := request(path, true); w.Code != 403 || calls != 1 {
		t.Fatal(w.Code, calls)
	}
	adjust("UPDATE platform_members SET role='admin' WHERE workspace_id=$1 AND user_id=$2", b.WorkspaceID, actor)
	for i, status := range []string{"denied", "unavailable", "authentication_failed", "unsupported", "available"} {
		adjust("UPDATE platform_channel_bindings SET config_revision=config_revision+1 WHERE id=$1", b.ID)
		checkStatus(status, b.Revision+int64(i)+1, true)
	}
	if calls != len(outcomes) {
		t.Fatal("probe did not recheck each changed revision")
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM platform_wecom_contacts WHERE binding_id=$1", b.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("probe wrote contact cache: %d (%v)", count, err)
	}
}

func TestWecomContactPermissionRejectedAfterConcurrentBindingChange(t *testing.T) {
	db, s, b, actor := wecomContactFixture(t)
	calls := 0
	s.Provider = contactProviderFunc(func(context.Context, string, string, string) (service.WecomContactSearch, error) {
		calls++
		if calls == 1 {
			if _, err := db.Exec("UPDATE platform_channel_bindings SET config_revision=config_revision+1 WHERE id=$1", b.ID); err != nil {
				return service.WecomContactSearch{}, err
			}
		}
		return service.WecomContactSearch{}, nil
	})
	ctx := context.Background()
	var domain *service.Error
	if _, err := s.ContactPermission(ctx, b.WorkspaceID, actor, b.AgentID); !errors.As(err, &domain) || domain.Code != "wecom_permission_binding_changed" {
		t.Fatalf("old authorization was published after the edit: %v", err)
	}
	v, err := s.ContactPermission(ctx, b.WorkspaceID, actor, b.AgentID)
	if err != nil || v.Status != "available" || v.Revision != b.Revision+1 || calls != 2 {
		t.Fatalf("fresh scope not checked: %+v calls=%d err=%v", v, calls, err)
	}
}
