package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChannelServiceCredentialRejectsBrowserAndOtherServices(t *testing.T) {
	token := strings.Repeat("h", 40)
	mux := http.NewServeMux()
	(&ChannelServer{Token: token}).Routes(mux)
	for _, tt := range []struct{ name, auth, cookie, origin string }{
		{"missing", "", "", ""}, {"engine", "Bearer " + strings.Repeat("e", 40), "", ""}, {"cookie", "Bearer " + token, "kiko_session=browser", ""}, {"origin", "Bearer " + token, "", "https://example.com"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/api/internal/v1/channel-inbox", strings.NewReader("{}"))
			r.Header.Set("Authorization", tt.auth)
			r.Header.Set("Cookie", tt.cookie)
			r.Header.Set("Origin", tt.origin)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != 401 {
				t.Fatal(w.Code)
			}
		})
	}
}
