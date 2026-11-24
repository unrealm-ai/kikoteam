package pi

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func uuid() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func TestUpstreamBuiltinHTTP(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	builtin := filepath.Join(root, "../sandbox-runner/builtin-mcp/dist/http-main.js")
	if _, err = os.Stat(builtin); err != nil {
		t.Skip("build sandbox-runner/builtin-mcp first")
	}
	dir, err := os.MkdirTemp("/tmp", "pi-mcp-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if e := os.RemoveAll(dir); e != nil {
			t.Error(e)
		}
	}()
	work := filepath.Join(dir, "project")
	if err = os.Mkdir(work, 0700); err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("a", 64)
	state := filepath.Join(dir, "state")
	args := []string{"--cwd", work, "--workspace-id", uuid(), "--session-id", uuid(), "--sandbox-instance-id", uuid(), "--runner-boot-id", uuid(), "--state-dir", state, "--runtime-dir", dir, "--connection-id", uuid(), "--binding-id", "builtin", "--config-revision", "1"}
	config := filepath.Join(dir, "launch.json")
	data := fixtureJSON(t, map[string]any{"host": "127.0.0.1", "port": 0, "allowed_hosts": []string{"127.0.0.1"}, "discovery_token": token, "launch_args": args})
	if err = os.WriteFile(config, data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "--disable-warning=ExperimentalWarning", builtin, "--config", config)
	out, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait() //nolint:errcheck // Exit is observed separately; fixture readiness and prompt assertions report failures.
		close(exited)
	}()
	defer func() {
		select {
		case <-exited:
			return
		default:
		}
		if err := cmd.Process.Signal(os.Interrupt); err != nil {
			t.Logf("fixture signal: %v", err)
		}
		select {
		case <-exited:
		case <-time.After(14 * time.Second):
			_ = cmd.Process.Kill()
			<-exited
		}
	}()
	ready := make(chan int, 1)
	go func() {
		scanner := bufio.NewScanner(io.MultiReader(out, strings.NewReader("")))
		for scanner.Scan() {
			var event struct {
				Event string `json:"event"`
				Port  int    `json:"port"`
			}
			if json.Unmarshal(scanner.Bytes(), &event) == nil && event.Event == "server.started" && event.Port > 0 {
				ready <- event.Port
			}
		}
	}()
	var port int
	select {
	case port = <-ready:
	case <-exited:
		t.Fatal("builtin exited before readiness")
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(dir, "m.sock"))
	}}
	defer transport.CloseIdleConnections()
	management := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	window := fixtureJSON(t, map[string]any{"control_revision": 2, "assignment_id": uuid(), "generation": 1, "harness_instance_id": uuid(), "turn_id": uuid(), "open": true, "access_token": token})
	req, err := http.NewRequestWithContext(ctx, "POST", "http://localhost/execution-window", bytes.NewReader(window))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := management.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response, readErr := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if res.StatusCode != 200 {
		t.Fatalf("window %d %s", res.StatusCode, response)
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role string `json:"role"`
			} `json:"messages"`
			Tools []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
		}
		if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
			t.Error(e)
			w.WriteHeader(400)
			return
		}
		results := 0
		for _, m := range body.Messages {
			if m.Role == "tool" {
				results++
			}
		}
		delta := map[string]any{"role": "assistant", "content": "builtin complete"}
		reason := "stop"
		if results < 2 {
			tool := ""
			suffix := "__write"
			arguments := map[string]any{"path": "from-pi.txt", "content": "actual MCP write"}
			if results == 1 {
				suffix = "__bash_start"
				arguments = map[string]any{"command": "printf 'actual MCP bash' > from-bash.txt", "yield_time_ms": 1000}
			}
			for _, v := range body.Tools {
				if strings.HasSuffix(v.Function.Name, suffix) {
					tool = v.Function.Name
				}
			}
			if tool == "" {
				t.Errorf("expected MCP tool %s in %#v", suffix, body.Tools)
				w.WriteHeader(500)
				return
			}
			a := fixtureJSON(t, arguments)
			delta = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": "call-" + hex.EncodeToString([]byte{byte(results)}), "type": "function", "function": map[string]any{"name": tool, "arguments": string(a)}}}}
			reason = "tool_calls"
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, d := range []map[string]any{delta, {}} {
			finish := any(nil)
			if len(d) == 0 {
				finish = reason
			}
			v := fixtureJSON(t, map[string]any{"id": "mcp-test", "object": "chat.completion.chunk", "choices": []any{map[string]any{"index": 0, "delta": d, "finish_reason": finish}}})
			if _, err := fmt.Fprintf(w, "data: %s\n\n", v); err != nil {
				t.Error(err)
				return
			}
		}
		if _, err := fmt.Fprint(w, "data: [DONE]\n\n"); err != nil {
			t.Error(err)
		}
	}))
	defer provider.Close()
	cfg := Config{Directory: filepath.Join(dir, "native"), RuntimeDirectory: filepath.Join(dir, "pi-work"), PiCommand: filepath.Join(root, "node_modules/.bin/pi"), Launcher: filepath.Join(root, "scripts/pi.sh"), Model: Model{Protocol: "chat_completions", BaseURL: provider.URL + "/v1", ID: "fixture-tools", Key: "fixture"}, MCP: map[string]any{"builtin": map[string]any{"url": fmt.Sprintf("http://127.0.0.1:%d/mcp", port), "headers": map[string]string{"Authorization": "Bearer " + token}, "exposure": "direct"}}}
	c, err := Start(ctx, cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	reason, err := c.Prompt(ctx, "Write the two files using the tools.", func(Update) {})
	if err != nil || reason != "end_turn" {
		t.Fatalf("prompt %s %v", reason, err)
	}
	for name, want := range map[string]string{"from-pi.txt": "actual MCP write", "from-bash.txt": "actual MCP bash"} {
		got, e := os.ReadFile(filepath.Join(work, name))
		if e != nil || string(got) != want {
			t.Fatalf("%s content=%q error=%v", name, got, e)
		}
	}
}
