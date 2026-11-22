package workitems

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func TestWorkMCPRejectsCredentialFromAnotherInput(t *testing.T) {
	secret := strings.Repeat("t", 40)
	id := service.DelegationIdentity{
		SessionID: service.NewID(), EngineID: service.NewID(),
		AssignmentID: service.NewID(), Generation: 1,
	}
	previousInput, currentInput := service.NewID(), service.NewID()
	mux := http.NewServeMux()
	// Only initialize the stateless MCP transport; no storage is used.
	if err := Register(mux, nil, secret); err != nil {
		t.Fatal(err)
	}
	status := func(signedInput, headerInput string) int {
		t.Helper()
		token, err := service.SignWorkTool(secret, "mcp-ordinary", signedInput, id)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/mcp/work-items/ordinary", strings.NewReader(
			`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`,
		))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set(service.WorkInputHeader, headerInput)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, req)
		return res.Code
	}
	if got := status(previousInput, currentInput); got != http.StatusUnauthorized {
		t.Fatalf("previous credential for current input: HTTP %d, want 401", got)
	}
	if got := status(currentInput, currentInput); got != http.StatusOK {
		t.Fatalf("matching current input credential: HTTP %d, want 200", got)
	}
}
