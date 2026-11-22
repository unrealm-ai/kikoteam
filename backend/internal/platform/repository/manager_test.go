package repository

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	managercontroller "github.com/unrealm-ai/kikoteam/backend/internal/manager/controller"
	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestManagerConversationAdmissionPaginationAndIsolation(t *testing.T) {
	db, models, w, owner := modelFixture(t)
	ctx := context.Background()
	m := saveTestModel(t, models, w, owner)
	agents := &service.AgentService{Store: NewAgentStore(db)}
	a, err := agents.Create(ctx, w, owner, service.AgentCreate{Name: "Runtime employee", ModelProfileID: m.ID})
	if err != nil {
		t.Fatal(err)
	}
	s := &manager.Service{Store: &managerrepo.Store{DB: db}, CursorSecret: []byte(strings.Repeat("s", 32))}
	ids := map[string]bool{}
	for range 3 {
		c, e := s.CreateConversation(ctx, w, owner, manager.CreateConversation{AgentID: a.ID})
		if e != nil {
			t.Fatal(e)
		}
		ids[c.ID] = true
		if c.ChatSessionID != nil || c.CleanupPending || c.Lifecycle != "active" {
			t.Fatal(c)
		}
	}
	if _, err = db.ExecContext(ctx, `UPDATE manager_conversations SET created_at='2026-01-01T00:00:00Z' WHERE workspace_id=$1`, w); err != nil {
		t.Fatal(err)
	}
	page, err := s.ListConversations(ctx, w, owner, "", "active", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	firstCursor := page.NextCursor
	seen := map[string]bool{}
	for {
		if len(page.Items) != 1 {
			t.Fatalf("page=%+v", page)
		}
		v := page.Items[0]
		if !ids[v.ID] || seen[v.ID] {
			t.Fatal("duplicate or unexpected conversation")
		}
		seen[v.ID] = true
		if page.NextCursor == nil {
			break
		}
		page, err = s.ListConversations(ctx, w, owner, "", "active", *page.NextCursor, 1)
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 3 {
		t.Fatal(seen)
	}
	if firstCursor == nil {
		t.Fatal("missing cursor")
	}
	if _, err = s.ListConversations(ctx, w, owner, "", "archived", *firstCursor, 1); err == nil {
		t.Fatal("cursor reused across filters")
	}
	if _, err = s.ListConversations(ctx, w, owner, "", "active", *firstCursor+"broken", 1); err == nil {
		t.Fatal("tampered cursor accepted")
	}
	for id := range ids {
		if _, err = s.Store.Conversation(ctx, w, service.NewID(), id); err == nil {
			t.Fatal("outsider read allowed")
		}
		break
	}
	var sessions, environments int
	if err = db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM manager_sessions),(SELECT count(*) FROM manager_conversation_sandboxes)`).Scan(&sessions, &environments); err != nil || sessions != 0 || environments != 0 {
		t.Fatalf("metadata created runtime work: sessions=%d environments=%d err=%v", sessions, environments, err)
	}
	disabled := false
	if _, err = agents.Update(ctx, w, owner, a.ID, service.AgentPatch{Version: a.Version, Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateConversation(ctx, w, owner, manager.CreateConversation{AgentID: a.ID}); err == nil {
		t.Fatal("disabled employee admitted new conversation")
	}
	if _, err = db.ExecContext(ctx, `UPDATE platform_members SET status='disabled' WHERE workspace_id=$1 AND user_id=$2`, w, owner); err != nil {
		t.Fatal(err)
	}
	page, err = s.ListConversations(ctx, w, owner, "", "active", *firstCursor, 1)
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("revoked list=%+v err=%v", page, err)
	}
}

func TestManagerConversationHTTP(t *testing.T) {
	db, models, workspace, owner := modelFixture(t)
	ctx := context.Background()
	m := saveTestModel(t, models, workspace, owner)
	a, err := (&service.AgentService{Store: NewAgentStore(db)}).Create(ctx, workspace, owner, service.AgentCreate{Name: "HTTP employee", ModelProfileID: m.ID})
	if err != nil {
		t.Fatal(err)
	}
	token := "manager-test-session"
	if _, err = db.ExecContext(ctx, `INSERT INTO platform_sessions(token_hash,account_id,expires_at) VALUES($1,$2,$3)`, service.HashToken(token), owner, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	auth := &service.AuthService{Store: &AuthStore{DB: db}}
	mux := http.NewServeMux()
	(&managercontroller.Server{Service: &manager.Service{Store: &managerrepo.Store{DB: db}, CursorSecret: []byte(strings.Repeat("s", 32))}, Authenticate: func(ctx context.Context, token string) (string, error) {
		v, err := auth.Me(ctx, token)
		return v.ID, err
	}, Origin: "http://localhost"}).Routes(mux)
	base := "/api/v1/workspaces/" + workspace + "/conversations"
	request := func(method, path, body, cookie, csrf string) (int, map[string]any) {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Cookie", cookie)
		r.Header.Set("X-CSRF-Protection", csrf)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if w.Header().Get("X-Request-ID") == "" {
			t.Fatal("missing request id")
		}
		return w.Code, out
	}
	cookie := "kiko_session=" + token
	if status, _ := request("GET", base, "", "", ""); status != 401 {
		t.Fatal(status)
	}
	body := `{"agent_id":"` + a.ID + `"}`
	if status, _ := request("POST", base, body, cookie, ""); status != 403 {
		t.Fatal(status)
	}
	if status, _ := request("POST", base, `{"agent_id":"`+a.ID+`","sandbox":"x"}`, cookie, "1"); status != 400 {
		t.Fatal(status)
	}
	status, out := request("POST", base, body, cookie, "1")
	if status != 201 {
		t.Fatal(status, out)
	}
	id := out["id"].(string)
	if status, _ = request("GET", base+"/"+id, "", cookie, ""); status != 200 {
		t.Fatal(status)
	}
	if status, _ = request("GET", base+"?limit=1&limit=2", "", cookie, ""); status != 400 {
		t.Fatal(status)
	}
	if status, _ = request("GET", strings.Replace(base, workspace, service.NewID(), 1)+"/"+id, "", cookie, ""); status != 404 {
		t.Fatal(status)
	}
}
