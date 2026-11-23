package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func TestHumanConfirmationMCPScopes(t *testing.T) {
	mux := http.NewServeMux()
	secret := strings.Repeat("e", 40)
	if err := registerManagerMCP(mux, nil, nil, strings.Repeat("a", 32), secret, nil); err != nil {
		t.Fatal(err)
	}
	input := manager.NewID()
	identity := manager.DelegationIdentity{SessionID: manager.NewID(), EngineID: manager.NewID(), AssignmentID: manager.NewID(), Generation: 1}
	chat, err := manager.SignHumanMCP(secret, "chat", input, identity)
	if err != nil {
		t.Fatal(err)
	}
	task, err := manager.SignHumanMCP(secret, "task", input, identity)
	if err != nil {
		t.Fatal(err)
	}
	local, err := manager.SignHumanRuntime(secret, input, identity)
	if err != nil {
		t.Fatal(err)
	}
	list := func(mode, token, headerInput string, extra map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/mcp/human-confirmations/"+mode, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("MCP-Protocol-Version", "2025-11-25")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Kiko-Human-Input", headerInput)
		for key, value := range extra {
			req.Header.Set(key, value)
		}
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, req)
		return res
	}
	for _, c := range []struct {
		mode, token string
		names       []string
	}{
		{"chat", chat, []string{"get_human_confirmation_receipt", "list_pending_human_confirmations", "submit_human_confirmation"}},
		{"task", task, []string{"list_human_confirmation_scopes"}},
	} {
		res := list(c.mode, c.token, input, nil)
		var reply struct {
			Result struct{ Tools []struct{ Name string } }
		}
		if err := json.Unmarshal(res.Body.Bytes(), &reply); err != nil || res.Code != http.StatusOK {
			t.Fatalf("%s discovery failed: %d %s", c.mode, res.Code, res.Body)
		}
		var names []string
		for _, tool := range reply.Result.Tools {
			names = append(names, tool.Name)
		}
		slices.Sort(names)
		if !slices.Equal(names, c.names) {
			t.Fatalf("%s tools = %v, want %v", c.mode, names, c.names)
		}
	}
	for _, c := range []struct {
		name, token, input string
		headers            map[string]string
	}{
		{"anonymous", "", input, nil},
		{"task credential", task, input, nil},
		{"local credential", local, input, nil},
		{"different input", chat, manager.NewID(), nil},
		{"browser cookie", chat, input, map[string]string{"Cookie": "session=browser"}},
		{"browser origin", chat, input, map[string]string{"Origin": "https://example.invalid"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			res := list("chat", c.token, c.input, c.headers)
			if res.Code != http.StatusUnauthorized || res.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("unexpected response: %d %+v", res.Code, res.Header())
			}
		})
	}
}
