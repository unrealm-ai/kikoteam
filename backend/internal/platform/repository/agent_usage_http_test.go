package repository

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/controller"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestAgentUsageHTTPContract(t *testing.T) {
	db, models, w, owner := modelFixture(t)
	model := saveTestModel(t, models, w, owner)
	agents := &service.AgentService{Store: NewAgentStore(db)}
	a, err := agents.Create(context.Background(), w, owner, service.AgentCreate{Name: "Usage HTTP", ModelProfileID: model.ID})
	if err != nil {
		t.Fatal(err)
	}
	token := "usage-test-session"
	if _, err = db.Exec(`INSERT INTO platform_sessions(token_hash,account_id,expires_at)VALUES($1,$2,$3)`, service.HashToken(token), owner, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	h := (&controller.Server{Auth: &service.AuthService{Store: &AuthStore{DB: db}}, Agents: agents, AgentUsage: &service.AgentUsageService{Store: &AgentUsageStore{DB: db}}, Origin: "http://localhost"}).Handler()
	base := "/api/v1/workspaces/" + w + "/agents/" + a.ID
	request := func(method, path, body string, auth, csrf bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if auth {
			r.Header.Set("Cookie", "kiko_session="+token)
		}
		if csrf {
			r.Header.Set("X-CSRF-Protection", "1")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}
	if r := request("GET", base+"/usage-config", "", false, false); r.Code != 401 {
		t.Fatal(r.Code, r.Body.String())
	}
	r := request("GET", base+"/usage-config", "", true, false)
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	var v service.AgentUsageResource
	if err = json.Unmarshal(r.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if !v.UsageConfig.Access.GroupMembersEnabled {
		t.Fatal("default group option disabled")
	}
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if r = request("PATCH", base+"/usage-config", string(raw), true, false); r.Code != 403 {
		t.Fatal("csrf", r.Code, r.Body.String())
	}
	if r = request("PATCH", base+"/usage-config", `{"version":1,"usage_config":null}`, true, true); r.Code != 400 {
		t.Fatal(r.Code, r.Body.String())
	}
	if r = request("PATCH", base+"/usage-config", strings.TrimSuffix(string(raw), "}")+`,"unexpected":true}`, true, true); r.Code != 400 {
		t.Fatal(r.Code, r.Body.String())
	}
	if r = request("PATCH", base+"/usage-config", string(raw), true, true); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	if r = request("PATCH", base+"/usage-config", string(raw), true, true); r.Code != 409 {
		t.Fatal("stale", r.Code, r.Body.String())
	}
	if r = request("PATCH", base, `{"version":2,"web_access":{"mode":"workspace","allow_member_ids":[],"deny_member_ids":[]}}`, true, true); r.Code != 400 {
		t.Fatal("shadow policy writable", r.Code, r.Body.String())
	}
}
