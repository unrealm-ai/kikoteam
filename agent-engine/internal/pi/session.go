package pi

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// ID preserves the platform's native_session_id, including legacy ACP aliases.
// NativeID is Pi's own identity. Only Engine metadata is written here.
type sessionRecord struct {
	ID       string `json:"id"`
	NativeID string `json:"nativeId"`
	File     string `json:"sessionFile"`
}

func prepareSession(directory, id string) (sessionRecord, error) {
	if id != "" {
		return loadSession(directory, id)
	}
	if _, err := os.Stat(filepath.Join(directory, "session.json")); !errors.Is(err, os.ErrNotExist) {
		return sessionRecord{}, errors.New("native session already exists or cannot be inspected")
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return sessionRecord{}, err
	}
	dir := filepath.Join(directory, "sessions")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return sessionRecord{}, err
	}
	path := filepath.Join(dir, hex.EncodeToString(nonce[:])+".jsonl")
	// Pi initializes an explicitly empty file with its own durable header. This
	// preserves identity even if the process exits before its first model call.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return sessionRecord{}, err
	}
	if err = f.Close(); err != nil {
		return sessionRecord{}, err
	}
	return sessionRecord{File: path}, nil
}
func loadSession(directory, id string) (sessionRecord, error) {
	data, err := os.ReadFile(filepath.Join(directory, "session.json"))
	var record sessionRecord
	if err == nil {
		if json.Unmarshal(data, &record) != nil || record.ID != id || record.NativeID == "" {
			return record, errors.New("native session identity mismatch")
		}
	} else if errors.Is(err, os.ErrNotExist) {
		record, err = loadLegacySession(directory, id)
		if err != nil {
			return record, err
		}
	} else {
		return record, err
	}
	path, err := containedHistory(directory, record.File)
	if err != nil {
		return record, err
	}
	nativeID, err := historyIdentity(path)
	if err != nil {
		return record, err
	}
	if record.NativeID != "" && record.NativeID != nativeID {
		return record, errors.New("native history identity mismatch")
	}
	record.File, record.NativeID = path, nativeID
	return record, nil
}
func loadLegacySession(directory, id string) (sessionRecord, error) {
	data, err := os.ReadFile(filepath.Join(directory, "home/.pi/pi-acp/session-map.json"))
	if err != nil {
		return sessionRecord{}, errors.New("native session mapping unavailable")
	}
	var mapping struct {
		Sessions map[string]struct {
			File string `json:"sessionFile"`
		} `json:"sessions"`
	}
	if json.Unmarshal(data, &mapping) != nil || mapping.Sessions[id].File == "" {
		return sessionRecord{}, errors.New("legacy native session mapping missing")
	}
	return sessionRecord{ID: id, File: mapping.Sessions[id].File}, nil
}
func containedHistory(directory, path string) (string, error) {
	root, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return "", err
	}
	actual, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", errors.New("native history unavailable")
	}
	rel, err := filepath.Rel(root, actual)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", errors.New("native history outside session directory")
	}
	return actual, nil
}
func historyIdentity(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }() //nolint:errcheck // Read-only validation; Scanner reports read errors.
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 65536), 16<<20)
	id := ""
	seen := map[string]bool{}
	for scanner.Scan() {
		var entry struct {
			Type, ID string
			ParentID *string `json:"parentId"`
		}
		if json.Unmarshal(scanner.Bytes(), &entry) != nil {
			return "", errors.New("invalid native history")
		}
		if id == "" {
			if entry.Type != "session" || entry.ID == "" {
				return "", errors.New("missing native history header")
			}
			id = entry.ID
			continue
		}
		if entry.Type == "" || entry.Type == "session" || entry.ID == "" || seen[entry.ID] || entry.ParentID != nil && !seen[*entry.ParentID] {
			return "", errors.New("invalid native history ancestry")
		}
		seen[entry.ID] = true
	}
	if scanner.Err() != nil || id == "" {
		return "", errors.New("native history unreadable or empty")
	}
	return id, nil
}
func (c *Client) bindSession(ctx context.Context, directory string, record sessionRecord) error {
	state, err := c.GetState(ctx)
	if err != nil {
		return err
	}
	if state.SessionID == "" || filepath.Clean(state.SessionFile) != filepath.Clean(record.File) || state.IsStreaming || state.PendingMessageCount != 0 {
		return errors.New("pi did not load the requested native session")
	}
	if record.NativeID != "" && state.SessionID != record.NativeID {
		return errors.New("pi replaced the native session")
	}
	if record.ID == "" {
		record.ID = state.SessionID
	}
	record.NativeID = state.SessionID
	if err = saveSession(directory, record); err != nil {
		return err
	}
	c.session = record.ID
	return nil
}
func saveSession(directory string, record sessionRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(directory, ".session-")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }() //nolint:errcheck // Temporary metadata is not authoritative.
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if err = errors.Join(err, f.Close()); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), filepath.Join(directory, "session.json")); err != nil {
		return err
	}
	dir, err := os.Open(directory)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}
