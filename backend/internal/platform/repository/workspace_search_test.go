package repository

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	mc "github.com/unrealm-ai/kikoteam/backend/internal/manager/controller"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestWorkspaceSearchFiltersPrivateConversationAndTasks(t *testing.T) {
	db, m, _, w, owner, agent, _ := managerFixture(t)
	ctx := t.Context()
	member, outsider := manager.NewID(), manager.NewID()
	for _, id := range []string{member, outsider} {
		insertWorkspaceAccount(t, db, id, id+"@example.test")
	}
	reviewExec(t, db, `INSERT INTO platform_members(workspace_id,user_id,role,status)VALUES($1,$2,'member','active')`, w, member)
	reviewExec(t, db, `UPDATE platform_agents SET name='Report Worker' WHERE id=$1`, agent)
	setup := func(actor, title string) (string, string) {
		t.Helper()
		c, err := m.ResolveWebConversation(ctx, w, actor, agent)
		if err != nil {
			t.Fatal(err)
		}
		reviewExec(t, db, `UPDATE manager_conversations SET title=$2 WHERE id=$1`, c.ID, title+" Report Chat")
		if _, _, err = m.CreateSession(ctx, w, actor, "", manager.CreateSession{ConversationID: c.ID, Mode: "chat"}); err != nil {
			t.Fatal(err)
		}
		taskTitle := title + " Report Task"
		req := manager.CreateSession{ConversationID: c.ID, Title: &taskTitle, Mode: "task"}
		req.Input = &struct {
			Text string `json:"text"`
		}{Text: "do not expose this body"}
		v, _, err := m.CreateSession(ctx, w, actor, manager.NewID(), req)
		if err != nil {
			t.Fatal(err)
		}
		return c.ID, *v.TaskID
	}
	privateChat, privateTask := setup(owner, "Owner")
	myChat, myTask := setup(member, "Member")
	mux := http.NewServeMux()
	(&mc.Server{Service: m, SearchAgents: &service.AgentService{Store: NewAgentStore(db)}, Authenticate: func(_ context.Context, token string) (string, error) { return token, nil }}).Routes(mux)
	search := func(actor, q string, expected int) string {
		t.Helper()
		req := httptest.NewRequest("GET", "/api/v1/workspaces/"+w+"/search?"+q, nil)
		if actor != "" {
			req.AddCookie(&http.Cookie{Name: "kiko_session", Value: actor})
		}
		out := httptest.NewRecorder()
		mux.ServeHTTP(out, req)
		if out.Code != expected {
			t.Fatalf("HTTP %d, want %d: %s", out.Code, expected, out.Body)
		}
		return out.Body.String()
	}
	search("", "q=report", 401)
	blocked := search(outsider, "q=report", 403)
	if strings.Contains(blocked, myChat) {
		t.Fatal(blocked)
	}
	search(member, "q=a&q=b", 400)
	search(member, "q="+url.QueryEscape(strings.Repeat("a", 121)), 400)
	body := search(member, "q=report", 200)
	var result struct {
		Items []struct {
			ID, Kind string
			AgentID  string `json:"agent_id"`
		}
	}
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"employee": agent, "task": myTask, "conversation": myChat}
	for _, v := range result.Items {
		if want[v.Kind] != v.ID || (v.Kind == "conversation" && v.AgentID != agent) {
			t.Fatal(result, body)
		}
		delete(want, v.Kind)
	}
	if len(want) != 0 || strings.Contains(body, privateChat) || strings.Contains(body, privateTask) || strings.Contains(body, "do not expose") || strings.Contains(body, "private employee instruction") {
		t.Fatal(body, want)
	}
	if no := search(member, "q="+url.QueryEscape("%"), 200); !strings.Contains(no, `"items":[]`) {
		t.Fatal(no)
	}
}
