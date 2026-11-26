package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestInviteLandingNoReferrer(t *testing.T) {
	h := noReferrer(http.NotFoundHandler())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/?invitation_token=test-bearer-secret", nil))
	if w.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("missing no-referrer")
	}
}

func TestReferralLandingIsUncachedSPAEntry(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>fixture</html>"), 0600); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	webFiles(dir).ServeHTTP(w, httptest.NewRequest("GET", "/r/16000000-9000-4000-8000-000000000000", nil))
	if w.Code != http.StatusOK || w.Body.String() != "<html>fixture</html>" {
		t.Fatalf("referral landing: %d %q", w.Code, w.Body)
	}
	if w.Header().Get("Referrer-Policy") != "no-referrer" || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Robots-Tag") == "" {
		t.Fatalf("referral landing headers: %v", w.Header())
	}
}
