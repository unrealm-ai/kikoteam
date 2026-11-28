package repository

import (
	"os"
	"os/exec"
	"testing"
)

// The optional browser submits the single admin probe against the local supplier
// and saves the draft. Neither fixture enters this path for a live supplier.
func browserTestGeneration(t *testing.T, server, token, workspace, kind, base, model string) bool {
	t.Helper()
	script := os.Getenv("KIKO_TEST_SETTINGS_BROWSER")
	if script == "" {
		return false
	}
	cmd := exec.CommandContext(t.Context(), "node", script, server, token, workspace, kind, base, model)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("settings browser: %v %s", err, out)
	}
	t.Log(string(out))
	return true
}
