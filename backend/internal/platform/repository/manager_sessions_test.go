package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	managercontroller "github.com/unrealm-ai/kikoteam/backend/internal/manager/controller"
	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func managerFixture(t *testing.T) (*sql.DB, *manager.Service, *http.ServeMux, string, string, string, string) {
	t.Helper()
	db, models, w, actor := modelFixture(t)
	ctx := context.Background()
	m := saveTestModel(t, models, w, actor)
	if _, err := models.Change(ctx, w, actor, m.ID, "set-default", m.Version, true); err != nil {
		t.Fatal(err)
	}
	a, err := (&service.AgentService{Store: NewAgentStore(db)}).Create(ctx, w, actor, service.AgentCreate{Name: "Worker", Instruction: "private employee instruction", ModelProfileID: m.ID})
	if err != nil {
		t.Fatal(err)
	}
	// Runtime tests explicitly use an open employee; product creation defaults
	// remain creator-only and are tested by AgentUsage tests.
	if _, err = db.ExecContext(ctx, `UPDATE platform_agents SET usage_config=jsonb_set(usage_config,'{access}', '{"mode":"all","allow":[],"deny":[]}'::jsonb) WHERE id=$1`, a.ID); err != nil {
		t.Fatal(err)
	}
	s := &manager.Service{Store: &managerrepo.Store{DB: db}, CursorSecret: []byte(strings.Repeat("s", 32))}
	auth := &service.AuthService{Store: &AuthStore{DB: db}}
	token := "runtime-fixture-session"
	if _, err = db.ExecContext(ctx, `INSERT INTO platform_sessions(token_hash,account_id,expires_at) VALUES($1,$2,$3)`, service.HashToken(token), actor, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	(&managercontroller.Server{Service: s, Origin: "http://localhost", Authenticate: func(ctx context.Context, token string) (string, error) {
		v, err := auth.Me(ctx, token)
		return v.ID, err
	}}).Routes(mux)
	return db, s, mux, w, actor, a.ID, "kiko_session=" + token
}

func TestManagerSessionAcceptanceControlHistoryAndHTTP(t *testing.T) {
	db, s, mux, w, actor, agent, cookie := managerFixture(t)
	ctx := context.Background()
	c, err := s.CreateConversation(ctx, w, actor, manager.CreateConversation{AgentID: agent})
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/workspaces/" + w + "/sessions"
	request := func(method, path, body, key string) (*httptest.ResponseRecorder, map[string]any) {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Cookie", cookie)
		r.Header.Set("X-CSRF-Protection", "1")
		r.Header.Set("Idempotency-Key", key)
		out := httptest.NewRecorder()
		mux.ServeHTTP(out, r)
		var v map[string]any
		if err := json.Unmarshal(out.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		return out, v
	}
	create := `{"conversation_id":"` + c.ID + `","mode":"chat","input":{"text":"first"}}`
	for _, body := range []string{`{"conversation_id":"` + c.ID + `","mode":"chat","input":null}`, `{"conversation_id":"` + c.ID + `","mode":"chat","input":{"text":"a","text":"b"}}`, create} {
		key := "first-key"
		if body == create {
			key = ""
		}
		out, _ := request("POST", base, body, key)
		if out.Code != 400 {
			t.Fatalf("invalid accepted %d %s", out.Code, out.Body.String())
		}
	}
	out, v := request("POST", base, create, "first-key")
	if out.Code != 201 {
		t.Fatal(out.Code, v)
	}
	id := v["id"].(string)
	path := base + "/" + id
	if strings.Contains(out.Body.String(), "private employee instruction") || strings.Contains(out.Body.String(), "base_url") {
		t.Fatal("private configuration leaked")
	}
	initial := v["initial_input_id"].(string)
	out, v = request("POST", base, create, "first-key")
	if out.Code != 200 || v["id"] != id || out.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatal(out.Code, v)
	}
	out, _ = request("POST", base, strings.Replace(create, "first", "different", 1), "first-key")
	if out.Code != 409 {
		t.Fatal(out.Code)
	}
	out, _ = request("POST", base, create, "another-key")
	if out.Code != 409 {
		t.Fatal(out.Code)
	}
	out, v = request("POST", path+"/inputs", `{"text":"second"}`, "second-key")
	if out.Code != 201 {
		t.Fatal(out.Code, v)
	}
	second := v["id"].(string)
	out, v = request("POST", path+"/inputs/"+second+"/withdraw", `{}`, "")
	if out.Code != 200 || v["status"] != "canceled" {
		t.Fatal(out.Code, v)
	}
	out, v = request("POST", path+"/inputs/"+second+"/withdraw", `{}`, "")
	if out.Code != 200 || v["cancel_reason"] != "withdrawn" {
		t.Fatal(out.Code, v)
	}
	out, v = request("GET", path+"/events?limit=2", "", "")
	if out.Code != 200 {
		t.Fatal(out.Code, v)
	}
	cursor := v["next_cursor"].(string)
	if _, _, err = s.SubmitInput(ctx, w, actor, id, "third-key", "third"); err != nil {
		t.Fatal(err)
	}
	for v["next_cursor"] != nil {
		if v["resume_cursor"] != nil {
			t.Fatal("unfinished history exposed a stream resume cursor")
		}
		out, v = request("GET", path+"/events?limit=2&cursor="+cursor, "", "")
		if out.Code != 200 {
			t.Fatal(out.Code, v)
		}
		for _, event := range v["items"].([]any) {
			ev := event.(map[string]any)
			if ev["type"] == "user.message" {
				for _, field := range []string{"message_id", "input_seq", "submitted_by", "content", "status", "cancel_reason", "rejection", "received_at", "started_at", "finished_at"} {
					if _, ok := ev[field]; !ok {
						t.Fatalf("input snapshot missing %s: %v", field, ev)
					}
				}
				content := ev["content"].([]any)
				if content[0].(map[string]any)["text"] == "third" {
					t.Fatal("new event entered frozen history")
				}
			}
		}
		if v["next_cursor"] != nil {
			cursor = v["next_cursor"].(string)
		}
	}
	events, _, err := s.StreamEvents(ctx, w, actor, id, "", "", v["resume_cursor"].(string))
	if err != nil || len(events) == 0 {
		t.Fatalf("history/stream gap %d %v", len(events), err)
	}
	out, v = request("POST", path+"/stop", `{}`, "")
	if out.Code != 200 || v["execution"].(map[string]any)["state"] != "idle" {
		t.Fatal(out.Code, v)
	}
	var count int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM manager_session_events WHERE session_id=$1`, id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	request("POST", path+"/stop", `{}`, "")
	var after int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM manager_session_events WHERE session_id=$1`, id).Scan(&after); err != nil || count != after {
		t.Fatalf("duplicate stop event %d %d %v", count, after, err)
	}
	out, v = request("POST", path+"/inputs", `{"text":"after stop"}`, "new-key")
	if out.Code != 201 || v["status"] != "queued" {
		t.Fatal(out.Code, v)
	}
	out, v = request("POST", "/api/v1/workspaces/"+w+"/conversations/"+c.ID+"/archive", `{}`, "")
	if out.Code != 409 {
		t.Fatal("queued archive must conflict", out.Code, v)
	}
	out, v = request("POST", path+"/archive", `{}`, "")
	if out.Code != 409 || !strings.Contains(out.Body.String(), "session_busy") {
		t.Fatal(out.Code, v)
	}
	request("POST", path+"/stop", `{}`, "")
	out, v = request("POST", "/api/v1/workspaces/"+w+"/conversations/"+c.ID+"/archive", `{}`, "")
	if out.Code != 200 || v["lifecycle"] != "archived" {
		t.Fatal(out.Code, v)
	}
	out, _ = request("POST", path+"/inputs", `{"text":"forbidden"}`, "blocked-key")
	if out.Code != 409 {
		t.Fatal(out.Code)
	}
	out, v = request("POST", base, create, "first-key")
	if out.Code != 200 || v["id"] != id || v["initial_input_id"] != initial {
		t.Fatal(out.Code, v)
	}
	out, v = request("GET", path+"/inputs/"+initial, "", "")
	if out.Code != 200 || v["cancel_reason"] != "session_stopped" {
		t.Fatal(out.Code, v)
	}
}

func TestManagerTaskCreationConcurrentInputDedup(t *testing.T) {
	db, s, _, w, actor, agent, _ := managerFixture(t)
	ctx := context.Background()
	c, err := s.CreateConversation(ctx, w, actor, manager.CreateConversation{AgentID: agent})
	if err != nil {
		t.Fatal(err)
	}
	req := manager.CreateSession{ConversationID: c.ID, Mode: "task", Input: &struct {
		Text string `json:"text"`
	}{Text: "do work"}}
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, _, err := s.CreateSession(ctx, w, actor, "same-key", req)
			if err != nil {
				t.Error(err)
				return
			}
			ids <- v.ID
		}()
	}
	wg.Wait()
	close(ids)
	id := ""
	for v := range ids {
		if id != "" && id != v {
			t.Fatal("duplicate task Session")
		}
		id = v
	}
	if id == "" {
		t.Fatal("no successful acceptance")
	}
	var tasks, inputs int
	if err = db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM manager_tasks WHERE conversation_id=$1),(SELECT count(*) FROM manager_inputs WHERE session_id=$2)`, c.ID, id).Scan(&tasks, &inputs); err != nil || tasks != 1 || inputs != 1 {
		t.Fatal(tasks, inputs, err)
	}
}
