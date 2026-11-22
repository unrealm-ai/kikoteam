package repository

import (
	"context"
	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"testing"
)

// This fresh-employee regression uses a disposable PostgreSQL fixture and never
// changes the operator's existing employee policy or calls a model/provider.
func TestNewUnifiedEmployeeWorkspaceMemberCanSubmitPrivateInput(t *testing.T) {
	db, models, w, owner := modelFixture(t)
	ctx := context.Background()
	model := saveTestModel(t, models, w, owner)
	if _, err := models.Change(ctx, w, owner, model.ID, "set-default", model.Version, true); err != nil {
		t.Fatal(err)
	}
	a, err := (&service.AgentService{Store: NewAgentStore(db)}).Create(ctx, w, owner, service.AgentCreate{Name: "isolated upgrade acceptance", ModelProfileID: model.ID})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := (&service.AgentUsageService{Store: &AgentUsageStore{DB: db}}).Get(ctx, w, owner, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	access := policy.UsageConfig.Access
	if access.Mode != "all" || !access.GroupMembersEnabled || len(access.Allow) != 0 || len(access.Deny) != 0 {
		t.Fatal("new employee should default to all with an empty blacklist", policy)
	}
	member := service.NewID()
	insertWorkspaceAccount(t, db, member, "new-employee-member@example.test")
	if _, err := db.Exec(`INSERT INTO platform_members(workspace_id,user_id,role) VALUES($1,$2,'member')`, w, member); err != nil {
		t.Fatal(err)
	}
	s := &manager.Service{Store: &managerrepo.Store{DB: db}, CursorSecret: []byte("isolated-review-cursor-secret-value")}
	c, err := s.ResolveWebConversation(ctx, w, member, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	chat, _, err := s.CreateSession(ctx, w, member, "", manager.CreateSession{ConversationID: c.ID, Mode: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	input, duplicate, err := s.SubmitInput(ctx, w, member, chat.ID, "review-new-private", "isolated acceptance only; do not execute tools")
	if err != nil || duplicate || input.ID == "" {
		t.Fatal(input, duplicate, err)
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM manager_inputs WHERE id=$1 AND status='queued'`, input.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
}
