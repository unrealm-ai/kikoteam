package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEngineCapacity(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("KIKO_BACKEND_URL", "http://localhost")
	t.Setenv("KIKO_ENGINE_TOKEN", strings.Repeat("x", 32))
	for raw, want := range map[string]int{"": 100, "2": 2} {
		t.Setenv("KIKO_ENGINE_CAPACITY", raw)
		cfg, err := Load()
		if err != nil || cfg.Capacity != want {
			t.Fatalf("capacity %q: got %d, want %d, err=%v", raw, cfg.Capacity, want, err)
		}
	}
}

func TestStorageRoot(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("KIKO_BACKEND_URL", "http://localhost")
	t.Setenv("KIKO_ENGINE_TOKEN", strings.Repeat("x", 32))
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	absolute := t.TempDir()
	for raw, want := range map[string]string{"": filepath.Join(home, ".kiko"), "~": home, "~/.kiko/": filepath.Join(home, ".kiko"), absolute: absolute} {
		t.Setenv("KIKO_STORAGE_LOCAL_ROOT", raw)
		cfg, err := Load()
		if err != nil || cfg.Root != want {
			t.Fatalf("root %q: got %q, want %q, err=%v", raw, cfg.Root, want, err)
		}
	}
}
