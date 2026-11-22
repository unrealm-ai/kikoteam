package app

import (
	"net/http"
	"net/http/httptest"
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
