package inputattachments

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

type testAttachmentReader struct {
	calls int
}

func (r *testAttachmentReader) ReadInputAttachment(context.Context, service.DelegationIdentity, string) (service.InputAttachmentStatus, service.Conversation, error) {
	r.calls++
	return service.InputAttachmentStatus{}, service.Conversation{}, errors.New("private database diagnostic")
}

func TestAttachmentLookupDoesNotSuggestOnAuthOrDependencyError(t *testing.T) {
	const secret = "fixture attachment token secret only"
	mux := http.NewServeMux()
	reader := &testAttachmentReader{}
	Register(mux, reader, &service.Service{}, secret)
	validToken, err := service.SignInputAttachment(secret, service.DelegationIdentity{
		SessionID: service.NewID(), EngineID: service.NewID(), AssignmentID: service.NewID(), Generation: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, token, origin string
		status, calls       int
	}{
		{"unauthenticated", "bogus", "", http.StatusUnauthorized, 0},
		{"browser origin", validToken, "https://fixture.example", http.StatusUnauthorized, 0},
		{"infrastructure error", validToken, "", http.StatusOK, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/mcp/input-attachments", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"attachment_get","arguments":{"attachment_id":"invalid"}}}`))
			r.Header.Set("Authorization", "Bearer "+tc.token)
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Accept", "application/json, text/event-stream")
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			got := w.Body.String()
			if w.Code != tc.status || reader.calls != tc.calls || strings.Contains(got, "candidates") ||
				strings.Contains(got, "private database diagnostic") || (tc.status == http.StatusOK && !strings.Contains(got, "dependency_unavailable")) {
				t.Fatal("unexpected denied lookup payload", w.Code, reader.calls, got)
			}
		})
	}
}
