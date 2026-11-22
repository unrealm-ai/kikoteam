package repository

import (
	"context"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"sync"
	"testing"
	"time"
)

type codeSink struct {
	mu   sync.Mutex
	code string
}

func (m *codeSink) SendCode(_ context.Context, _, _, code string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.code = code
	return nil
}
func TestPostgresEmailCodeSingleUseAndReset(t *testing.T) {
	db := workspaceTestDB(t)
	sink := &codeSink{}
	store := &AuthStore{DB: db}
	s := &service.AuthService{Store: store, Mailer: sink, Secret: []byte("test-only-secret-test-only-secret")}
	ctx := context.Background()
	c, e := s.RequestCode(ctx, "person@example.test", "register", "local")
	if e != nil {
		t.Fatal(e)
	}
	code := sink.code
	_, _, e = s.Register(ctx, "person@example.test", "Name", "long-password", "not-a-code", c.ID)
	if e == nil {
		t.Fatal("bad code accepted")
	}
	var attempts int
	if e = db.QueryRow(`SELECT attempts FROM platform_email_challenges WHERE id=$1`, c.ID).Scan(&attempts); e != nil || attempts != 1 {
		t.Fatalf("attempt not committed %d %v", attempts, e)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	tokens := make(chan string, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, tok, err := s.Register(ctx, "person@example.test", "Name", "long-password", code, c.ID)
			results <- err
			if err == nil {
				tokens <- tok
			}
		}()
	}
	wg.Wait()
	close(results)
	ok := 0
	for err := range results {
		if err == nil {
			ok++
		}
	}
	if ok != 1 {
		t.Fatalf("successful registrations %d", ok)
	}
	tok := <-tokens
	if _, e = s.Me(ctx, tok); e != nil {
		t.Fatal(e)
	}
	reset, e := s.RequestCode(ctx, "person@example.test", "reset_password", "local")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ResetPassword(ctx, "person@example.test", sink.code, reset.ID, "new-long-password"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Me(ctx, tok); e == nil {
		t.Fatal("reset retained old session")
	}
	if _, _, e = s.Login(ctx, "person@example.test", "password", "new-long-password", "", "", "local"); e != nil {
		t.Fatal(e)
	}
}
func TestPostgresResendInvalidatesOldCodeAndAttemptLimit(t *testing.T) {
	db := workspaceTestDB(t)
	sink := &codeSink{}
	s := &service.AuthService{Store: &AuthStore{DB: db}, Mailer: sink, Secret: []byte("test-only-secret-test-only-secret")}
	ctx := context.Background()
	old, e := s.RequestCode(ctx, "resend@example.test", "register", "other")
	if e != nil {
		t.Fatal(e)
	}
	oldCode := sink.code
	if _, e = s.RequestCode(ctx, "resend@example.test", "register", "other"); e == nil {
		t.Fatal("resend throttle missing")
	}
	if _, e = db.Exec(`UPDATE platform_email_challenges SET created_at=now()-interval '61 seconds' WHERE id=$1`, old.ID); e != nil {
		t.Fatal(e)
	}
	c, e := s.RequestCode(ctx, "resend@example.test", "register", "other")
	if e != nil {
		t.Fatal(e)
	}
	right := sink.code
	if _, _, e = s.Register(ctx, "resend@example.test", "Name", "", oldCode, old.ID); e == nil {
		t.Fatal("old code accepted")
	}
	for range 5 {
		if _, _, err := s.Register(ctx, "resend@example.test", "Name", "", "bad-code", c.ID); err == nil {
			t.Fatal("bad code accepted")
		}
	}
	if _, _, e = s.Register(ctx, "resend@example.test", "Name", "", right, c.ID); e == nil {
		t.Fatal("attempt exhausted code accepted")
	}
}
func TestPostgresDistributedRateLimit(t *testing.T) {
	db := workspaceTestDB(t)
	s := &AuthStore{DB: db}
	ctx := context.Background()
	for range 2 {
		if e := s.Allow(ctx, "test", 2, time.Minute); e != nil {
			t.Fatal(e)
		}
	}
	if e := (&AuthStore{DB: db}).Allow(ctx, "test", 2, time.Minute); e == nil {
		t.Fatal("shared limit missing")
	}
}
func TestPasswordSessionRejectsPreResetHash(t *testing.T) {
	db := workspaceTestDB(t)
	sink := &codeSink{}
	store := &AuthStore{DB: db}
	s := &service.AuthService{Store: store, Mailer: sink, Secret: []byte("test-only-secret-test-only-secret")}
	ctx := context.Background()
	c, e := s.RequestCode(ctx, "reset-race@example.test", "register", "source")
	if e != nil {
		t.Fatal(e)
	}
	a, _, e := s.Register(ctx, "reset-race@example.test", "User", "old-password", sink.code, c.ID)
	if e != nil {
		t.Fatal(e)
	}
	old, e := store.FindAccount(ctx, a.Email)
	if e != nil {
		t.Fatal(e)
	}
	c, e = s.RequestCode(ctx, a.Email, "reset_password", "source")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ResetPassword(ctx, a.Email, sink.code, c.ID, "new-password"); e != nil {
		t.Fatal(e)
	}
	if e = store.PasswordSession(ctx, a.Email, old.PasswordHash, service.HashToken("old"), time.Now().Add(time.Hour)); e == nil {
		t.Fatal("old password signed session after reset")
	}
	fresh, e := store.FindAccount(ctx, a.Email)
	if e != nil {
		t.Fatal(e)
	}
	if e = store.PasswordSession(ctx, a.Email, fresh.PasswordHash, service.HashToken("new"), time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
}

func TestUnverifiedMailboxRecoveryRevokesPreclaimCredentials(t *testing.T) {
	db := workspaceTestDB(t)
	sink := &codeSink{}
	store := &AuthStore{DB: db}
	s := &service.AuthService{Store: store, Mailer: sink, Secret: []byte("test-only-secret-test-only-secret")}
	ctx := context.Background()
	a, oldToken, e := s.RegisterWithPassword(ctx, "preclaim@example.test", "Preclaim", "attacker-password", "test")
	if e != nil {
		t.Fatal(e)
	}
	if a.EmailVerified {
		t.Fatal("unverified trusted")
	}
	oldHash := a.PasswordHash
	c, e := s.RequestCode(ctx, a.Email, "login", "test")
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.Login(ctx, a.Email, "code", "", sink.code, c.ID, "test"); e == nil {
		t.Fatal("OTP login retained preclaim credentials")
	}
	var n int
	if e = db.QueryRow(`SELECT count(*) FROM platform_sessions WHERE account_id=$1`, a.ID).Scan(&n); e != nil || n != 1 {
		t.Fatalf("OTP unexpectedly issued session %d %v", n, e)
	}
	c, e = s.RequestCode(ctx, a.Email, "reset_password", "test")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ResetPassword(ctx, a.Email, sink.code, c.ID, "owner-new-password", "test"); e != nil {
		t.Fatal(e)
	}
	current, e := store.FindAccount(ctx, a.Email)
	if e != nil || !current.EmailVerified {
		t.Fatalf("verification missing %v %+v", e, current)
	}
	if _, e = s.Me(ctx, oldToken); e == nil {
		t.Fatal("old session survived verification")
	}
	if _, _, e = s.Login(ctx, a.Email, "password", "attacker-password", "", "", "test"); e == nil {
		t.Fatal("old password survived verification")
	}
	if e = store.PasswordSession(ctx, a.Email, oldHash, service.HashToken("late-old-session"), time.Now().Add(time.Hour)); e == nil {
		t.Fatal("racing preclaim hash issued session")
	}
	if _, _, e = s.Login(ctx, a.Email, "password", "owner-new-password", "", "", "test"); e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.RegisterWithPassword(ctx, a.Email, "Another", "replacement-password", "other"); e == nil {
		t.Fatal("existing verified account overwritten")
	}
}
