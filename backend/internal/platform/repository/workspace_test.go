package repository

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

// Each test uses an isolated schema. No developer or production tables are mutated.
func workspaceTestDB(t *testing.T) *sql.DB {
	return workspaceTestDBThrough(t, "")
}

func workspaceTestDBThrough(t *testing.T, lastMigration string) *sql.DB {
	t.Helper()
	dsn := os.Getenv("BACKEND_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("BACKEND_TEST_DATABASE_URL required for real PostgreSQL test")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { _ = admin.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	schema := "workspace_test_" + strings.ReplaceAll(service.NewID(), "-", "")
	if _, err = admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, err := admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		if err != nil {
			t.Error(err)
		}
	})
	cfg.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { _ = db.Close() })
	applyTestMigrations(t, ctx, db, "", lastMigration)
	return db
}

func applyTestMigrations(t *testing.T, ctx context.Context, db *sql.DB, after, through string) {
	t.Helper()
	files, err := filepath.Glob("../../../../event-hub/database/postgres/migrations/*.sql")
	if err != nil || len(files) == 0 {
		t.Fatalf("migrations missing: %v", err)
	}
	for _, file := range files {
		if name := filepath.Base(file); name <= after || through != "" && name > through {
			continue
		}
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		up := strings.Split(string(data), "-- +goose Down")[0]
		if _, err = db.ExecContext(ctx, up); err != nil {
			t.Fatalf("migration %s: %v", file, err)
		}
	}
}
func insertWorkspaceAccount(t *testing.T, db *sql.DB, id, email string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO platform_accounts(id,email,name,email_verified)VALUES($1,$2,'Tester',true)`, id, email)
	if err != nil {
		t.Fatal(err)
	}
}
func TestPostgresWorkspaceInvitationConcurrencyAndIsolation(t *testing.T) {
	db := workspaceTestDB(t)
	ctx := context.Background()
	owner := service.NewID()
	newUser := service.NewID()
	outsider := service.NewID()
	insertWorkspaceAccount(t, db, owner, "owner@example.test")
	insertWorkspaceAccount(t, db, newUser, "new@example.test")
	insertWorkspaceAccount(t, db, outsider, "outside@example.test")
	s := &service.WorkspaceService{EmailInvitationsEnabled: true, MailSecret: []byte("test-only-invitation-secret-32-bytes"), Store: NewWorkspaceStore(db)}
	w, err := s.Create(ctx, owner, "Team", "")
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.Create(ctx, outsider, "Other", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(ctx, outsider, w.ID); err == nil {
		t.Fatal("cross-workspace read allowed")
	}
	inv, err := s.CreateInvitation(ctx, owner, w.ID, "new@example.test", "member")
	if err != nil {
		t.Fatal(err)
	}
	members, err := s.Members(ctx, owner, w.ID, 1, 50)
	if err != nil || members.Total != 1 {
		t.Fatalf("pending invitation added member: %+v %v", members, err)
	}
	if err = s.RevokeInvitation(ctx, outsider, other.ID, inv.ID); !errors.Is(err, service.ErrWorkspaceMissing) {
		t.Fatalf("cross-workspace invitation: %v", err)
	}
	token := strings.TrimPrefix(inv.Link, "/?invitation_token=")
	var wg sync.WaitGroup
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.AcceptInvitation(ctx, newUser, token); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	members, err = s.Members(ctx, owner, w.ID, 1, 50)
	if err != nil || members.Total != 2 {
		t.Fatalf("duplicate membership %+v %v", members, err)
	}
	var version int64
	if err = db.QueryRow(`SELECT version FROM platform_members WHERE workspace_id=$1 AND user_id=$2`, w.ID, newUser).Scan(&version); err != nil || version != 1 {
		t.Fatalf("version=%d err=%v", version, err)
	}
	one, err := s.Members(ctx, owner, w.ID, 1, 1)
	if err != nil || len(one.Items) != 1 || one.Total != 2 || one.PageSize != 1 {
		t.Fatalf("pagination %+v %v", one, err)
	}
	two, err := s.Members(ctx, owner, w.ID, 2, 1)
	if err != nil || len(two.Items) != 1 || one.Items[0].ID == two.Items[0].ID {
		t.Fatalf("pagination second %+v %v", two, err)
	}
}

func TestPostgresWorkspaceCASAndOwnerProtection(t *testing.T) {
	db := workspaceTestDB(t)
	ctx := context.Background()
	owner := service.NewID()
	insertWorkspaceAccount(t, db, owner, "owner@example.test")
	s := &service.WorkspaceService{EmailInvitationsEnabled: true, MailSecret: []byte("test-only-invitation-secret-32-bytes"), Store: NewWorkspaceStore(db)}
	w, err := s.Create(ctx, owner, "Original", "")
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, name := range []string{"First", "Second"} {
		wg.Add(1)
		go func(n string) {
			defer wg.Done()
			_, e := s.Update(ctx, owner, w.ID, service.WorkspacePatch{Name: &n, Version: 1})
			results <- e
		}(name)
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
			continue
		}
		var e *service.WorkspaceError
		if errors.As(err, &e) && e.Code == "version_conflict" {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("success=%d conflict=%d", success, conflict)
	}
	if err = s.RemoveMember(ctx, owner, w.ID, owner, 1); err == nil {
		t.Fatal("owner removal allowed")
	}
	latest, err := s.Get(ctx, owner, w.ID)
	if err != nil || latest.Version != 2 {
		t.Fatalf("%+v %v", latest, err)
	}
}

func TestPostgresReusableLinkAndRemovalHistory(t *testing.T) {
	db := workspaceTestDB(t)
	ctx := context.Background()
	owner := service.NewID()
	one := service.NewID()
	two := service.NewID()
	insertWorkspaceAccount(t, db, owner, "link-owner@example.test")
	insertWorkspaceAccount(t, db, one, "link-one@example.test")
	insertWorkspaceAccount(t, db, two, "link-two@example.test")
	if _, err := db.ExecContext(ctx, `UPDATE platform_accounts SET email_verified=false WHERE id IN($1,$2)`, one, two); err != nil {
		t.Fatal(err)
	}
	s := &service.WorkspaceService{EmailInvitationsEnabled: true, MailSecret: []byte("test-only-invitation-secret-32-bytes"), Store: NewWorkspaceStore(db)}
	w, err := s.Create(ctx, owner, "Reusable", "")
	if err != nil {
		t.Fatal(err)
	}
	i, err := s.CreateInvitationWithOptions(ctx, owner, w.ID, "", "member", "link", 0)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimPrefix(i.Link, "/?invitation_token=")
	var wg sync.WaitGroup
	for _, user := range []string{one, two, one, two} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if _, err := s.AcceptInvitation(ctx, id, token); err != nil {
				t.Error(err)
			}
		}(user)
	}
	wg.Wait()
	var total int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM platform_invitation_redemptions WHERE invitation_id=$1`, i.ID).Scan(&total); err != nil || total != 2 {
		t.Fatalf("redemptions=%d err=%v", total, err)
	}
	inv, err := s.InspectInvitation(ctx, token)
	if err != nil || inv.Status != "valid" || inv.Kind != "link" {
		t.Fatalf("link consumed %+v %v", inv, err)
	}
	if err = s.RemoveMember(ctx, owner, w.ID, one, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AcceptInvitation(ctx, one, token); err == nil {
		t.Fatal("removed user rejoined old token")
	}
	// Unused old links are also blocked, not just previously redeemed links.
	old, err := s.CreateInvitationWithOptions(ctx, owner, w.ID, "", "member", "link", 7)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RemoveMember(ctx, owner, w.ID, two, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AcceptInvitation(ctx, two, strings.TrimPrefix(old.Link, "/?invitation_token=")); err == nil {
		t.Fatal("removed user rejoined unused old token")
	}
	next, err := s.CreateInvitationWithOptions(ctx, owner, w.ID, "", "member", "link", 180)
	if err != nil {
		t.Fatal(err)
	}
	nextToken := strings.TrimPrefix(next.Link, "/?invitation_token=")
	if _, err = s.AcceptInvitation(ctx, one, nextToken); err != nil {
		t.Fatal(err)
	}
	disabled := "disabled"
	if _, err = s.UpdateMember(ctx, owner, w.ID, one, service.MemberPatch{Status: &disabled, Version: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AcceptInvitation(ctx, one, nextToken); err == nil {
		t.Fatal("disabled member reactivated")
	}
	if err = s.RevokeInvitation(ctx, owner, w.ID, next.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AcceptInvitation(ctx, two, nextToken); err == nil {
		t.Fatal("revoked link accepted")
	}
	targeted, err := s.CreateInvitation(ctx, owner, w.ID, "link-two@example.test", "member")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AcceptInvitation(ctx, two, strings.TrimPrefix(targeted.Link, "/?invitation_token=")); err == nil {
		t.Fatal("unverified email accepted targeted invitation")
	}
}

func TestTargetedInvitationAcceptUsesOneDatabaseConnection(t *testing.T) {
	db := workspaceTestDB(t)
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	owner, user := service.NewID(), service.NewID()
	insertWorkspaceAccount(t, db, owner, "single-owner@example.test")
	insertWorkspaceAccount(t, db, user, "single-user@example.test")
	s := service.WorkspaceService{EmailInvitationsEnabled: true, MailSecret: []byte("test-only-invitation-secret-32-bytes"), Store: NewWorkspaceStore(db)}
	w, err := s.Create(ctx, owner, "One connection", "")
	if err != nil {
		t.Fatal(err)
	}
	invitation, err := s.CreateInvitation(ctx, owner, w.ID, "single-user@example.test", "member")
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := s.AcceptInvitation(ctx, user, strings.TrimPrefix(invitation.Link, "/?invitation_token="))
	if err != nil || accepted.ID != w.ID {
		t.Fatalf("accept while pool capacity=1: %+v %v", accepted, err)
	}
}

func TestSharedEmailInvitationRecipients(t *testing.T) {
	db := workspaceTestDB(t)
	ctx := context.Background()
	owner := service.NewID()
	insertWorkspaceAccount(t, db, owner, "owner@example.test")
	svc := &service.WorkspaceService{Store: NewWorkspaceStore(db), EmailInvitationsEnabled: true, MailSecret: []byte("shared-invitation-repository-test-secret")}
	ws, err := svc.Create(ctx, owner, "Shared invitations", "")
	if err != nil {
		t.Fatal(err)
	}
	accounts := map[string]string{}
	for _, name := range []string{"alice", "bob", "carol", "outside"} {
		accounts[name] = service.NewID()
		insertWorkspaceAccount(t, db, accounts[name], name+"@example.test")
	}
	invite, err := svc.CreateInvite(ctx, owner, ws.ID, service.InvitationOptions{Kind: "email", Emails: []string{"ALICE@example.test", " bob@example.test ", "carol@example.test", "alice@example.test"}, Role: "member"})
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimPrefix(invite.Link, "/?invitation_token=")
	checkInspection := func(status string) {
		t.Helper()
		inspect, err := svc.InspectInvitation(ctx, token)
		if err != nil || inspect.Status != status || inspect.EmailMasked != "" {
			t.Fatalf("inspect %+v %v", inspect, err)
		}
	}
	checkInspection("valid")
	records, err := svc.Invitations(ctx, owner, ws.ID, 1, 20)
	if err != nil || records.Total != 3 {
		t.Fatalf("recipient rows: %+v, %v", records, err)
	}
	ids := map[string]string{}
	for _, i := range records.Items {
		if !i.Shared || i.Link != "" {
			t.Fatal("invalid shared list record")
		}
		ids[i.Email] = i.ID
	}
	checkMembers := func(total int) {
		t.Helper()
		v, e := svc.Members(ctx, owner, ws.ID, 1, 20)
		if e != nil || v.Total != total || v.Items[0].JoinedAt.IsZero() {
			t.Fatalf("members %+v %v", v, e)
		}
	}
	checkMembers(1)
	sink := &inviteSink{}
	dispatch := &service.DeliveryDispatcher{Store: NewDeliveryStore(db), Workspaces: svc, Secret: svc.MailSecret, Sender: sink}
	received := map[string]bool{}
	for range 3 {
		ok, e := dispatch.DispatchOnce(ctx)
		if !ok || e != nil || sink.link != invite.Link || received[sink.to] {
			t.Fatalf("shared delivery: %v %v", ok, e)
		}
		received[sink.to] = true
	}
	bob := ids["bob@example.test"]
	if _, err = db.Exec("UPDATE platform_invitation_deliveries SET status='unknown' WHERE invitation_id=$1", bob); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.RetryInvitationDelivery(ctx, owner, ws.ID, bob, false); err == nil {
		t.Fatal("unknown delivery needs confirmation")
	}
	if _, err = svc.RetryInvitationDelivery(ctx, owner, ws.ID, bob, true); err != nil {
		t.Fatal(err)
	}
	if ok, e := dispatch.DispatchOnce(ctx); !ok || e != nil || sink.to != "bob@example.test" || sink.link != invite.Link {
		t.Fatal("retry did not target Bob")
	}
	if ok, e := dispatch.DispatchOnce(ctx); ok || e != nil {
		t.Fatal("unexpected extra delivery")
	}
	if _, err = svc.AcceptInvitation(ctx, accounts["outside"], token); err == nil {
		t.Fatal("outsider admitted")
	}
	if _, err = db.Exec("UPDATE platform_accounts SET email_verified=false WHERE id=$1", accounts["bob"]); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.AcceptInvitation(ctx, accounts["bob"], token); err == nil {
		t.Fatal("unverified Bob admitted")
	}
	if _, err = svc.AcceptInvitation(ctx, accounts["alice"], token); err != nil {
		t.Fatal(err)
	}
	checkInspection("valid")
	if _, err = db.Exec("UPDATE platform_accounts SET email_verified=true WHERE id=$1", accounts["bob"]); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.AcceptInvitation(ctx, accounts["bob"], token); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.AcceptInvitation(ctx, accounts["alice"], token); err != nil {
		t.Fatal(err)
	}
	checkMembers(3)
	if err = svc.RevokeInvitation(ctx, owner, ws.ID, ids["carol@example.test"]); err != nil {
		t.Fatal(err)
	}
	checkInspection("revoked")
	if _, err = svc.AcceptInvitation(ctx, accounts["carol"], token); err == nil {
		t.Fatal("revoked recipient admitted")
	}
	checkMembers(3)
}
