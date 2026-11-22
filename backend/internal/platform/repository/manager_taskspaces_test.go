package repository

import (
	"context"
	"encoding/json"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	platform "github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

func TestTaskSpaceKeysIsolationAndGrants(t *testing.T) {
	db, m, _, w, owner, agent, _ := managerFixture(t)
	ctx := context.Background()
	key := "order-42"
	var ids [4]string
	var errs [4]error
	var wg sync.WaitGroup
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			space, _, err := m.CreateTaskSpace(ctx, w, owner, manager.NewID(), manager.TaskSpaceCreate{BusinessKey: &key, AgentID: agent})
			ids[i], errs[i] = space.ID, err
		}(i)
	}
	wg.Wait()
	for i, id := range ids {
		if errs[i] != nil || id != ids[0] {
			t.Fatal(ids, errs)
		}
	}
	first, replay, err := m.CreateTaskSpace(ctx, w, owner, "random-space", manager.TaskSpaceCreate{AgentID: agent})
	if err != nil || replay {
		t.Fatal(first, replay, err)
	}
	again, replay, err := m.CreateTaskSpace(ctx, w, owner, "random-space", manager.TaskSpaceCreate{AgentID: agent})
	if err != nil || !replay || again.ID != first.ID {
		t.Fatal(again, replay, err)
	}
	distinct, _, err := m.CreateTaskSpace(ctx, w, owner, "other-random", manager.TaskSpaceCreate{AgentID: agent})
	if err != nil || distinct.ID == first.ID {
		t.Fatal(distinct, err)
	}
	other := platform.NewID()
	insertWorkspaceAccount(t, db, other, "space-other@example.test")
	if _, err = db.Exec(`INSERT INTO platform_members(workspace_id,user_id,role)VALUES($1,$2,'admin')`, w, other); err != nil {
		t.Fatal(err)
	}
	if _, _, err = m.CreateTaskSpace(ctx, w, other, "collide", manager.TaskSpaceCreate{BusinessKey: &key, AgentID: agent}); err == nil {
		t.Fatal("same key leaked space")
	}
	if _, err = m.TaskSpace(ctx, w, other, first.ID); err == nil {
		t.Fatal("workspace admin saw private space")
	}
	if _, err = m.TaskSpace(ctx, platform.NewID(), owner, first.ID); err == nil {
		t.Fatal("cross workspace read")
	}
	var model string
	if err = db.QueryRow(`SELECT model_profile_id FROM platform_agents WHERE id=$1`, agent).Scan(&model); err != nil {
		t.Fatal(err)
	}
	second, err := (&platform.AgentService{Store: NewAgentStore(db)}).Create(ctx, w, owner, platform.AgentCreate{Name: "Second", ModelProfileID: model})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = m.CreateTaskSpace(ctx, w, owner, "no-implicit-grant", manager.TaskSpaceCreate{BusinessKey: &key, AgentID: second.ID}); err == nil {
		t.Fatal("same key implicitly granted another employee")
	}
	existing, err := m.TaskSpace(ctx, w, owner, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	existing, err = m.GrantTaskSpaceAgent(ctx, w, owner, existing.ID, second.ID, manager.TaskSpaceGrant{Version: existing.Version, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	task := taskSpaceSession(t, m, w, owner, second.ID, existing.ID)
	if task.ConversationID != "" || task.TaskSpaceID == nil || *task.TaskSpaceID != existing.ID {
		t.Fatal(task)
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM manager_conversations`).Scan(&count); err != nil || count != 0 {
		t.Fatal("fake conversation", count, err)
	}
	existing, err = m.GrantTaskSpaceAgent(ctx, w, owner, existing.ID, second.ID, manager.TaskSpaceGrant{Version: existing.Version, Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	var status string
	if err = db.QueryRow(`SELECT status FROM manager_inputs WHERE id=$1`, task.InitialInputID).Scan(&status); err != nil || status != "canceled" {
		t.Fatal(status, err)
	}
	if read, e := m.Store.Session(ctx, w, owner, task.ID); e != nil || read.AcceptsInput || read.InputBlockReason == nil || *read.InputBlockReason != "task_space_access_revoked" {
		t.Fatal("revoked input projection", read, e)
	}
	if _, _, err = m.SubmitInput(ctx, w, owner, task.ID, "after-revoke", "must reject"); err == nil {
		t.Fatal("revoked employee accepted input")
	}
	if _, err = m.ControlSession(ctx, w, owner, task.ID, false); err != nil {
		t.Fatal("owner unable to stop", err)
	}
	_, err = m.GrantTaskSpaceAgent(ctx, w, owner, existing.ID, second.ID, manager.TaskSpaceGrant{Version: existing.Version, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = m.SubmitInput(ctx, w, owner, task.ID, "after-regrant", "must reject old task"); err == nil {
		t.Fatal("old grant epoch revived")
	}
}

func TestTaskSpaceHTTPPaginationTaskCreationAndOwnership(t *testing.T) {
	db, m, mux, w, owner, agent, cookie := managerFixture(t)
	ctx := context.Background()
	base := "/api/v1/workspaces/" + w
	call := func(method, path, key, body string, auth, csrf bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, base+path, strings.NewReader(body))
		if auth {
			r.Header.Set("Cookie", cookie)
		}
		if csrf {
			r.Header.Set("X-CSRF-Protection", "1")
			r.Header.Set("Origin", "http://localhost")
		}
		r.Header.Set("Idempotency-Key", key)
		out := httptest.NewRecorder()
		mux.ServeHTTP(out, r)
		return out
	}
	body := `{"agent_id":"` + agent + `"}`
	if r := call("POST", "/task-spaces", "first", body, false, true); r.Code != 401 {
		t.Fatal(r.Code, r.Body.String())
	}
	if r := call("POST", "/task-spaces", "first", body, true, false); r.Code != 403 {
		t.Fatal(r.Code, r.Body.String())
	}
	r := call("POST", "/task-spaces", "first", body, true, true)
	if r.Code != 201 {
		t.Fatal(r.Code, r.Body.String())
	}
	var space manager.TaskSpace
	if err := json.Unmarshal(r.Body.Bytes(), &space); err != nil {
		t.Fatal(err)
	}
	if r = call("POST", "/task-spaces", "first", body, true, true); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	if _, _, err := m.CreateTaskSpace(ctx, w, owner, "second", manager.TaskSpaceCreate{AgentID: agent}); err != nil {
		t.Fatal(err)
	}
	if r = call("GET", "/task-spaces?limit=1", "", "", true, false); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	var page manager.Page[manager.TaskSpace]
	if err := json.Unmarshal(r.Body.Bytes(), &page); err != nil || len(page.Items) != 1 || page.NextCursor == nil {
		t.Fatal(page, err)
	}
	if r = call("GET", "/task-spaces?limit=1&cursor="+url.QueryEscape(*page.NextCursor), "", "", true, false); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	taskBody := `{"task_space_id":"` + space.ID + `","agent_id":"` + agent + `","mode":"task","input":{"text":"do work"}}`
	r = call("POST", "/sessions", "task", taskBody, true, true)
	if r.Code != 201 {
		t.Fatal(r.Code, r.Body.String())
	}
	var task manager.Session
	if err := json.Unmarshal(r.Body.Bytes(), &task); err != nil {
		t.Fatal(err)
	}
	r = call("POST", "/sessions", "task", taskBody, true, true)
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	var again manager.Session
	if err := json.Unmarshal(r.Body.Bytes(), &again); err != nil || again.ID != task.ID {
		t.Fatal(again, err)
	}
	for _, path := range []string{"/sessions?task_space_id=" + space.ID, "/tasks?task_space_id=" + space.ID} {
		if r = call("GET", path, "", "", true, false); r.Code != 200 || !strings.Contains(r.Body.String(), task.ID) {
			t.Fatal(path, r.Code, r.Body.String())
		}
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM manager_sessions WHERE task_space_id=$1`, space.ID).Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if r = call("POST", "/sessions/"+task.ID+"/inputs", "next", `{"text":"continue","delivery_mode":"queue"}`, true, true); r.Code != 201 {
		t.Fatal(r.Code, r.Body.String())
	}
	if err := db.QueryRow(`SELECT count(*) FROM manager_inputs WHERE session_id=$1`, task.ID).Scan(&n); err != nil || n != 2 {
		t.Fatal(n, err)
	}
}
