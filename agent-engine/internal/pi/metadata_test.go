package pi

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Real upstream appendEntry custom metadata stays off the model
// wire and immediately precedes the persisted user entry, including after load.
func assertInvisibleReceiptParent(t *testing.T, root string) {
	t.Helper()
	matches := 0
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() {
			if err := f.Close(); err != nil {
				t.Error(err)
			}
		}()
		scanner := bufio.NewScanner(f)
		var previous struct{ ID, Type, CustomType string }
		for scanner.Scan() {
			var entry struct {
				ID         string `json:"id"`
				ParentID   string `json:"parentId"`
				Type       string `json:"type"`
				CustomType string `json:"customType"`
				Message    struct {
					Role string `json:"role"`
				} `json:"message"`
			}
			if err = json.Unmarshal(scanner.Bytes(), &entry); err != nil {
				return err
			}
			if entry.Type == "message" && entry.Message.Role == "user" && previous.CustomType == "kiko.context-receipt.v2" {
				if previous.Type != "custom" || previous.CustomType != "kiko.context-receipt.v2" || entry.ParentID != previous.ID {
					t.Fatalf("user not attached to invisible receipt: %+v", entry)
				}
				matches++
			}
			previous.ID, previous.Type, previous.CustomType = entry.ID, entry.Type, entry.CustomType
		}
		return scanner.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	if matches == 0 {
		t.Fatal("no native user receipt verified")
	}
}
