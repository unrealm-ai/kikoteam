// Package integration provides bounded clients for the Backend control plane.
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/unrealm-ai/kikoteam/agent-engine/internal/pi"
	"github.com/unrealm-ai/kikoteam/agent-engine/internal/repository"
)

type Backend struct {
	URL, Token, EngineID string
	HTTP                 *http.Client
}
type HarnessConfig struct {
	ReportOnly            bool         `json:"report_only,omitempty"`
	WorkRole              string       `json:"work_role"`
	WorkLocalToken        string       `json:"work_local_token"`
	WorkMCPToken          string       `json:"work_mcp_token"`
	HumanToolsToken       string       `json:"human_tools_token"`
	HumanMCPToken         string       `json:"human_mcp_token"`
	GroupHistoryToken     string       `json:"group_history_token"`
	TaskSpaceHistoryToken string       `json:"task_space_history_token"`
	CapabilityToken       string       `json:"capability_token"`
	Resources             Resources    `json:"resources"`
	DelegationToken       string       `json:"delegation_token"`
	SessionID             string       `json:"session_id"`
	WorkspaceID           string       `json:"workspace_id"`
	AgentID               string       `json:"agent_id"`
	TaskSpaceID           string       `json:"task_space_id,omitempty"`
	TaskID                string       `json:"task_id,omitempty"`
	ConversationID        string       `json:"conversation_id"`
	Mode                  string       `json:"mode"`
	APIKey                string       `json:"api_key"`
	WebhookFile           *WebhookFile `json:"webhook_file,omitempty"`
	Configuration         struct {
		Vision      bool   `json:"supports_images"`
		Protocol    string `json:"protocol"`
		BaseURL     string `json:"base_url"`
		ModelID     string `json:"model_id"`
		Instruction string `json:"instruction"`
	} `json:"configuration"`
}
type WebhookFile struct {
	EventID string `json:"event_id"`
	SHA256  string `json:"sha256"`
	Body    []byte `json:"body"`
}

func (c HarnessConfig) Model() pi.Model {
	return pi.Model{Vision: c.Configuration.Vision, Protocol: c.Configuration.Protocol, BaseURL: c.Configuration.BaseURL, ID: c.Configuration.ModelID, Key: c.APIKey}
}

func (b *Backend) Call(ctx context.Context, method, path string, body, out any) error {
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, b.URL+"/api/internal/v1"+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+b.Token)
	req.Header.Set("Content-Type", "application/json")
	res, err := b.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }() //nolint:errcheck // Read/drain failures are handled below.
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("backend operation returned HTTP %d", res.StatusCode)
	}
	if out == nil {
		_, err = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
		return err
	}
	return json.NewDecoder(io.LimitReader(res.Body, 32<<20)).Decode(out)
}
func (b *Backend) Assignments(ctx context.Context) ([]repository.Assignment, error) {
	var out struct {
		Items []repository.Assignment `json:"items"`
	}
	err := b.Call(ctx, "GET", "/engines/"+b.EngineID+"/assignments", nil, &out)
	return out.Items, err
}
func (b *Backend) Config(ctx context.Context, w repository.Work, input string) (HarnessConfig, error) {
	q := url.Values{"engine_id": {b.EngineID}, "assignment_id": {w.AssignmentID}, "generation": {strconv.FormatInt(w.Generation, 10)}, "input_id": {input}}
	var out HarnessConfig
	err := b.Call(ctx, "GET", "/sessions/"+w.SessionID+"/harness-config?"+q.Encode(), nil, &out)
	return out, err
}
func (b *Backend) Release(ctx context.Context, w repository.Work) error {
	return b.Call(ctx, "POST", "/sessions/"+w.SessionID+"/assignments/"+w.AssignmentID+"/release", map[string]string{"engine_id": b.EngineID, "generation": strconv.FormatInt(w.Generation, 10)}, nil)
}
