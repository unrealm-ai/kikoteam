package skillasset

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

const goodMD = "---\nname: sample-skill\ndescription: A sample\nextra: preserved\n---\nHello\n"

func TestZIPDirectoryAndSafety(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
		fail  bool
	}{
		{"complete", map[string]string{"sample/SKILL.md": goodMD, "sample/scripts/run.py": "print('hello')"}, false},
		{"traversal", map[string]string{"SKILL.md": goodMD, "../escape": "x"}, true},
		{"case collision", map[string]string{"SKILL.md": goodMD, "skill.md": goodMD}, true},
		{"secret", map[string]string{"SKILL.md": goodMD, ".env": "secret"}, true},
		{"multiple roots", map[string]string{"a/SKILL.md": goodMD, "b/file": "x"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var b bytes.Buffer
			z := zip.NewWriter(&b)
			for p, s := range tc.files {
				w, e := z.Create(p)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = w.Write([]byte(s)); e != nil {
					t.Fatal(e)
				}
			}
			if e := z.Close(); e != nil {
				t.Fatal(e)
			}
			files, e := readZIP(b.Bytes())
			if (e != nil) != tc.fail {
				t.Fatalf("files=%v error=%v", files, e)
			}
			if !tc.fail && string(files["SKILL.md"]) != goodMD {
				t.Fatal("markdown altered")
			}
		})
	}
}
func TestMetadataValidation(t *testing.T) {
	for _, md := range []string{"hello", "---\nname: BAD\ndescription: x\n---", "---\nname: a\nname: b\ndescription: x\n---", "---\nname: a\ndescription: &x hello\nother: *x\n---"} {
		if _, _, e := metadata([]byte(md)); e == nil {
			t.Fatalf("accepted %q", md)
		}
	}
}
func TestLocalImmutableStore(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, e := NewStore(ctx, Config{LocalRoot: root})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := s.Close(); e != nil {
			t.Error(e)
		}
	})
	b, e := s.Prepare(ctx, service.SkillImportRequest{Source: "markdown", Markdown: goodMD})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Write(ctx, "tmp/import", b); e != nil {
		t.Fatal(e)
	}
	if e = s.Copy(ctx, "tmp/import", "workspaces/ws/skills/s/versions/v", b.Manifest); e != nil {
		t.Fatal(e)
	}
	if e = s.Write(ctx, "tmp/import", b); e != nil {
		t.Fatalf("reuse identical files: %v", e)
	}
	if e = s.put(ctx, "tmp/import/files/SKILL.md", []byte("changed")); !errors.Is(e, os.ErrExist) {
		t.Fatalf("expected a conflict, got %v", e)
	}
	data, e := s.Read(ctx, "workspaces/ws/skills/s/versions/v", b.Manifest.Files[0])
	if e != nil || string(data) != goodMD {
		t.Fatalf("read=%q %v", data, e)
	}
	if _, e = s.Read(ctx, "tmp/import", b.Manifest.Files[0]); e != nil {
		t.Fatalf("conflict damaged the original: %v", e)
	}
	if e = s.put(ctx, "../escape", []byte("bad")); e == nil {
		t.Fatal("escaped root")
	}
	if e = os.Symlink(t.TempDir(), root+"/outside"); e != nil {
		t.Fatal(e)
	}
	if e = s.put(ctx, "outside/escape", []byte("bad")); e == nil {
		t.Fatal("followed symlink outside root")
	}
}
func TestConfigFailClosed(t *testing.T) {
	_, e := LoadConfig(func(k string) string {
		if k == "KIKO_STORAGE_S3_BUCKET" {
			return "bucket"
		}
		return ""
	})
	if e == nil {
		t.Fatal("partial S3 fell back")
	}
	c, e := LoadConfig(func(k string) string {
		if k == "KIKO_STORAGE_LOCAL_ROOT" {
			return "~"
		}
		return ""
	})
	h, he := os.UserHomeDir()
	if e != nil || he != nil || c.LocalRoot != h {
		t.Fatalf("root=%s err=%v", c.LocalRoot, e)
	}
}

func TestExampleProjectRoot(t *testing.T) {
	t.Setenv("KIKO_STORAGE_LOCAL_ROOT", "~/.kiko/")
	cfg, err := LoadConfig(os.Getenv)
	home, e := os.UserHomeDir()
	if e != nil || err != nil || cfg.LocalRoot != filepath.Join(home, ".kiko") {
		t.Fatalf("example root=%q errors=%v %v", cfg.LocalRoot, err, e)
	}
}

func TestCleanupIncompleteImport(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, e := NewStore(ctx, Config{LocalRoot: root})
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if e := s.Close(); e != nil {
			t.Error(e)
		}
	}()
	b, e := s.Prepare(ctx, service.SkillImportRequest{Source: "markdown", Markdown: goodMD})
	if e != nil {
		t.Fatal(e)
	}
	p := "temp/uploads/skill-imports/ws/id"
	if e = os.MkdirAll(filepath.Join(root, p, "files"), 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(root, p, "files", "SKILL.md"), []byte("partial"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = s.Write(ctx, p, b); !errors.Is(e, os.ErrExist) {
		t.Fatalf("expected an incomplete file to conflict, got %v", e)
	}
	if e = s.Cleanup(ctx, p, b.Manifest); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(root + "/" + p); !os.IsNotExist(e) {
		t.Fatal("receipt directory retained")
	}
	if e = s.Cleanup(ctx, "temp/uploads/skill-imports/ws", b.Manifest); e == nil {
		t.Fatal("accepted workspace-wide cleanup")
	}
}
