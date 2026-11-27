package repository

import (
	"database/sql"
	"testing"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

// This actor loses all management rights when demoted. The original fixture
// owner is the employee creator and would retain those rights independently.
func setupAdminWithoutCreatorRights(t *testing.T, db *sql.DB, workspace string) string {
	t.Helper()
	admin := service.NewID()
	insertWorkspaceAccount(t, db, admin, "revocable-setup-admin@example.test")
	reviewExec(t, db, `INSERT INTO platform_members(workspace_id,user_id,role,status) VALUES($1,$2,'admin','active')`, workspace, admin)
	return admin
}

func assertNoSetupBinding(t *testing.T, db *sql.DB, workspace, agent, provider string) {
	t.Helper()
	var count int
	err := db.QueryRow(`SELECT count(*) FROM platform_channel_bindings WHERE workspace_id=$1 AND agent_id=$2 AND provider=$3`, workspace, agent, provider).Scan(&count)
	if err != nil || count != 0 {
		t.Fatalf("expected no %s binding after revocation, count=%d err=%v", provider, count, err)
	}
}

// setupAdminSessionWithoutCreatorRights provides the real HTTP identity for
// tests that need to demote the only role authorizing a channel operation.
func setupAdminSessionWithoutCreatorRights(t *testing.T, db *sql.DB, workspace string) (actor, token string) {
	t.Helper()
	actor, token = setupAdminWithoutCreatorRights(t, db, workspace), service.NewID()
	if err := (&AuthStore{DB: db}).CreateSession(t.Context(), service.HashToken(token), actor, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	return actor, token
}
