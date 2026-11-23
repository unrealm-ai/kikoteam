package runtime

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/unrealm-ai/kikoteam/agent-engine/internal/integration"
	"github.com/unrealm-ai/kikoteam/agent-engine/internal/repository"
)

func TestWebhookAttachmentMaterialization(t *testing.T) {
	w := Worker{Root: t.TempDir()}
	task := repository.Work{WorkspaceID: NewID(), AgentID: NewID(), TaskSpaceID: NewID(), TaskID: NewID(), Mode: "task"}
	project := filepath.Join(w.Root, "workspaces", task.WorkspaceID, "projects", "task-"+task.TaskID)
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	body := bytes.Repeat([]byte{0, 0xfe, 0xff}, 8192)
	f := integration.WebhookFile{EventID: NewID(), SHA256: fmt.Sprintf("%x", sha256.Sum256(body)), Body: body}
	path := filepath.Join(project, "webhook-"+f.EventID+".body")
	outside := filepath.Join(t.TempDir(), "unrelated")
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := w.materializeWebhookFile(task, f); err != nil {
			t.Fatal(err)
		}
	}
	saved, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(saved, body) {
		t.Fatal("raw bytes differ", err)
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatal("followed pre-existing symlink")
	}
	f.Body = []byte("changed")
	if err := w.materializeWebhookFile(task, f); err == nil {
		t.Fatal("accepted wrong digest")
	}
}
