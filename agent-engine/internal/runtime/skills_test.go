package runtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"github.com/unrealm-ai/kikoteam/agent-engine/internal/integration"
	"github.com/unrealm-ai/kikoteam/agent-engine/internal/repository"
)

func TestSkillDirectoryIntegrityAndCacheRepair(t *testing.T) {
	files := map[string]string{"SKILL.md": "fixture body", "references/data.txt": "reference bytes"}
	skill := integration.Skill{SkillID: NewID(), VersionID: NewID(), Name: "fixture"}
	skill.Manifest.FormatVersion = 1
	paths := []string{}
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		data := []byte(files[path])
		skill.Manifest.Files = append(skill.Manifest.Files, integration.SkillFile{Path: path, Size: int64(len(data)), SHA256: digest(data)})
	}
	raw, err := json.Marshal(skill.Manifest.Files)
	if err != nil {
		t.Fatal(err)
	}
	skill.Manifest.Digest = digest(raw)
	var reads atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		if r.Header.Get("Authorization") != "Bearer engine" || r.URL.Query().Get("version_id") != skill.VersionID {
			t.Error("asset request lost execution/version identity")
			w.WriteHeader(403)
			return
		}
		if err := json.NewEncoder(w).Encode(map[string]string{"content_base64": base64.StdEncoding.EncodeToString([]byte(files[r.URL.Query().Get("path")]))}); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	worker := &Worker{Backend: &integration.Backend{URL: server.URL, Token: "engine", EngineID: NewID(), HTTP: &http.Client{Timeout: time.Second}}}
	work := repository.Work{Assignment: repository.Assignment{SessionID: NewID(), AssignmentID: NewID(), Generation: 1}}
	native := t.TempDir()
	dir, err := worker.materializeSkill(context.Background(), work, NewID(), native, skill)
	if err != nil {
		t.Fatal(err)
	}
	if reads.Load() != 2 {
		t.Fatal("incomplete directory download")
	}
	if _, err = worker.materializeSkill(context.Background(), work, NewID(), native, skill); err != nil || reads.Load() != 2 {
		t.Fatal("valid fixed cache was not reused", err)
	}
	path := filepath.Join(dir, "references/data.txt")
	if err = os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("corrupted"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = worker.materializeSkill(context.Background(), work, NewID(), native, skill); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "reference bytes" || reads.Load() != 4 {
		t.Fatal("modified cache was trusted", err)
	}
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "sentinel")
	if err = os.WriteFile(sentinel, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(sentinel, path); err != nil {
		t.Fatal(err)
	}
	if _, err = worker.materializeSkill(context.Background(), work, NewID(), native, skill); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(sentinel)
	if err != nil || string(data) != "outside" {
		t.Fatal("cache repair followed a file symlink", err)
	}
	otherNative := t.TempDir()
	if err = os.Symlink(outside, filepath.Join(otherNative, "skills")); err != nil {
		t.Fatal(err)
	}
	if _, err = worker.materializeSkill(context.Background(), work, NewID(), otherNative, skill); err == nil {
		t.Fatal("cache parent escaped its root")
	}
	bad := skill
	bad.Manifest.Digest = "bad"
	if _, err = worker.materializeSkill(context.Background(), work, NewID(), native, bad); err == nil {
		t.Fatal("unverified manifest accepted")
	}
}
