package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWecomGroupRefreshRejectsBrowserServiceSpoofing(t *testing.T) {
	token := strings.Repeat("s", 32)
	mux := http.NewServeMux()
	WecomGroupRefreshInternalRoutes(mux, nil, token)
	for _, mode := range []string{"missing", "wrong", "cookie", "origin"} {
		t.Run(mode, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/api/internal/v1/wecom-group-refreshes/claim", strings.NewReader(`{}`))
			if mode != "missing" {
				r.Header.Set("Authorization", "Bearer "+token)
			}
			switch mode {
			case "wrong":
				r.Header.Set("Authorization", "Bearer wrong")
			case "cookie":
				r.Header.Set("Cookie", "session=x")
			case "origin":
				r.Header.Set("Origin", "https://app.test")
			}
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != 401 {
				t.Fatal(w.Code)
			}
		})
	}
}
