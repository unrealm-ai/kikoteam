package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/unrealm-ai/kikoteam/agent-engine/internal/repository"
	"io"
	"net/http"
	"strconv"
)

type Sandbox struct {
	ID             string `json:"sandbox_id"`
	WorkspaceID    string `json:"workspace_id"`
	AgentID        string `json:"agent_id"`
	TaskSpaceID    string `json:"task_space_id,omitempty"`
	TaskID         string `json:"task_id,omitempty"`
	ConversationID string `json:"conversation_id"`
	EngineID       string `json:"engine_id"`
	Lifecycle      string `json:"lifecycle"`
	State          string `json:"state"`
	ConfigToken    string `json:"config_token"`
	ReclaimAllowed bool   `json:"reclaim_allowed"`
}

func (b *Backend) Sandboxes(ctx context.Context) ([]Sandbox, error) {
	var v struct {
		Items []Sandbox `json:"items"`
	}
	err := b.Call(ctx, "GET", "/engines/"+b.EngineID+"/sandboxes", nil, &v)
	return v.Items, err
}
func (b *Backend) SandboxState(ctx context.Context, id, state string) error {
	return b.Call(ctx, "POST", "/sandbox-instances/"+id+"/state", map[string]string{"engine_id": b.EngineID, "state": state}, nil)
}

type Runner struct {
	URL, Token string
	HTTP       *http.Client
}
type Endpoint struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}
type Activity struct {
	Verified      bool `json:"verified"`
	Active        int  `json:"active"`
	Unknown       int  `json:"unknown"`
	PendingWrites int  `json:"pending_writes"`
}

func (a Activity) Quiet() bool {
	return a.Verified && a.Active == 0 && a.Unknown == 0 && a.PendingWrites == 0
}
func (r *Runner) Call(ctx context.Context, method, path string, in, out any) error {
	var data []byte
	var err error
	if in != nil {
		data, err = json.Marshal(in)
		if err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, r.URL+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.Token)
	req.Header.Set("Content-Type", "application/json")
	res, err := r.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }() //nolint:errcheck // Read failures are handled by bounded decode below.
	if res.StatusCode != 200 {
		return fmt.Errorf("Runner operation HTTP %d", res.StatusCode)
	}
	if out == nil {
		_, err = io.Copy(io.Discard, io.LimitReader(res.Body, 65536))
		return err
	}
	return json.NewDecoder(io.LimitReader(res.Body, 65536)).Decode(out)
}
func (r *Runner) Prepare(ctx context.Context, id string) (Endpoint, error) {
	var v Endpoint
	err := r.Call(ctx, "POST", "/sessions/"+id+"/prepare", nil, &v)
	return v, err
}
func (r *Runner) Open(ctx context.Context, w repository.Work, turn, harness string) error {
	return r.Call(ctx, "POST", "/sessions/"+w.SessionID+"/execution-window", map[string]string{"assignment_id": w.AssignmentID, "generation": strconv.FormatInt(w.Generation, 10), "turn_id": turn, "harness_instance_id": harness}, nil)
}
func (r *Runner) State(ctx context.Context, id string) (Activity, error) {
	var v Activity
	err := r.Call(ctx, "GET", "/sessions/"+id+"/state", nil, &v)
	return v, err
}
func (r *Runner) Stop(ctx context.Context, id string) (Activity, error) {
	var v Activity
	err := r.Call(ctx, "POST", "/sessions/"+id+"/stop", nil, &v)
	return v, err
}

func (r *Runner) Release(ctx context.Context, id string) (Activity, error) {
	var v Activity
	err := r.Call(ctx, "POST", "/sessions/"+id+"/release", nil, &v)
	return v, err
}
