package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Moving the adapter into Manager must preserve the existing route and reject
// browser or anonymous callers before consulting the database.
func TestDelegationRouteRejectsNonRuntimeIdentity(t *testing.T) {
	mux := http.NewServeMux()
	if err := registerManagerMCP(mux, nil, "", strings.Repeat("a", 32), strings.Repeat("e", 32), nil); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		headers map[string]string
	}{
		{"anonymous", nil},
		{"cookie", map[string]string{"Authorization": "Bearer invalid", "Cookie": "session=browser"}},
		{"origin", map[string]string{"Authorization": "Bearer invalid", "Origin": "https://example.com"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/mcp/task-delegation", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			res := httptest.NewRecorder()
			mux.ServeHTTP(res, req)
			if res.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", res.Code)
			}
			if res.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("missing no-store")
			}
		})
	}
}
