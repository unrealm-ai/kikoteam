package repository

import (
	"context"
	"encoding/json"
	"errors"
	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	platform "github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestWebDirectUniqueOwnerAndPrivacy(t *testing.T) {
	db, s, mux, w, owner, agent, cookie := managerFixture(t)
	ctx := context.Background()
	ids := make(chan string, 12)
	errs := make(chan error, 12)
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() { v, e := s.ResolveWebConversation(ctx, w, owner, agent); ids <- v.ID; errs <- e })
	}
	wg.Wait()
	close(ids)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	var id string
	for v := range ids {
		if id != "" && id != v {
			t.Fatal("duplicate conversation")
		}
		id = v
	}
	c, e := s.Store.Conversation(ctx, w, owner, id)
	if e != nil || c.WebUserID == nil || *c.WebUserID != owner {
		t.Fatalf("%+v %v", c, e)
	}
	other := manager.NewID()
	insertWorkspaceAccount(t, db, other, "other-web@example.test")
	if _, e = db.ExecContext(ctx, `INSERT INTO platform_members(workspace_id,user_id,role,status) VALUES($1,$2,'member','active')`, w, other); e != nil {
		t.Fatal(e)
	}
	different, e := s.ResolveWebConversation(ctx, w, other, agent)
	if e != nil || different.ID == id {
		t.Fatalf("other identity %v", e)
	}
	assert404 := func(e error) {
		t.Helper()
		var v *manager.Error
		if !errors.As(e, &v) || v.Status != 404 {
			t.Fatalf("wanted 404: %v", e)
		}
	}
	_, e = s.Store.Conversation(ctx, w, other, id)
	assert404(e)
	page, e := s.ListConversations(ctx, w, other, "", "", "", 50)
	if e != nil || len(page.Items) != 1 || page.Items[0].ID != different.ID {
		t.Fatalf("leaked list %+v %v", page, e)
	}
	request := manager.CreateSession{ConversationID: id, Mode: "chat"}
	chat, _, e := s.CreateSession(ctx, w, owner, "", request)
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.Store.Session(ctx, w, other, chat.ID)
	assert404(e)
	_, e = s.ListEvents(ctx, w, other, chat.ID, "", "", "", 10, "backward")
	assert404(e)
	_, _, e = s.StreamEvents(ctx, w, other, chat.ID, "", "", "")
	assert404(e)
	_, e = s.ControlSession(ctx, w, other, chat.ID, false)
	assert404(e)
	_, _, e = s.CreateSession(ctx, w, other, "", manager.CreateSession{ConversationID: id, Mode: "chat"})
	assert404(e)
	// Cookie identity determines owner; callers cannot supply another user.
	r := httptest.NewRequest("POST", "/api/v1/workspaces/"+w+"/conversations/web-direct", strings.NewReader(`{"agent_id":"`+agent+`"}`))
	r.Header.Set("Cookie", cookie)
	r.Header.Set("X-CSRF-Protection", "1")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	var got manager.Conversation
	if e = json.Unmarshal(rec.Body.Bytes(), &got); e != nil || rec.Code != 200 || got.ID != id {
		t.Fatalf("HTTP %d %s %v", rec.Code, rec.Body.String(), e)
	}
	if _, e = db.ExecContext(ctx, `UPDATE platform_members SET status='disabled' WHERE workspace_id=$1 AND user_id=$2`, w, owner); e != nil {
		t.Fatal(e)
	}
	_, e = s.ResolveWebConversation(ctx, w, owner, agent)
	assert404(e)
}

func TestManagerBackwardHistoryAndLiveBoundary(t *testing.T) {
	_, s, _, w, owner, agent, _ := managerFixture(t)
	ctx := context.Background()
	c, e := s.ResolveWebConversation(ctx, w, owner, agent)
	if e != nil {
		t.Fatal(e)
	}
	chat, _, e := s.CreateSession(ctx, w, owner, "", manager.CreateSession{ConversationID: c.ID, Mode: "chat"})
	if e != nil {
		t.Fatal(e)
	}
	appendEvent := func() {
		t.Helper()
		if e := s.Store.WithSession(ctx, manager.Target{Workspace: w, Actor: owner, Session: chat.ID}, func(tx manager.SessionTx) error {
			return tx.Event("agent.message", nil, nil, map[string]any{"text": "history"})
		}); e != nil {
			t.Fatal(e)
		}
	}
	for range 6 {
		appendEvent()
	}
	page, e := s.ListEvents(ctx, w, owner, chat.ID, "", "", "", 2, "backward")
	if e != nil || page.ResumeCursor == nil || page.NextCursor == nil {
		t.Fatalf("%+v %v", page, e)
	}
	resume := *page.ResumeCursor
	empty, emptyErr := s.ListEvents(ctx, w, owner, chat.ID, "", "agent.tool_use", "", 2, "backward")
	if emptyErr != nil || len(empty.Items) != 0 || empty.NextCursor != nil || empty.ResumeCursor == nil {
		t.Fatalf("empty filtered page %+v %v", empty, emptyErr)
	}
	if _, err := s.ListEvents(ctx, w, owner, chat.ID, "", "agent.message", *page.NextCursor, 2, "backward"); err == nil {
		t.Fatal("changed filter reused history cursor")
	}
	if _, err := s.ListEvents(ctx, w, owner, chat.ID, "", "", *page.NextCursor+"x", 2, "backward"); err == nil {
		t.Fatal("tampered history cursor")
	}
	seqOf := func(b json.RawMessage) int64 {
		t.Helper()
		var v struct {
			Seq string `json:"seq"`
		}
		if e := json.Unmarshal(b, &v); e != nil {
			t.Fatal(e)
		}
		n, e := strconv.ParseInt(v.Seq, 10, 64)
		if e != nil {
			t.Fatal(e)
		}
		return n
	}
	high := seqOf(page.Items[len(page.Items)-1])
	seen := map[int64]bool{}
	appendEvent() // Arrives after the frozen historical snapshot.
	for {
		var last int64
		for _, b := range page.Items {
			seq := seqOf(b)
			if seq <= last || seq > high || seen[seq] {
				t.Fatal("invalid historical order or duplicate")
			}
			seen[seq] = true
			last = seq
		}
		if page.ResumeCursor == nil || *page.ResumeCursor != resume {
			t.Fatal("resume moved during old-page traversal")
		}
		if page.NextCursor == nil {
			break
		}
		if _, e = s.ListEvents(ctx, w, owner, chat.ID, "", "", *page.NextCursor, 2); e == nil {
			t.Fatal("backward cursor accepted as forward")
		}
		page, e = s.ListEvents(ctx, w, owner, chat.ID, "", "", *page.NextCursor, 2, "backward")
		if e != nil {
			t.Fatal(e)
		}
	}
	if int64(len(seen)) != high {
		t.Fatalf("missing history %d of %d", len(seen), high)
	}
	live, _, e := s.StreamEvents(ctx, w, owner, chat.ID, "", "", resume)
	if e != nil || len(live) != 1 || seqOf(live[0]) != high+1 {
		t.Fatalf("lost event %s %v", live, e)
	}
	if _, e = s.ListEvents(ctx, w, owner, chat.ID, "", "", resume, 2, "backward"); e == nil {
		t.Fatal("stream cursor accepted as history")
	}
	if _, e = s.ListEvents(ctx, w, owner, chat.ID, "", "", "", 2, "invalid"); e == nil {
		t.Fatal("invalid direction")
	}
}

func TestWebDirectUpgradePreservesLegacyConversations(t *testing.T) {
	db, models, w, owner := modelFixtureThrough(t, "000011_manager_capability_calls.sql")
	ctx := context.Background()
	m := saveTestModel(t, models, w, owner)
	agent := platform.Agent{ID: platform.NewID()}
	// Seed the pre-upgrade shape directly; current Agent projections require migration 17.
	_, e := db.ExecContext(ctx, `INSERT INTO platform_agents(id,workspace_id,name,model_profile_id,version,config_version,admission_epoch) VALUES($1,$2,'Upgrade',$3,1,1,1)`, agent.ID, w, m.ID)
	if e != nil {
		t.Fatal(e)
	}
	legacy := manager.NewID()
	if _, e = db.ExecContext(ctx, `INSERT INTO manager_conversations(id,workspace_id,agent_id,created_by,title,lifecycle,archived_at) VALUES($1,$2,$3,$4,'Legacy archived','archived',now())`, legacy, w, agent.ID, owner); e != nil {
		t.Fatal(e)
	}
	// The fixture starts before the Web migration, then upgrades through the
	// current schema before using current repository projections. Legacy data
	// preservation and Web uniqueness remain the assertions below.
	files, e := filepath.Glob("../../../../event-hub/database/postgres/migrations/*.sql")
	if e != nil {
		t.Fatal(e)
	}
	for _, file := range files {
		if filepath.Base(file) <= "000011_manager_capability_calls.sql" {
			continue
		}
		data, e := os.ReadFile(file)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = db.ExecContext(ctx, strings.Split(string(data), "-- +goose Down")[0]); e != nil {
			t.Fatal(e)
		}
	}
	// New unified policy intentionally does not inherit grants from legacy data.
	// Explicitly configure only the owner, keeping the legacy preservation test
	// independent from the new platform's deny-by-default migration policy.
	usage := &platform.AgentUsageService{Store: &AgentUsageStore{DB: db}}
	policy, e := usage.Get(ctx, w, owner, agent.ID)
	if e != nil {
		t.Fatal(e)
	}
	policy.UsageConfig = platform.DefaultAgentUsage(owner)
	if _, e = usage.Update(ctx, w, owner, agent.ID, policy); e != nil {
		t.Fatal(e)
	}
	s := &manager.Service{Store: &managerrepo.Store{DB: db}}
	old, e := s.Store.Conversation(ctx, w, owner, legacy)
	if e != nil || old.Lifecycle != "archived" || old.WebUserID != nil || old.Title == nil || *old.Title != "Legacy archived" {
		t.Fatalf("legacy changed %+v %v", old, e)
	}
	v, e := s.ResolveWebConversation(ctx, w, owner, agent.ID)
	if e != nil || v.ID == legacy {
		t.Fatalf("legacy silently adopted %+v %v", v, e)
	}
	if _, e = db.ExecContext(ctx, `INSERT INTO manager_conversations(id,workspace_id,agent_id,created_by,web_user_id) VALUES($1,$2,$3,$4,$4)`, manager.NewID(), w, agent.ID, owner); e == nil {
		t.Fatal("business uniqueness not enforced by database")
	}
	if _, e = db.ExecContext(ctx, `UPDATE platform_agents SET enabled=false WHERE id=$1`, agent.ID); e != nil {
		t.Fatal(e)
	}
	again, e := s.ResolveWebConversation(ctx, w, owner, agent.ID)
	if e != nil || again.ID != v.ID {
		t.Fatalf("disabled history inaccessible %v", e)
	}
}

func TestWebDirectNonOwnerDispatchAndRevocation(t *testing.T) {
	db, s, _, w, _, agent, _ := managerFixture(t)
	ctx := context.Background()
	member := manager.NewID()
	insertWorkspaceAccount(t, db, member, "web-dispatch@example.test")
	if _, e := db.ExecContext(ctx, `INSERT INTO platform_members(workspace_id,user_id,role,status) VALUES($1,$2,'member','active')`, w, member); e != nil {
		t.Fatal(e)
	}
	c, e := s.ResolveWebConversation(ctx, w, member, agent)
	if e != nil {
		t.Fatal(e)
	}
	create := func(mode string) manager.Session {
		t.Helper()
		r := manager.CreateSession{ConversationID: c.ID, Mode: mode}
		r.Input = &struct {
			Text string `json:"text"`
		}{Text: "execute"}
		v, _, e := s.CreateSession(ctx, w, member, manager.NewID(), r)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	chat := create("chat")
	task := create("task")
	store := &managerrepo.Store{DB: db}
	engine := manager.Engine{ID: manager.NewID(), HostID: "private-test", Endpoint: "http://127.0.0.1:9999", Capacity: 10}
	if e = store.RegisterEngine(ctx, engine); e != nil {
		t.Fatal(e)
	}
	if e = store.Dispatch(ctx); e != nil {
		t.Fatal(e)
	}
	assignments, e := store.Assignments(ctx, engine.ID)
	if e != nil || len(assignments) != 2 {
		t.Fatalf("private work never assigned %+v %v", assignments, e)
	}
	found := map[string]bool{}
	for _, a := range assignments {
		found[a.SessionID] = true
	}
	if !found[chat.ID] || !found[task.ID] {
		t.Fatal("wrong assignments")
	}
	// A queued task from a revoked private owner is still reconciled by the
	// internal dispatcher, without opening its history to workspace members.
	revoked := create("task")
	if _, e = db.ExecContext(ctx, `UPDATE platform_members SET status='disabled' WHERE workspace_id=$1 AND user_id=$2`, w, member); e != nil {
		t.Fatal(e)
	}
	if e = store.Dispatch(ctx); e != nil {
		t.Fatal(e)
	}
	var status, reason string
	if e = db.QueryRowContext(ctx, `SELECT status,rejection_reason FROM manager_inputs WHERE id=$1`, *revoked.InitialInputID).Scan(&status, &reason); e != nil || status != "rejected" || reason != "access_revoked" {
		t.Fatalf("revoked queued input %s %s %v", status, reason, e)
	}
}

func TestWebDirectManualArchiveKeepsMappingAndHistory(t *testing.T) {
	_, s, _, w, actor, agent, _ := managerFixture(t)
	ctx := context.Background()
	c, err := s.ResolveWebConversation(ctx, w, actor, agent)
	if err != nil {
		t.Fatal(err)
	}
	chat, _, err := s.CreateSession(ctx, w, actor, "", manager.CreateSession{ConversationID: c.ID, Mode: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	// A private chat obeys the same busy archive rules as generic chats.
	if _, _, err = s.SubmitInput(ctx, w, actor, chat.ID, "archive-test", "queued"); err != nil {
		t.Fatal(err)
	}
	_, err = s.ArchiveConversation(ctx, w, actor, c.ID)
	assertArchiveConflict(t, err, "conversation_busy")
	_, err = s.ControlSession(ctx, w, actor, chat.ID, true)
	assertArchiveConflict(t, err, "session_busy")
	if _, err = s.ControlSession(ctx, w, actor, chat.ID, false); err != nil {
		t.Fatal(err)
	}
	for _, parent := range []bool{false, true} {
		if parent {
			_, err = s.ArchiveConversation(ctx, w, actor, c.ID)
		} else {
			_, err = s.ControlSession(ctx, w, actor, chat.ID, true)
		}
		if err != nil {
			t.Fatal(err)
		}
		for n := 0; n < 3; n++ {
			got, e := s.ResolveWebConversation(ctx, w, actor, agent)
			if e != nil || got.ID != c.ID || got.ChatSessionID == nil || *got.ChatSessionID != chat.ID || (got.Lifecycle == "archived") != parent {
				t.Fatalf("archive replaced mapping: %+v %v", got, e)
			}
		}
		events, e := s.ListEvents(ctx, w, actor, chat.ID, "", "", "", 100, "backward")
		if e != nil || len(events.Items) == 0 {
			t.Fatalf("lost history: %+v %v", events, e)
		}
		if _, _, e = s.SubmitInput(ctx, w, actor, chat.ID, "after-archive", "blocked"); e == nil {
			t.Fatal("archived chat accepted input")
		}
		if _, _, e = s.CreateSession(ctx, w, actor, "", manager.CreateSession{ConversationID: c.ID, Mode: "chat"}); e == nil {
			t.Fatal("created replacement chat")
		}
	}
	// Repeating manual archive remains idempotent and does not erase ownership.
	if _, err = s.ArchiveConversation(ctx, w, actor, c.ID); err != nil {
		t.Fatal(err)
	}
}
