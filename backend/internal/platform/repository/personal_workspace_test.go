package repository

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestRegistrationCreatesOnePersonalWorkspaceAtomically(t *testing.T) {
	db := workspaceTestDB(t)
	store := &AuthStore{DB: db}
	auth := &service.AuthService{Store: store, Secret: []byte("test-only-test-only-test-only-secret")}
	ctx := context.Background()
	a, _, err := auth.RegisterWithPassword(ctx, "personal@example.test", strings.Repeat("名", 100), "long-password", "test")
	if err != nil {
		t.Fatal(err)
	}
	var count, length int
	err = db.QueryRow(`SELECT count(*),max(length(w.name)) FROM platform_workspaces w JOIN platform_members m ON w.id=m.workspace_id WHERE w.personal_owner_id=$1 AND m.user_id=$1 AND m.role='owner' AND m.status='active'`, a.ID).Scan(&count, &length)
	if err != nil || count != 1 || length > 100 {
		t.Fatalf("personal workspace: count=%d length=%d err=%v", count, length, err)
	}
	if _, _, err = auth.RegisterWithPassword(ctx, a.Email, "Again", "other-password", "test"); err == nil {
		t.Fatal("duplicate registration accepted")
	}
	if err = db.QueryRow(`SELECT count(*) FROM platform_workspaces WHERE personal_owner_id=$1`, a.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate workspace", err)
	}
	// Force session insertion failure after account and workspace insertions.
	var existingHash string
	if err = db.QueryRow(`SELECT token_hash FROM platform_sessions WHERE account_id=$1`, a.ID).Scan(&existingHash); err != nil {
		t.Fatal(err)
	}
	failed := service.Account{ID: service.NewID(), Email: "rollback@example.test", Name: "Rollback"}
	workspace := service.Workspace{ID: service.NewID(), Name: "Rollback"}
	if err = store.RegisterPassword(ctx, failed, workspace, existingHash, time.Now().Add(time.Hour)); err == nil {
		t.Fatal("duplicate session unexpectedly inserted")
	}
	for _, table := range []string{"platform_accounts", "platform_workspaces"} {
		id := failed.ID
		if table == "platform_workspaces" {
			id = workspace.ID
		}
		if err = db.QueryRow("SELECT count(*) FROM "+table+" WHERE id=$1", id).Scan(&count); err != nil || count != 0 {
			t.Fatal("partial registration survived", table, err)
		}
	}
}

func TestOTPRegistrationAlsoCreatesPersonalWorkspace(t *testing.T) {
	db := workspaceTestDB(t)
	sink := &codeSink{}
	auth := &service.AuthService{Store: &AuthStore{DB: db}, Mailer: sink, Secret: []byte("test-only-test-only-test-only-secret")}
	ctx := context.Background()
	code, err := auth.RequestCode(ctx, "otp-personal@example.test", "register", "test")
	if err != nil {
		t.Fatal(err)
	}
	a, _, err := auth.Register(ctx, "otp-personal@example.test", "OTP", "", ""+sink.code, code.ID)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM platform_workspaces WHERE personal_owner_id=$1`, a.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("OTP personal workspace missing", err)
	}
}
