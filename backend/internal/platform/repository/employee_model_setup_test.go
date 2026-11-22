package repository

import (
	"testing"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestEmployeeConversationModelSetupUsesSelectedProfile(t *testing.T) {
	db, models, workspace, actor := modelFixture(t)
	ctx := t.Context()
	selected := saveTestModel(t, models, workspace, actor)
	a, err := (&service.AgentService{Store: NewAgentStore(db)}).Create(ctx, workspace, actor, service.AgentCreate{Name: "Model setup", ModelProfileID: selected.ID})
	if err != nil {
		t.Fatal(err)
	}
	store := &TemplateStore{DB: db}
	check := func(want bool) {
		t.Helper()
		setup, err := store.Setup(ctx, workspace, actor, a.ID)
		if err != nil {
			t.Fatal(err)
		}
		if setup["conversation_model"].(map[string]any)["available"] != want || setup["task_model"].(map[string]any)["available"] != want {
			t.Fatalf("setup availability wanted %v, got %v", want, setup)
		}
	}
	// No workspace default is required when the employee's selected model works.
	check(true)
	reviewExec(t, db, `UPDATE platform_model_profiles SET enabled=false WHERE id=$1`, selected.ID)
	fallback := saveTestModel(t, models, workspace, actor)
	if _, err = models.Change(ctx, workspace, actor, fallback.ID, "set-default", fallback.Version, true); err != nil {
		t.Fatal(err)
	}
	// A healthy default cannot mask an unavailable employee selection.
	check(false)
}
