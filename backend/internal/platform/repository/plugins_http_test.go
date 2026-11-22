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

func TestPluginHTTPContract(t *testing.T) {
	db, models, w, owner := modelFixture(t)
	m := saveTestModel(t, models, w, owner)
	employee, err := (&service.AgentService{Store: NewAgentStore(db)}).Create(context.Background(), w, owner, service.AgentCreate{Name: "Employee", ModelProfileID: m.ID})
	if err != nil {
		t.Fatal(err)
	}
	token := "plugin-test-session"
	if _, err = db.Exec(`INSERT INTO platform_sessions(token_hash,account_id,expires_at) VALUES($1,$2,$3)`, service.HashToken(token), owner, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	h := (&controller.Server{Auth: &service.AuthService{Store: &AuthStore{DB: db}}, Plugins: &service.PluginService{Store: NewPluginStore(db), Secret: []byte("test-only-plugin-secret-32-characters"), Probe: pluginProbeFixture{}}, Origin: "http://localhost"}).Handler()
	request := func(method, path, body, cookie string) (int, map[string]any) {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("X-CSRF-Protection", "1")
		r.Header.Set("Cookie", cookie)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		var out map[string]any
		if rec.Body.Len() > 0 {
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
		}
		if strings.Contains(rec.Body.String(), "super-secret") {
			t.Fatal("secret leaked")
		}
		return rec.Code, out
	}
	cookie := "kiko_session=" + token
	base := "/api/v1/workspaces/" + w + "/plugins"
	if status, _ := request("GET", base, "", ""); status != 401 {
		t.Fatal(status)
	}
	if status, _ := request("POST", base, `{"name":"MCP","endpoint":"http://example.com","auth_type":"none"}`, cookie); status != 400 {
		t.Fatal(status)
	}
	status, out := request("POST", base, `{"name":"MCP","endpoint":"https://example.com/mcp","auth_type":"bearer"}`, cookie)
	if status != 201 {
		t.Fatal(status, out)
	}
	id := out["id"].(string)
	version := out["version_id"].(string)
	account := "/api/v1/workspaces/" + w + "/agents/" + employee.ID + "/plugin-accounts/" + id
	if status, _ := request("GET", account, "", cookie); status != 404 {
		t.Fatal(status)
	}
	body := `{"name":"Business","plugin_version_id":"` + version + `","secret":"super-secret","version":0}`
	if status, out := request("PUT", account, body, cookie); status != 200 {
		t.Fatal(status, out)
	}
	if status, _ := request("PUT", account, body, cookie); status != 409 {
		t.Fatal(status)
	}
	if status, out := request("DELETE", account, `{"version":1}`, cookie); status != 204 {
		t.Fatal(status, out)
	}
}
