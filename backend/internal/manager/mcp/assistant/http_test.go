package assistant_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/mcp/assistant"
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/mcp/platform"
	ms "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	ps "github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type engineFixture struct{}

func (engineFixture) HarnessConfig(context.Context, string, string, string, int64, string) (ms.HarnessConfig, string, error) {
	return ms.HarnessConfig{WorkspaceID: "verified-workspace", AgentID: "verified-agent", Mode: "chat"}, "verified-actor", nil
}

func TestAssistantMCPChecksInputAndLiveGrant(t *testing.T) {
	secret := strings.Repeat("x", 32)
	id := ms.DelegationIdentity{SessionID: ms.NewID(), AssignmentID: ms.NewID(), EngineID: ms.NewID(), Generation: 1}
	input := ms.NewID()
	token, err := assistant.Token(secret, input, "platform", id)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	allowed := true
	assistant.Mount(mux, engineFixture{}, func(_ context.Context, w, actor, a, key string, _ *ps.ExecutionCapabilities) (bool, error) {
		if w != "verified-workspace" || actor != "verified-actor" || a != "verified-agent" || key != "platform" {
			t.Fatal("request fields replaced verified runtime identity")
		}
		return allowed, nil
	}, secret, "platform", "chat", platform.MockServer())
	call := func(inputID string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/mcp/assistant/platform", strings.NewReader(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"employee_management_status","arguments":{}}}`))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-Kiko-Assistant-Input", inputID)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	if w := call(input); w.Code != 200 || !strings.Contains(w.Body.String(), "writes_enabled") {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call(ms.NewID()); w.Code != 401 {
		t.Fatal(w.Code)
	}
	allowed = false
	if w := call(input); w.Code != 403 {
		t.Fatal(w.Code)
	}
}
