package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestMissingConfigDoesNotStart(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("KIKO_AUTH_SECRET", "")
	var output bytes.Buffer
	if code := run(&output); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatalf("decode structured startup diagnostic: %v", err)
	}
	for key, want := range map[string]string{
		"service": "backend", "event": "startup.invalid_config",
		"error_code": "invalid_config", "level": "ERROR", "version": version,
	} {
		if record[key] != want {
			t.Errorf("%s = %v, want %s", key, record[key], want)
		}
	}
}
