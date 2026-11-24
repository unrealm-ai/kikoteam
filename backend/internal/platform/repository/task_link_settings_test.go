package repository

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/controller"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestTaskLinkSettingsPlatformHTTP(t *testing.T) {
	db, m, _, _, actor, _, cookie := managerFixture(t)
	store := m.Store.(*managerrepo.Store)
	api := (&controller.Server{
		Auth:      &service.AuthService{Store: &AuthStore{DB: db}},
		TaskLinks: &service.TaskLinkSettingsService{Store: &TaskLinkSettingsStore{DB: db}},
		Origin:    "https://example.test",
	}).Handler()
	call := func(method, body string, auth, csrf bool, status int) service.TaskLinkSettings {
		t.Helper()
		r := httptest.NewRequest(method, "/api/v1/platform/task-link-settings", strings.NewReader(body))
		if auth {
			r.Header.Set("Cookie", cookie)
		}
		if csrf {
			r.Header.Set("Origin", "https://example.test")
			r.Header.Set("X-CSRF-Protection", "1")
		}
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s: got HTTP %d, want %d: %s", method, w.Code, status, w.Body)
		}
		var out service.TaskLinkSettings
		if status == 200 {
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
		}
		return out
	}
	checkEnabled := func(want bool) {
		t.Helper()
		got, err := store.TaskDetailLinksEnabled(t.Context())
		if err != nil || got != want {
			t.Fatalf("dispatch enablement got %t, want %t: %v", got, want, err)
		}
	}
	call(http.MethodGet, "", false, false, 401)
	call(http.MethodGet, "", true, false, 403)
	const first = `{"expected_version":0,"origin":"https://Example.TEST:443/","enabled":true}`
	call(http.MethodPatch, first, true, true, 403)
	reviewExec(t, db, `UPDATE platform_accounts SET is_platform_admin=true WHERE id=$1`, actor)
	call(http.MethodPatch, first, true, false, 403)
	if got := call(http.MethodGet, "", true, false, 200); got != (service.TaskLinkSettings{Enabled: true}) {
		t.Fatalf("unexpected default: %+v", got)
	}
	checkEnabled(false)
	const invalid = `{"expected_version":0,"origin":"https://example.test/tasks","enabled":true}`
	call(http.MethodPatch, invalid, true, true, 400)
	if got := call(http.MethodPatch, first, true, true, 200); got != (service.TaskLinkSettings{Version: 1, Origin: "https://example.test", Enabled: true}) {
		t.Fatalf("expected canonical URL: %+v", got)
	}
	checkEnabled(true)
	call(http.MethodPatch, first, true, true, 409)
	call(http.MethodPatch, `{"expected_version":1,"origin":"https://example.test","enabled":false}`, true, true, 200)
	checkEnabled(false)
	call(http.MethodPatch, `{"expected_version":2,"origin":"","enabled":false}`, true, true, 200)
	if got := call(http.MethodGet, "", true, false, 200); got.Enabled || got.Origin != "" {
		t.Fatalf("clearing the domain must preserve explicit disable: %+v", got)
	}
	call(http.MethodPatch, `{"expected_version":3,"origin":"https://example.test","enabled":false}`, true, true, 200)
	checkEnabled(false)
	if got := call(http.MethodPatch, `{"expected_version":4,"origin":"http://127.0.0.1:18082/","enabled":true}`, true, true, 200); got != (service.TaskLinkSettings{Version: 5, Origin: "http://127.0.0.1:18082", Enabled: true}) {
		t.Fatalf("expected an explicitly configured intranet origin: %+v", got)
	}
	checkEnabled(true)
}
