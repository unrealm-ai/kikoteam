package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTaskDetailLandingDoesNotReflectBearer(t *testing.T) {
	dir := t.TempDir()
	const shell = "<!doctype html><title>KIKO</title>"
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(shell), 0o600); err != nil {
		t.Fatal(err)
	}
	const bearer = "test_url_safe_bearer_value"
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		t.Run(method, func(t *testing.T) {
			w := httptest.NewRecorder()
			webFiles(dir).ServeHTTP(w, httptest.NewRequest(method, "/tasks/"+bearer, nil))
			if w.Code != http.StatusOK || w.Header().Get("Referrer-Policy") != "no-referrer" ||
				!strings.Contains(w.Header().Get("Cache-Control"), "no-store") ||
				!strings.Contains(w.Header().Get("X-Robots-Tag"), "noindex") {
				t.Fatalf("unexpected landing response: %d headers=%v", w.Code, w.Header())
			}
			if strings.Contains(w.Body.String(), bearer) {
				t.Fatal("landing reflected bearer into HTML")
			}
			if method == http.MethodGet && w.Body.String() != shell {
				t.Fatalf("landing did not serve the SPA shell: %q", w.Body.String())
			}
		})
	}
}
