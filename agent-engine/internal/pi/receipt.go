package pi

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// InputReceipt is an Engine-private dispatch intent, not a model assertion.
// The nonce is correlation, not authorization: matching the complete native user
// entry and its current ancestry is required. Never expose the journal via APIs.
type InputReceipt struct {
	Version   int    `json:"version"`
	InputID   string `json:"input_id"`
	SessionID string `json:"session_id"`
	Nonce     string `json:"nonce"`
	Digest    string `json:"digest"`
}

func digest(text string) string { v := sha256.Sum256([]byte(text)); return hex.EncodeToString(v[:]) }
func receiptPath(directory, input string) string {
	return filepath.Join(directory, "input-receipts", digest(input)+".json")
}

// PrepareInputReceipt durably publishes an exclusive intent BEFORE sending RPC.
// An existing intent must not be overwritten or dispatched again after a crash.
func PrepareInputReceipt(directory, session, input, text string) (string, error) {
	if session == "" || input == "" {
		return "", errors.New("missing receipt identity")
	}
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	token := hex.EncodeToString(nonce)
	prompt := text
	v := InputReceipt{Version: 2, InputID: input, SessionID: session, Nonce: token, Digest: digest(prompt)}
	path := receiptPath(directory, input)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".receipt-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(file.Name()) }() //nolint:errcheck // Temporary file is not authoritative.
	data, err := json.Marshal(v)
	if err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	err = errors.Join(err, file.Close())
	if err != nil {
		return "", err
	}
	// Link is atomic and refuses to replace an existing intent.
	if err = os.Link(file.Name(), path); err != nil {
		return "", err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	err = errors.Join(dir.Sync(), dir.Close())
	if err != nil {
		return "", err
	}
	if err = os.Link(path, filepath.Join(filepath.Dir(path), "active.json")); err != nil {
		return "", err
	}
	activeDir, e := os.Open(filepath.Dir(path))
	if e != nil {
		return "", e
	}
	e = errors.Join(activeDir.Sync(), activeDir.Close())
	if e != nil {
		return "", e
	}
	return prompt, nil
}

// NativeInputState only proves committed entries. Missing/torn/unmapped files,
// old sessions without an intent, and off-branch matches remain unknown.
func NativeInputState(directory, session, input string) string {
	data, err := os.ReadFile(receiptPath(directory, input))
	if err != nil {
		return "unknown"
	}
	var receipt InputReceipt
	if json.Unmarshal(data, &receipt) != nil || receipt.Version != 2 || receipt.InputID != input || receipt.SessionID != session || len(receipt.Nonce) != 64 || len(receipt.Digest) != 64 {
		return "unknown"
	}
	record, err := loadSession(directory, session)
	if err != nil {
		return "unknown"
	}
	f, err := os.Open(record.File)
	if err != nil {
		return "unknown"
	}
	defer func() { _ = f.Close() }() //nolint:errcheck // Read-only evidence.
	info, statErr := f.Stat()
	if statErr != nil || info.Size() > 32<<20 {
		return "unknown"
	}
	state := inspectReceipt(f, receipt)
	if state == "committed" {
		// Fence the observed user entry before publishing a database receipt.
		if err = f.Sync(); err != nil {
			return "unknown"
		}
		dir, e := os.Open(filepath.Dir(record.File))
		if e != nil {
			return "unknown"
		}
		e = errors.Join(dir.Sync(), dir.Close())
		if e != nil {
			return "unknown"
		}
	}
	return state
}

type nativeReceiptEntry struct {
	CustomType string       `json:"customType"`
	Data       InputReceipt `json:"data"`
	Type       string       `json:"type"`
	ID         string       `json:"id"`
	ParentID   *string      `json:"parentId"`
	Message    struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

func inspectReceipt(r io.Reader, receipt InputReceipt) string {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 65536), 16<<20)
	entries := map[string]nativeReceiptEntry{}
	leaf := ""
	for scanner.Scan() {
		var e nativeReceiptEntry
		if json.Unmarshal(scanner.Bytes(), &e) != nil {
			return "unknown"
		}
		if e.Type == "session" {
			continue
		}
		if e.ID == "" {
			return "unknown"
		}
		if _, exists := entries[e.ID]; exists {
			return "unknown"
		}
		entries[e.ID] = e
		leaf = e.ID
	}
	if scanner.Err() != nil {
		return "unknown"
	}
	seen := map[string]bool{}
	matched := false
	for leaf != "" {
		e, ok := entries[leaf]
		if !ok || seen[leaf] {
			return "unknown"
		}
		seen[leaf] = true
		if e.Type == "message" && e.Message.Role == "user" {
			text, ok := nativeUserText(e.Message.Content)
			if ok && digest(text) == receipt.Digest && e.ParentID != nil {
				parent, exists := entries[*e.ParentID]
				if exists && parent.Type == "custom" && parent.CustomType == "kiko.context-receipt.v2" && parent.Data == receipt {
					matched = true
				}
			}
		}
		leaf = ""
		if e.ParentID != nil {
			leaf = *e.ParentID
		}
	}
	if matched {
		return "committed"
	}
	return "unknown"
}
func nativeUserText(raw json.RawMessage) (string, bool) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text, true
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil || len(blocks) != 1 || blocks[0].Type != "text" {
		return "", false
	}
	return blocks[0].Text, true
}

// ClearActiveReceipt only clears the previous dispatch pointer, never durable evidence.
func ClearActiveReceipt(directory string) error {
	err := os.Remove(filepath.Join(directory, "input-receipts/active.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// ForgetUnconsumedIntent only removes Engine metadata after positive queue
// removal evidence. Native history is never changed.
func ForgetUnconsumedIntent(directory, input string) error {
	if err := ClearActiveReceipt(directory); err != nil {
		return err
	}
	return os.Remove(receiptPath(directory, input))
}
