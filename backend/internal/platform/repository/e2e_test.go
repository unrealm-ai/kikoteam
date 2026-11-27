package repository

import (
	"context"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/controller"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fileSink struct{ Dir string }

func (s fileSink) SendCode(_ context.Context, to, _, code string) error {
	return os.WriteFile(filepath.Join(s.Dir, service.HashToken(to)+".code"), []byte(code), 0600)
}

// Explicit opt-in local-only fixture. Not compiled into production binaries.
// Codes are available only in a mode-0700 temporary directory owned by this test.
func TestBrowserHarness(t *testing.T) {
	if os.Getenv("KIKO_RUN_BROWSER_TEST") != "1" {
		t.Skip("manual browser fixture")
	}
	db := workspaceTestDB(t)
	enableMailFixture(t, db, "")
	dir, err := os.MkdirTemp("", "kiko-test-mail-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	}()
	t.Logf("test-only mailbox directory: %s", dir)
	origin := "http://127.0.0.1:5173"
	h := (&controller.Server{Auth: &service.AuthService{Store: &AuthStore{DB: db}, Mailer: fileSink{dir}, Secret: []byte("isolated-test-only-not-production-secret")}, Workspaces: &service.WorkspaceService{EmailInvitationsEnabled: true, MailSecret: []byte("test-only-secret-test-only-secret"), Store: NewWorkspaceStore(db), PublicURL: origin}, Origin: origin}).Handler()
	server := &http.Server{Addr: "127.0.0.1:18080", Handler: h, ReadHeaderTimeout: 5 * time.Second}
	defer func() {
		if err := server.Close(); err != nil {
			t.Error(err)
		}
	}()
	t.Log("local browser API on 127.0.0.1:18080, expires after 20 minutes")
	go func() {
		if e := server.ListenAndServe(); e != nil && e != http.ErrServerClosed {
			t.Error(e)
		}
	}()
	time.Sleep(20 * time.Minute)
}
