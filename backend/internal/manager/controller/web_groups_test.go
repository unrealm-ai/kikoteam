package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func TestWebGroupHTTPAuthenticationAndUntrustedFields(t *testing.T) {
	mux := http.NewServeMux()
	(&Server{Service: &service.Service{}, Authenticate: func(context.Context, string) (string, error) { return "11111111-1111-4111-8111-111111111111", nil }}).Routes(mux)
	path := "/api/v1/workspaces/22222222-2222-4222-8222-222222222222/web-groups/33333333-3333-4333-8333-333333333333/messages"
	for _, tc := range []struct {
		name, method, body string
		cookie, csrf       bool
		status             int
	}{
		{"get_requires_login", "GET", "", false, false, 401},
		{"post_requires_csrf", "POST", "{}", true, false, 403},
		{"post_requires_login", "POST", "{}", false, true, 401},
		{"cannot_claim_system_sender", "POST", `{"text":"x","kind":"system","sender_id":"forged"}`, true, true, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, path, strings.NewReader(tc.body))
			if tc.cookie {
				r.AddCookie(&http.Cookie{Name: "kiko_session", Value: "test"})
			}
			if tc.csrf {
				r.Header.Set("X-CSRF-Protection", "1")
			}
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}
