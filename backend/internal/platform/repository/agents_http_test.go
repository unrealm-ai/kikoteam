package repository

import (
	"context"
	"encoding/json"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/controller"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAgentHTTPContract(t *testing.T) {
	db, models, w, owner := modelFixture(t)
	m := saveTestModel(t, models, w, owner)
	token := "agent-test-session"
	if _, e := db.Exec(`INSERT INTO platform_sessions(token_hash,account_id,expires_at) VALUES($1,$2,$3)`, service.HashToken(token), owner, time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	h := (&controller.Server{Auth: &service.AuthService{Store: &AuthStore{DB: db}}, Agents: &service.AgentService{Store: NewAgentStore(db)}, Origin: "http://localhost"}).Handler()
	request := func(method, path, body, cookie string) (int, map[string]any) {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("X-CSRF-Protection", "1")
		r.Header.Set("Cookie", cookie)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		var out map[string]any
		if e := json.Unmarshal(rec.Body.Bytes(), &out); e != nil {
			t.Fatal(e)
		}
		return rec.Code, out
	}
	cookie := "kiko_session=" + token
	base := "/api/v1/workspaces/" + w + "/agents"
	if status, _ := request("GET", base, "", ""); status != 401 {
		t.Fatal(status)
	}
	for _, path := range []string{"/api/v1/workspaces/bad/agents", base + "/bad"} {
		if status, _ := request("GET", path, "", cookie); status != 400 {
			t.Fatal(path, status)
		}
	}
	for _, extra := range []string{`,"harness":"claude"`, `,"thinking_level":"high"`, `,"instruction":null`} {
		if status, _ := request("POST", base, `{"name":"Test","model_profile_id":"`+m.ID+`"`+extra+`}`, cookie); status != 400 {
			t.Fatal(status)
		}
	}
	status, out := request("POST", base, `{"name":"Test","model_profile_id":"`+m.ID+`"}`, cookie)
	if status != 201 {
		t.Fatal(status, out)
	}
	id := out["id"].(string)
	if status, _ := request("PATCH", base+"/"+id, `{"version":1,"instruction":null}`, cookie); status != 400 {
		t.Fatal(status)
	}
	if status, _ := request("PATCH", base+"/"+id, `{"version":1,"instruction":""}`, cookie); status != 200 {
		t.Fatal(status)
	}
	if status, _ := request("PATCH", base+"/"+id, `{"version":1,"name":"stale"}`, cookie); status != 409 {
		t.Fatal(status)
	}
	if _, e := (&service.AgentService{Store: NewAgentStore(db)}).Get(context.Background(), w, owner, id); e != nil {
		t.Fatal(e)
	}
}
