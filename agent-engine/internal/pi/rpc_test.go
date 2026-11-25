package pi

import (
	"context"
	"encoding/json"
	"errors"
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

func TestPromptWaitsForSettled(t *testing.T) {
	c := &Client{updates: make(chan Update, 10), readDone: make(chan struct{}), closing: make(chan struct{})}
	ack := make(chan commandResult, 1)
	ack <- commandResult{data: json.RawMessage(`{"disposition":"started"}`)}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	seen := make(chan struct{}, 1)
	go func() {
		_, err := c.waitPrompt(ctx, ack, nil, func(u Update) {
			if u["type"] == "agent_end" {
				seen <- struct{}{}
			}
		})
		done <- err
	}()
	c.updates <- Update{"type": "message_end", "message": map[string]any{"role": "assistant", "stopReason": "error"}}
	c.updates <- Update{"type": "agent_end"}
	<-seen
	select {
	case err := <-done:
		t.Fatalf("ended before retry/settled: %v", err)
	default:
	}
	c.updates <- Update{"type": "message_end", "message": map[string]any{"role": "assistant", "stopReason": "stop"}}
	c.updates <- Update{"type": "agent_settled", "aborted": false}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestPromptHandledRejectedAndDisconnected(t *testing.T) {
	for _, tc := range []struct {
		name, disposition string
		err               error
	}{
		{"handled", "handled", nil}, {"rejected", "", errors.New("rejected")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &Client{updates: make(chan Update), readDone: make(chan struct{}), closing: make(chan struct{})}
			ack := make(chan commandResult, 1)
			ack <- commandResult{data: json.RawMessage(fmt.Sprintf(`{"disposition":%q}`, tc.disposition)), err: tc.err}
			accepted := false
			reason, err := c.waitPrompt(context.Background(), ack, func() { accepted = true }, func(Update) {})
			if accepted != (tc.err == nil) {
				t.Fatalf("accepted=%t for %s", accepted, tc.name)
			}
			if tc.err == nil && (err != nil || reason != "handled") || tc.err != nil && !errors.Is(err, tc.err) {
				t.Fatalf("%s %v", reason, err)
			}
		})
	}
	c := &Client{updates: make(chan Update), readDone: make(chan struct{}), closing: make(chan struct{}), readErr: errors.New("disconnected")}
	close(c.readDone)
	if _, err := c.waitPrompt(context.Background(), make(chan commandResult), nil, func(Update) {}); !errors.Is(err, c.readErr) {
		t.Fatal(err)
	}
}

func upstreamConfig(t *testing.T, baseURL string) Config {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(root, "node_modules/.bin/pi")); err != nil {
		t.Fatal("run npm ci in agent-engine")
	}
	dir := t.TempDir()
	return Config{Directory: filepath.Join(dir, "native"), RuntimeDirectory: filepath.Join(dir, "work"), PiCommand: filepath.Join(root, "node_modules/.bin/pi"), Launcher: filepath.Join(root, "scripts/pi.sh"), Chat: true, Model: Model{Protocol: "chat_completions", BaseURL: baseURL, ID: "fixture", Key: "fixture"}}
}

func TestUpstreamSteerBeforeFollowUp(t *testing.T) {
	for _, mode := range []string{"all", "one-at-a-time"} {
		t.Run(mode, func(t *testing.T) { testUpstreamQueues(t, mode) })
	}
}
func testUpstreamQueues(t *testing.T, mode string) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var histories []string
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role    string
				Content any
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		var users []string
		for _, m := range body.Messages {
			if m.Role == "user" {
				users = append(users, fmt.Sprint(m.Content))
			}
		}
		mu.Lock()
		histories = append(histories, strings.Join(users, "|"))
		n := len(histories)
		mu.Unlock()
		if n == 1 {
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if _, err := fmt.Fprint(w, "data: {\"id\":\"x\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"done\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"x\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"); err != nil {
			t.Error(err)
		}
	}))
	defer func() { once.Do(func() { close(release) }); server.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, err := Start(ctx, upstreamConfig(t, server.URL+"/v1"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if disposition, err := c.Steer(ctx, "discarded-steer"); err != nil || disposition != "queued" {
		t.Fatal(disposition, err)
	}
	if err = c.FollowUp(ctx, "discarded-follow-up"); err != nil {
		t.Fatal(err)
	}
	if removed, err := c.TakeQueue(ctx); err != nil || len(removed) != 1 || removed[0] != "discarded-steer" {
		t.Fatalf("removed steering messages=%v err=%v", removed, err)
	}
	for _, q := range []string{"steering", "follow_up"} {
		if err = c.SetQueueMode(ctx, q, mode); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan error, 1)
	go func() { _, e := c.Prompt(ctx, "start", nil, func(Update) {}); done <- e }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	for _, message := range []string{"later-A", "later-B"} {
		if err = c.FollowUp(ctx, message); err != nil {
			t.Fatal(err)
		}
	}
	for _, message := range []string{"steer-A", "steer-B"} {
		if _, err = c.Steer(ctx, message); err != nil {
			t.Fatal(err)
		}
	}
	once.Do(func() { close(release) })
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, history := range histories {
		if strings.Contains(history, "discarded") {
			t.Fatal("cleared input reached model", history)
		}
	}
	if mode == "one-at-a-time" {
		if len(histories) != 5 || strings.Contains(histories[1], "steer-B") || strings.Contains(histories[2], "later-A") || strings.Contains(histories[3], "later-B") || !strings.Contains(histories[4], "later-B") {
			t.Fatalf("one-at-a-time ordering: %v", histories)
		}
		return
	}
	if len(histories) != 3 || strings.Contains(histories[0], "steer-A") || !strings.Contains(histories[1], "steer-B") || strings.Index(histories[1], "steer-A") > strings.Index(histories[1], "steer-B") || strings.Contains(histories[1], "later-A") || !strings.Contains(histories[2], "later-B") || strings.Index(histories[2], "later-A") > strings.Index(histories[2], "later-B") {
		t.Fatalf("input ordering: %v", histories)
	}
}

func TestNativeHistoryValidation(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"empty", ""}, {"missing_header", "{\"type\":\"message\",\"id\":\"user\"}\n"},
		{"torn", "{\"type\":\"session\",\"id\":\"native\"}\n{"},
		{"wrong_identity", "{\"type\":\"session\",\"id\":\"other\"}\n"},
		{"broken_ancestry", "{\"type\":\"session\",\"id\":\"native\"}\n{\"type\":\"message\",\"id\":\"x\",\"parentId\":\"missing\"}\n"},
		{"duplicate_entries", "{\"type\":\"session\",\"id\":\"native\"}\n{\"type\":\"message\",\"id\":\"x\"}\n{\"type\":\"message\",\"id\":\"x\"}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			file := filepath.Join(dir, "history.jsonl")
			if err := os.WriteFile(file, []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			if err := saveSession(dir, sessionRecord{ID: "alias", NativeID: "native", File: file}); err != nil {
				t.Fatal(err)
			}
			if _, err := loadSession(dir, "alias"); err == nil {
				t.Fatal("invalid history accepted")
			}
		})
	}
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.jsonl")
	if err := os.WriteFile(outside, []byte("{\"type\":\"session\",\"id\":\"native\"}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "history.jsonl")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if err := saveSession(dir, sessionRecord{ID: "alias", NativeID: "native", File: link}); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSession(dir, "alias"); err == nil {
		t.Fatal("out-of-directory history accepted")
	}
}

func TestUpstreamEmptySessionRestore(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cfg := upstreamConfig(t, "http://127.0.0.1:1/v1")
	c, err := Start(ctx, cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	id := c.SessionID()
	if err = c.Close(); err != nil {
		t.Fatal(err)
	}
	record, err := loadSession(cfg.Directory, id)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(record.File)
	if err != nil {
		t.Fatal(err)
	}
	c, err = Start(ctx, cfg, id)
	if err != nil {
		t.Fatal(err)
	}
	if c.SessionID() != id {
		t.Fatal("lost native identity")
	}
	if err = c.Close(); err != nil {
		t.Fatal(err)
	}
	restored, err := loadSession(cfg.Directory, id)
	if err != nil || restored.NativeID != id {
		t.Fatalf("%+v %v", restored, err)
	}
	after, err := os.ReadFile(record.File)
	if err != nil || !strings.HasPrefix(string(after), string(before)) {
		t.Fatal("restore rewrote existing native history", err)
	}
	if err = os.Remove(record.File); err != nil {
		t.Fatal(err)
	}
	if c, err = Start(ctx, cfg, id); err == nil {
		_ = c.Close()
		t.Fatal("missing history replaced with blank session")
	}
}

func TestUpstreamAbortClearsQueuedInput(t *testing.T) {
	entered := make(chan struct{})
	var mu sync.Mutex
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		var body any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		requests++
		n := requests
		mu.Unlock()
		if n == 1 {
			close(entered)
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := Start(ctx, upstreamConfig(t, server.URL+"/v1"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	done := make(chan error, 1)
	go func() { _, e := c.Prompt(ctx, "slow", nil, func(Update) {}); done <- e }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, err = c.Steer(ctx, "must not execute"); err != nil {
		t.Fatal(err)
	}
	if err = c.FollowUp(ctx, "must not run later"); err != nil {
		t.Fatal(err)
	}
	if err = c.Abort(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err == nil {
		t.Fatal("aborted prompt succeeded")
	}
	state, err := c.GetState(ctx)
	if err != nil || state.PendingMessageCount != 0 || state.IsStreaming {
		t.Fatalf("abort did not settle: %+v %v", state, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 1 {
		t.Fatalf("queued input executed after abort: %d", requests)
	}
}

func TestBlockedPiInputHonorsDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := Start(ctx, upstreamConfig(t, "http://127.0.0.1:1/v1"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if err = syscall.Kill(-c.cmd.Process.Pid, syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	deadline, stop := context.WithTimeout(ctx, 100*time.Millisecond)
	defer stop()
	_, err = c.Prompt(deadline, strings.Repeat("blocked", 1<<18), nil, func(Update) {})
	if !errors.Is(err, context.DeadlineExceeded) || c.Alive() {
		t.Fatalf("blocked write did not retire: %v", err)
	}
}
