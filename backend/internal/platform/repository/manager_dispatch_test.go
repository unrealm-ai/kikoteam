package repository

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestManagerUnassignedInputsRejectWithoutEngine(t *testing.T) {
	db, s, _, w, actor, agent, _ := managerFixture(t)
	ctx := context.Background()
	store := &managerrepo.Store{DB: db}
	c, err := s.CreateConversation(ctx, w, actor, manager.CreateConversation{AgentID: agent})
	if err != nil {
		t.Fatal(err)
	}
	req := manager.CreateSession{ConversationID: c.ID, Mode: "task"}
	req.Input = &struct {
		Text string `json:"text"`
	}{Text: "must not execute"}
	session, _, err := s.CreateSession(ctx, w, actor, "first", req)
	if err != nil {
		t.Fatal(err)
	}
	agents := &service.AgentService{Store: NewAgentStore(db)}
	change := func(enabled bool) {
		t.Helper()
		var version int64
		if err := db.QueryRowContext(ctx, `SELECT version FROM platform_agents WHERE id=$1`, agent).Scan(&version); err != nil {
			t.Fatal(err)
		}
		if _, err := agents.Update(ctx, w, actor, agent, service.AgentPatch{Version: version, Enabled: &enabled}); err != nil {
			t.Fatal(err)
		}
	}
	check := func(id string) {
		t.Helper()
		input, err := s.Store.Input(ctx, w, actor, session.ID, id)
		if err != nil || input.Status != "rejected" || input.RejectionReason == nil || *input.RejectionReason != "agent_disabled" {
			t.Fatalf("input not rejected: %+v %v", input, err)
		}
	}
	change(false)
	if err = store.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	check(*session.InitialInputID)
	change(true)
	second, _, err := s.SubmitInput(ctx, w, actor, session.ID, "second", "old epoch")
	if err != nil {
		t.Fatal(err)
	}
	change(false)
	change(true)
	if err = store.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	check(second.ID)
	member := service.NewID()
	insertWorkspaceAccount(t, db, member, "revoked-runtime@example.test")
	if _, err = db.ExecContext(ctx, `INSERT INTO platform_members(workspace_id,user_id,role,status) VALUES($1,$2,'member','active')`, w, member); err != nil {
		t.Fatal(err)
	}
	third, _, err := s.SubmitInput(ctx, w, member, session.ID, "revoked-member", "must be rejected")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE platform_members SET status='disabled' WHERE workspace_id=$1 AND user_id=$2`, w, member); err != nil {
		t.Fatal(err)
	}
	if err = store.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	veto, err := s.Store.Input(ctx, w, actor, session.ID, third.ID)
	if err != nil || veto.RejectionReason == nil || *veto.RejectionReason != "access_revoked" {
		t.Fatal("revoked actor's queued input remained active", err)
	}
	var turns, sandboxes int
	if err = db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM manager_turns),(SELECT count(*) FROM manager_conversation_sandboxes)`).Scan(&turns, &sandboxes); err != nil || turns != 0 || sandboxes != 0 {
		t.Fatalf("rejection created runtime: turns=%d sandboxes=%d err=%v", turns, sandboxes, err)
	}
	v, err := s.Store.Session(ctx, w, actor, session.ID)
	if err != nil || v.Execution.QueuedInputCount != 0 || v.Execution.State != "idle" {
		t.Fatalf("queue not reconciled: %+v %v", v.Execution, err)
	}
}

func TestManagerEngineHeartbeatAndCapacity(t *testing.T) {
	db, s, mux, w, actor, agent, cookie := managerFixture(t)
	ctx := context.Background()
	store := &managerrepo.Store{DB: db}
	if err := store.HeartbeatEngine(ctx, manager.NewID(), 1, true); err == nil {
		t.Fatal("unknown Engine heartbeat accepted")
	}
	engine := manager.Engine{ID: manager.NewID(), HostID: "test-host", Endpoint: "http://127.0.0.1:9000", Capacity: 1}
	if err := store.RegisterEngine(ctx, engine); err != nil {
		t.Fatal(err)
	}
	if err := store.HeartbeatEngine(ctx, engine.ID, 2, false); err != nil {
		t.Fatal(err)
	}
	if err := store.HeartbeatEngine(ctx, engine.ID, 1, true); err != nil {
		t.Fatal(err)
	}
	var accepting bool
	var seq int64
	if err := db.QueryRowContext(ctx, `SELECT accepting,heartbeat_seq FROM manager_engines WHERE id=$1`, engine.ID).Scan(&accepting, &seq); err != nil || accepting || seq != 2 {
		t.Fatalf("stale heartbeat changed state: %v %d %v", accepting, seq, err)
	}
	if err := store.HeartbeatEngine(ctx, engine.ID, 3, true); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		c, err := s.CreateConversation(ctx, w, actor, manager.CreateConversation{AgentID: agent})
		if err != nil {
			t.Fatal(err)
		}
		req := manager.CreateSession{ConversationID: c.ID, Mode: "chat"}
		req.Input = &struct {
			Text string `json:"text"`
		}{Text: "queued"}
		if _, _, err = s.CreateSession(ctx, w, actor, manager.NewID(), req); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	assignments, err := store.Assignments(ctx, engine.ID)
	if err != nil || len(assignments) != 1 {
		t.Fatalf("capacity not enforced: %v %v", assignments, err)
	}
	var session string
	if err = db.QueryRowContext(ctx, `SELECT id FROM manager_sessions LIMIT 1`).Scan(&session); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/sessions?sort_by=created_at", "/sessions/" + session + "/inputs?status=queued%7Cstarted", "/sessions/" + session + "/turns?status=running%7Ccompleted"} {
		r := httptest.NewRequest("GET", "/api/v1/workspaces/"+w+path, nil)
		r.Header.Set("Cookie", cookie)
		out := httptest.NewRecorder()
		mux.ServeHTTP(out, r)
		if out.Code != 400 {
			t.Fatalf("invalid query accepted: %s status %d", path, out.Code)
		}
	}
	var e *manager.Error
	if err = store.HeartbeatEngine(ctx, manager.NewID(), 1, true); !errors.As(err, &e) || e.Code != "resource_not_found" {
		t.Fatal("unknown Engine has wrong error", err)
	}
}
