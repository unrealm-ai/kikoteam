package repository

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/controller"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestPlatformTimezoneAdminReschedulesBusinessTriggers(t *testing.T) {
	db, m, _, workspace, actor, agent, cookie := managerFixture(t)
	ctx := t.Context()
	store := m.Store.(*managerrepo.Store)
	api := (&controller.Server{Auth: &service.AuthService{Store: &AuthStore{DB: db}}, Timezone: &service.TimezoneSettingsService{Store: &TimezoneSettingsStore{DB: db}}, Origin: "https://example.test"}).Handler()
	call := func(method, body string, csrf bool, want int) service.TimezoneSettings {
		t.Helper()
		req := httptest.NewRequest(method, "/api/v1/platform/timezone-settings", strings.NewReader(body))
		req.Header.Set("Cookie", cookie)
		if csrf {
			req.Header.Set("Origin", "https://example.test")
			req.Header.Set("X-CSRF-Protection", "1")
		}
		out := httptest.NewRecorder()
		api.ServeHTTP(out, req)
		if out.Code != want {
			t.Fatalf("got HTTP %d, want %d: %s", out.Code, want, out.Body)
		}
		var got service.TimezoneSettings
		if want == 200 {
			if err := json.Unmarshal(out.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
		}
		return got
	}
	const patch = `{"expected_version":0,"timezone":"America/New_York"}`
	call("GET", "", false, 403)
	call("PATCH", patch, true, 403)
	reviewExec(t, db, `UPDATE platform_accounts SET is_platform_admin=true WHERE id=$1`, actor)
	call("PATCH", patch, false, 403)
	if got := call("GET", "", false, 200); got != (service.TimezoneSettings{Timezone: service.DefaultPlatformTimezone}) {
		t.Fatal(got)
	}
	call("PATCH", `{"expected_version":0,"timezone":"Local"}`, true, 400)
	write := manager.TriggerWrite{Kind: "schedule", Enabled: true, Config: json.RawMessage(`{"cron":"0 9 * * *","description":"daily report","timezone":"UTC"}`), AllowedActions: []string{"create_task"}}
	trigger, err := store.SaveTrigger(ctx, workspace, actor, agent, "", write)
	if err != nil {
		t.Fatal(err)
	}
	old, err := manager.ParseTriggerSchedule(trigger.Config)
	if err != nil || old.Timezone != service.DefaultPlatformTimezone {
		t.Fatal(old, err)
	}
	due := time.Now().UTC().Truncate(time.Minute).Add(-time.Minute)
	reviewExec(t, db, `UPDATE manager_triggers SET next_due=$2 WHERE id=$1`, trigger.ID, due)
	if got := call("PATCH", patch, true, 200); got != (service.TimezoneSettings{Version: 1, Timezone: "America/New_York"}) {
		t.Fatal(got)
	}
	call("PATCH", patch, true, 409)
	stale, err := store.TickTriggerSchedule(ctx, trigger.ID, manager.TriggerScheduleTick{Version: trigger.Version, ExpectedDue: due, RecoveryStartedAt: due.Add(-time.Minute)})
	if err != nil || stale.Status != "stale" {
		t.Fatal(stale, err)
	}
	poll, err := store.PollTriggerSchedules(ctx, 100)
	if err != nil || len(poll.Items) != 1 {
		t.Fatal(poll, err)
	}
	updated, err := store.GetTrigger(ctx, workspace, actor, agent, trigger.ID)
	if err != nil || updated.Version != trigger.Version || updated.NextDue == nil {
		t.Fatal(updated, err)
	}
	zone, err := manager.ParseTriggerSchedule(updated.Config)
	if err != nil || zone.Timezone != "America/New_York" {
		t.Fatal(zone, err)
	}
	loc, err := time.LoadLocation(zone.Timezone)
	if err != nil || updated.NextDue.In(loc).Hour() != 9 || !updated.NextDue.After(updated.UpdatedAt) {
		t.Fatal(updated.NextDue, err)
	}
}
