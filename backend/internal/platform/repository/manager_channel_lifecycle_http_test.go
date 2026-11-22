package repository

import (
	"bytes"
	"encoding/json"
	managercontroller "github.com/unrealm-ai/kikoteam/backend/internal/manager/controller"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestManagerGroupLifecycleHTTP(t *testing.T) {
	_, m, s, _, _, _, binding := channelManagerFixture(t)
	token := strings.Repeat("h", 40)
	mux := http.NewServeMux()
	(&managercontroller.ChannelServer{Store: s, Manager: m, Token: token}).Routes(mux)
	call := func(path string, body any, mode string, want int) *httptest.ResponseRecorder {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("POST", path, bytes.NewReader(raw))
		if mode != "anonymous" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		if mode == "browser" {
			r.Header.Set("Origin", "https://example.com")
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %d: %s", path, w.Code, w.Body.String())
		}
		return w
	}
	event := lifecycleFixture(binding, "http-add", "bot_added", 1000)
	for _, mode := range []string{"anonymous", "browser"} {
		for _, path := range []string{"/api/internal/v1/channel-lifecycle", "/api/internal/v1/channel-groups/claim", "/api/internal/v1/channel-groups/" + manager.NewID() + "/complete"} {
			call(path, event, mode, 401)
		}
	}
	call("/api/internal/v1/channel-lifecycle", map[string]any{"extra": "field"}, "service", 400)
	call("/api/internal/v1/channel-lifecycle", manager.ChannelLifecycle{}, "service", 400)
	wrong := event
	wrong.TenantID = "foreign"
	call("/api/internal/v1/channel-lifecycle", wrong, "service", 409)
	call("/api/internal/v1/channel-lifecycle", event, "service", 200)
	w := call("/api/internal/v1/channel-lifecycle", event, "service", 200)
	var receipt manager.ChannelReceipt
	if err := json.Unmarshal(w.Body.Bytes(), &receipt); err != nil || receipt.Status != "duplicate" {
		t.Fatal(receipt, err)
	}
	var jobs struct {
		Items []manager.ChannelGroupCheck `json:"items"`
	}
	w = call("/api/internal/v1/channel-groups/claim", nil, "service", 200)
	if err := json.Unmarshal(w.Body.Bytes(), &jobs); err != nil || len(jobs.Items) != 1 {
		t.Fatal(jobs, err)
	}
	yes := true
	job := jobs.Items[0]
	call("/api/internal/v1/channel-groups/"+job.ID+"/complete", manager.ChannelGroupCompletion{LeaseToken: manager.NewID(), Present: &yes}, "service", 409)
	call("/api/internal/v1/channel-groups/"+job.ID+"/complete", manager.ChannelGroupCompletion{LeaseToken: job.LeaseToken, Present: &yes}, "service", 200)
}
