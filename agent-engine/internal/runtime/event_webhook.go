package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"regexp"

	"github.com/unrealm-ai/kikoteam/agent-engine/internal/integration"
	"github.com/unrealm-ai/kikoteam/agent-engine/internal/repository"
)

// Publish a single server-named entry into the already-prepared task project.
// Rename replaces the directory entry, never follows an existing file symlink.
func (w *Worker) materializeWebhookFile(task repository.Work, f integration.WebhookFile) error {
	if task.Mode != "task" || task.TaskSpaceID == "" || task.TaskID == "" || len(f.Body) > 256*1024 || !webhookUUID.MatchString(f.EventID) {
		return errors.New("invalid webhook attachment scope")
	}
	digest := sha256.Sum256(f.Body)
	if hex.EncodeToString(digest[:]) != f.SHA256 {
		return errors.New("webhook attachment digest mismatch")
	}
	base := filepath.Join(w.Root, "state/sandboxes/workspaces", task.WorkspaceID, "employees", task.AgentID, "taskspaces", task.TaskSpaceID, "tasks", task.TaskID)
	taskRoot, err := os.OpenRoot(base)
	if err != nil {
		return err
	}
	defer func() { _ = taskRoot.Close() }() //nolint:errcheck // Directory handle only.
	dir, err := taskRoot.OpenRoot("project")
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }() //nolint:errcheck // Directory handle only.
	tmp := ".webhook-" + NewID() + ".tmp"
	file, err := dir.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Remove(tmp) }() //nolint:errcheck // Rename consumes the entry.
	_, err = file.Write(f.Body)
	if err = errors.Join(err, file.Close()); err != nil {
		return err
	}
	return dir.Rename(tmp, "webhook-"+f.EventID+".body")
}

var webhookUUID = regexp.MustCompile(`^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$`)
