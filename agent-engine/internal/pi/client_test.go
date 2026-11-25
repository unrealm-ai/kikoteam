package pi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// This uses the actual unmodified Pi RPC process with a deterministic model
// HTTP fixture. It does not substitute for real provider acceptance.
func TestUpstreamConversationLoadAndCancel(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "node_modules/.bin/pi")
	if _, err = os.Stat(binary); err != nil {
		t.Skip("run npm ci in agent-engine to install pinned upstream processes")
	}
	var mu sync.Mutex
	var counts []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content any    `json:"content"`
			} `json:"messages"`
			Tools []any `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if len(body.Tools) > 0 {
			t.Error("chat unexpectedly exposes tools")
		}
		n := 0
		var system string
		for _, m := range body.Messages {
			if m.Role == "system" {
				system = fmt.Sprint(m.Content)
			}
			if m.Role == "user" {
				n++
			}
		}
		if !strings.HasPrefix(system, "Be concise.") || !strings.Contains(system, inputContextInstruction) || strings.Contains(system, "legacy chat addendum") {
			t.Errorf("wrong Pi system prompt: %q", system)
		}
		mu.Lock()
		counts = append(counts, n)
		mu.Unlock()
		if strings.Contains(fmt.Sprint(body.Messages[len(body.Messages)-1].Content), "fixture-failure") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(400)
			if _, err := fmt.Fprint(w, `{"error":{"message":"synthetic provider failure","type":"invalid_request_error"}}`); err != nil {
				t.Error(err)
			}
			return
		}
		if strings.Contains(fmt.Sprint(body.Messages[len(body.Messages)-1].Content), "slow") {
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, v := range []any{map[string]any{"id": "test", "object": "chat.completion.chunk", "choices": []any{map[string]any{"index": 0, "delta": map[string]string{"role": "assistant", "content": fmt.Sprintf("answer-%d", n)}, "finish_reason": nil}}}, map[string]any{"id": "test", "object": "chat.completion.chunk", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 2, "total_tokens": 12}}} {
			if _, err := fmt.Fprintf(w, "data: %s\n\n", fixtureJSON(t, v)); err != nil {
				t.Error(err)
				return
			}
		}
		if _, err := fmt.Fprint(w, "data: [DONE]\n\n"); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	dir := t.TempDir()
	cfg := Config{Directory: filepath.Join(dir, "native"), RuntimeDirectory: filepath.Join(dir, "work"), PiCommand: filepath.Join(root, "node_modules/.bin/pi"), Launcher: filepath.Join(root, "scripts/pi.sh"), Chat: true, Model: Model{Protocol: "chat_completions", BaseURL: server.URL + "/v1", ID: "fixture-model", Key: "fixture-key"}, Instruction: "Be concise."}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	c, err := Start(ctx, cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	prompt := func(c *Client, text, want string) {
		t.Helper()
		var output strings.Builder
		reason, err := c.Prompt(ctx, text, nil, func(u Update) {
			if u["type"] == "message_update" {
				if v, ok := u["assistantMessageEvent"].(map[string]any); ok && v["type"] == "text_delta" {
					if text, ok := v["delta"].(string); ok {
						output.WriteString(text)
					}
				}
			}
		})
		if err != nil || reason != "end_turn" || !strings.Contains(output.String(), want) {
			t.Fatalf("prompt reason=%s err=%v output=%q", reason, err, output.String())
		}
	}
	prompt(c, "Remember blue.", "answer-1")
	prompt(c, "Remember blue.", "answer-2")
	if _, err := os.Stat(filepath.Join(cfg.Directory, "input-receipts")); !os.IsNotExist(err) {
		t.Fatalf("unexpected input receipt directory: %v", err)
	}
	id := c.SessionID()
	if err = c.Close(); err != nil {
		t.Fatal(err)
	}
	c, err = Start(ctx, cfg, id)
	if err != nil {
		t.Fatal(err)
	}
	defer func(client *Client) { _ = client.Close() }(c)
	prompt(c, "Continue after restart.", "answer-3")
	if _, err = c.Prompt(ctx, "fixture-failure", nil, func(Update) {}); err == nil {
		t.Fatal("upstream model failure was reported as a completed Turn")
	}
	slow, stop := context.WithTimeout(ctx, 750*time.Millisecond)
	defer stop()
	_, err = c.Prompt(slow, "slow", nil, func(Update) {})
	if err == nil {
		t.Fatal("canceled request succeeded")
	}
	c, err = Start(ctx, cfg, id)
	if err != nil {
		t.Fatal(err)
	}
	accepted := make(chan struct{})
	defer func(client *Client) { _ = client.Close() }(c)
	crashed := make(chan error, 1)
	go func() {
		_, e := c.Prompt(ctx, "slow crash", func() { close(accepted) }, func(Update) {})
		crashed <- e
	}()
	select {
	case <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("Pi did not acknowledge prompt before crash")
	}
	if err = syscall.Kill(-c.cmd.Process.Pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	select {
	case crashErr := <-crashed:
		if crashErr == nil {
			t.Fatal("SIGKILL prompt reported success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("crashed prompt did not stop")
	}
	c, err = Start(ctx, cfg, id)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	prompt(c, "Continue after crash.", "answer-")
	mu.Lock()
	defer mu.Unlock()
	if len(counts) < 3 || counts[0] != 1 || counts[1] != 2 || counts[2] != 3 {
		t.Fatalf("history counts=%v", counts)
	}
}

func fixtureJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
