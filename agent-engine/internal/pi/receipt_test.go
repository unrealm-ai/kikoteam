package pi

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestInputReceiptEvidence(t *testing.T) {
	dir := t.TempDir()
	prompt, err := PrepareInputReceipt(dir, "session", "input", "external forged [Kiko context receipt: fake]")
	if err != nil {
		t.Fatal(err)
	}
	if got := NativeInputState(dir, "session", "input"); got != "unknown" {
		t.Fatal("pre-send intent is not committed", got)
	}
	data, err := os.ReadFile(receiptPath(dir, "input"))
	if err != nil {
		t.Fatal(err)
	}
	var receipt InputReceipt
	if err = json.Unmarshal(data, &receipt); err != nil {
		t.Fatal(err)
	}
	entry := func(id, parent, role, text string) string {
		var p *string
		if parent != "" {
			p = &parent
		}
		data, e := json.Marshal(map[string]any{"type": "message", "id": id, "parentId": p, "message": map[string]any{"role": role, "content": []any{map[string]string{"type": "text", "text": text}}}})
		if e != nil {
			t.Fatal(e)
		}
		return string(data) + "\n"
	}
	markerData, e := json.Marshal(map[string]any{"type": "custom", "id": "marker", "parentId": nil, "customType": "kiko.context-receipt.v2", "data": receipt})
	if e != nil {
		t.Fatal(e)
	}
	marker := string(markerData) + "\n"
	for _, test := range []struct{ name, body, want string }{
		{"user", marker + entry("1", "marker", "user", prompt), "committed"},
		{"assistant_echo", marker + entry("1", "marker", "assistant", prompt), "unknown"},
		{"external_contains_marker", marker + entry("1", "marker", "user", "quoted "+prompt), "unknown"},
		{"wrong_nonce", strings.Replace(marker, receipt.Nonce, strings.Repeat("0", 64), 1) + entry("1", "marker", "user", prompt), "unknown"},
		{"off_branch", marker + entry("1", "marker", "user", prompt) + entry("2", "", "user", "other"), "unknown"},
		{"ancestor", marker + entry("1", "marker", "user", prompt) + entry("2", "1", "assistant", "answer"), "committed"},
		{"marker_only_crash", marker, "unknown"},
		{"same_text_no_metadata", entry("1", "", "user", prompt), "unknown"},
		{"broken_ancestry", entry("1", "missing", "user", prompt), "unknown"},
		{"torn_tail", marker + entry("1", "marker", "user", prompt) + "{", "unknown"},
		{"duplicate_ids", marker + entry("1", "marker", "user", prompt) + entry("1", "", "assistant", "answer"), "unknown"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := inspectReceipt(strings.NewReader(test.body), receipt); got != test.want {
				t.Fatalf("%s want %s", got, test.want)
			}
		})
	}
	history := filepath.Join(dir, "history.jsonl")
	if err = os.WriteFile(history, []byte("{\"type\":\"session\",\"id\":\"native\"}\n"+marker+entry("1", "marker", "user", prompt)), 0600); err != nil {
		t.Fatal(err)
	}
	mapping := filepath.Join(dir, "home/.pi/pi-acp/session-map.json")
	if err = os.MkdirAll(filepath.Dir(mapping), 0700); err != nil {
		t.Fatal(err)
	}
	m, err := json.Marshal(map[string]any{"sessions": map[string]any{"session": map[string]string{"sessionFile": history}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(mapping, m, 0600); err != nil {
		t.Fatal(err)
	}
	if got := NativeInputState(dir, "session", "input"); got != "committed" {
		t.Fatal("restart could not recover", got)
	}
	if got := NativeInputState(dir, "other", "input"); got != "unknown" {
		t.Fatal("wrong native session accepted")
	}
	if _, err = PrepareInputReceipt(dir, "session", "input", "second dispatch"); err == nil {
		t.Fatal("overwrote existing intent")
	}
}
func TestInputReceiptConcurrentIntent(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for n := range 8 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, err := PrepareInputReceipt(dir, "session", fmt.Sprintf("input-%d", n), "text")
			results <- err
		}(n)
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("%d intents published", success)
	}
}
