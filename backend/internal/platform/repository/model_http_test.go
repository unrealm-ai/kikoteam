package repository

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/controller"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/integration"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestModelHTTPThreeProtocolLifecycle(t *testing.T) {
	db := workspaceTestDB(t)
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var req map[string]any
		if e := json.NewDecoder(r.Body).Decode(&req); e != nil {
			t.Error(e)
			w.WriteHeader(400)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-key" && r.Header.Get("X-Api-Key") != "test-key" {
			t.Error("credential not forwarded correctly")
			w.WriteHeader(401)
			return
		}
		// Vision deliberately unsupported; text must pass and allow saving.
		raw, e := json.Marshal(req)
		if e != nil {
			t.Error(e)
			return
		}
		if strings.Contains(string(raw), "image") {
			w.WriteHeader(400)
			if _, e := w.Write([]byte(`{"error":{"message":"image input is not supported"}}`)); e != nil {
				t.Error(e)
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/chat/completions":
			if _, e := w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"OK"}}]}`)); e != nil {
				t.Error(e)
			}
		case "/v1/responses":
			if _, e := w.Write([]byte(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"OK"}]}]}`)); e != nil {
				t.Error(e)
			}
		case "/v1/messages":
			if _, e := w.Write([]byte(`{"stop_reason":"end_turn","content":[{"type":"text","text":"OK"}]}`)); e != nil {
				t.Error(e)
			}
		default:
			t.Error("wrong protocol URL", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer provider.Close()
	secret := []byte("test-only-secret-test-only-secret")
	auth := &service.AuthService{Store: &AuthStore{DB: db}, Secret: secret, EmailCodeDisabled: true}
	models := &service.ModelService{AllowLoopbackHTTP: true, Store: NewModelStore(db), Prober: &integration.ModelProber{AllowLoopbackHTTP: true}, NormalizeEndpoint: integration.NormalizeModelEndpoint, Secret: secret}
	h := (&controller.Server{Auth: auth, Models: models, Workspaces: &service.WorkspaceService{Store: NewWorkspaceStore(db)}, Origin: "http://localhost"}).Handler()
	request := func(method, path string, body any, cookie string) (int, map[string]any, string) {
		data, e := json.Marshal(body)
		if e != nil {
			t.Fatal(e)
		}
		r := httptest.NewRequest(method, path, strings.NewReader(string(data)))
		r.Header.Set("X-CSRF-Protection", "1")
		r.Header.Set("Origin", "http://localhost")
		r.Header.Set("Cookie", cookie)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var out map[string]any
		if e = json.Unmarshal(w.Body.Bytes(), &out); e != nil {
			t.Fatal(e)
		}
		if w.Header().Get("X-Request-ID") == "" {
			t.Fatal("missing request id")
		}
		return w.Code, out, w.Header().Get("Set-Cookie")
	}
	code, _, cookie := request("POST", "/api/v1/auth/registrations", map[string]any{"method": "password", "email": "model-http@example.test", "name": "Models", "password": "test-password"}, "")
	if code != 201 {
		t.Fatal("registration", code)
	}
	code, ws, _ := request("GET", "/api/v1/workspaces", nil, cookie)
	if code != 200 {
		t.Fatal(code)
	}
	wid := ws["items"].([]any)[0].(map[string]any)["id"].(string)
	base := "/api/v1/workspaces/" + wid + "/model-profiles"
	for _, protocol := range []string{"chat_completions", "openai_responses", "anthropic_messages"} {
		t.Run(protocol, func(t *testing.T) {
			candidate := map[string]any{"name": protocol, "protocol": protocol, "base_url": provider.URL, "model_id": "fixture", "api_key": "test-key"}
			code, test, _ := request("POST", base+"/test", candidate, cookie)
			if code != 200 {
				t.Fatal(code, test)
			}
			if test["text"].(map[string]any)["status"] != "passed" {
				t.Fatal(test)
			}
			if test["image"].(map[string]any)["status"] != "unsupported" {
				t.Fatal(test)
			}
			candidate["test_receipt"] = test["receipt"]
			code, saved, _ := request("POST", base, candidate, cookie)
			if code != 201 {
				t.Fatal(code, saved)
			}
			if saved["api_key"] != nil || saved["credential"] != nil || saved["has_key"] != true || saved["base_url"] != provider.URL+"/v1" {
				t.Fatal("unsafe or noncanonical save", saved)
			}
			id := saved["id"].(string)
			code, replay, _ := request("POST", base, candidate, cookie)
			if code != 200 || replay["id"] != id {
				t.Fatal("receipt retry created duplicate", code, replay)
			}
			code, _, _ = request("PATCH", base+"/"+id, map[string]any{"version": 1, "enabled": false, "model_id": "bypass"}, cookie)
			if code != 400 {
				t.Fatal("status bypass", code)
			}
			code, revoked, _ := request("POST", base+"/"+id+"/revoke-key", map[string]any{"version": 1}, cookie)
			if code != 200 || revoked["enabled"] != false || revoked["has_key"] != false {
				t.Fatal(code, revoked)
			}
			code, _, _ = request("PATCH", base+"/"+id, map[string]any{"version": 2, "enabled": true}, cookie)
			if code != 409 {
				t.Fatal("revoked enabled", code)
			}
		})
	}
	if calls.Load() != 6 {
		t.Fatal("expected one text and one image call per protocol", calls.Load())
	}
	code, _, _ = request("GET", base, nil, "")
	if code != 401 {
		t.Fatal("anonymous allowed", code)
	}
	code, _, _ = request("POST", base+"/test", map[string]any{"model_profile_id": "invalid"}, cookie)
	if code != 400 {
		t.Fatal("bad ID", code)
	}
	code, _, _ = request("GET", base+"/"+fmt.Sprintf("%036d", 1), nil, cookie)
	if code != 400 {
		t.Fatal("bad path ID", code)
	}
}
