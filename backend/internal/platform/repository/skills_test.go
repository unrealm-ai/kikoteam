package repository

import (
	"context"
	"encoding/base64"
	"os"
	"path"
	"path/filepath"
	"testing"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/integration/skillasset"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestPostgresSkillImmutableVersionsAndIsolation(t *testing.T) {
	db, _, ws, actor := modelFixture(t)
	ctx := context.Background()
	root := t.TempDir()
	assets, e := skillasset.NewStore(ctx, skillasset.Config{LocalRoot: root})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := assets.Close(); e != nil {
			t.Error(e)
		}
	})
	s := &service.SkillService{Store: NewSkillStore(db), Assets: assets}
	imp, e := s.Import(ctx, ws, actor, service.SkillImportRequest{Source: "manual", Name: "test-skill", Description: "test", Body: "original"})
	if e != nil {
		t.Fatal(e)
	}
	req := service.SkillPublish{ImportID: imp.ID, Name: "Display"}
	v, e := s.Publish(ctx, ws, actor, "", req)
	if e != nil {
		t.Fatal(e)
	}
	retry, e := s.Publish(ctx, ws, actor, "", req)
	if e != nil || retry.ID != v.ID {
		t.Fatalf("retry %+v %v", retry, e)
	}
	imp2, e := s.Import(ctx, ws, actor, service.SkillImportRequest{Source: "manual", Name: "test-skill", Description: "test", Body: "updated"})
	if e != nil {
		t.Fatal(e)
	}
	// Simulate a process exiting mid-write, before publishing the version.
	prefix := path.Join("workspaces", ws, "skills", v.ID, "versions", imp2.ID)
	files := filepath.Join(root, prefix, "files")
	if e = os.MkdirAll(files, 0700); e != nil {
		t.Fatal(e)
	}
	partial := filepath.Join(files, "SKILL.md")
	if e = os.WriteFile(partial, []byte("partial"), 0600); e != nil {
		t.Fatal(e)
	}
	update := service.SkillPublish{ImportID: imp2.ID, Version: v.Version}
	if _, e = s.Publish(ctx, ws, actor, v.ID, update); e == nil {
		t.Fatal("published an incomplete version")
	}
	current, e := s.Get(ctx, ws, actor, v.ID)
	if e != nil || current.CurrentVersionID != v.CurrentVersionID {
		t.Fatalf("failed publication changed the current version: %+v %v", current, e)
	}
	if data, err := os.ReadFile(partial); err != nil || string(data) != "partial" {
		t.Fatalf("failed publication changed the incomplete file: %q %v", data, err)
	}
	if e = os.Remove(partial); e != nil {
		t.Fatal(e)
	}
	// A retry can also find complete files left before the database commit.
	if e = assets.Copy(ctx, imp2.KeyPrefix, prefix, imp2.Manifest); e != nil {
		t.Fatal(e)
	}
	v2, e := s.Publish(ctx, ws, actor, v.ID, update)
	if e != nil {
		t.Fatal(e)
	}
	if v2.CurrentVersion.ID == v.CurrentVersion.ID || v2.CurrentVersion.Sequence != 2 {
		t.Fatalf("version %+v", v2)
	}
	f, e := s.File(ctx, ws, actor, v.ID, v.CurrentVersion.ID, "SKILL.md")
	if e != nil {
		t.Fatal(e)
	}
	b, e := base64.StdEncoding.DecodeString(f.ContentBase64)
	if e != nil || string(b) == "" {
		t.Fatal("missing old file")
	}
	if _, e = db.ExecContext(ctx, `INSERT INTO platform_skill_imports(id,workspace_id,actor_id,technical_name,description,manifest,source,storage_identity,key_prefix,expires_at) SELECT md5(g::text)::uuid,workspace_id,actor_id,technical_name,description,manifest,source,'old-storage',key_prefix,now()-interval '2 hours' FROM platform_skill_imports CROSS JOIN generate_series(1,100) g WHERE id=$1`, imp.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.Cleanup(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Publish(ctx, ws, actor, "", req); e != nil {
		t.Fatalf("retry after cleanup: %v", e)
	}
	if _, e = s.File(ctx, ws, actor, v.ID, v.CurrentVersion.ID, "SKILL.md"); e != nil {
		t.Fatalf("published file removed: %v", e)
	}
	var cleaned int
	if e = db.QueryRow(`SELECT count(*) FROM platform_skill_imports WHERE cleaned`).Scan(&cleaned); e != nil || cleaned != 2 {
		t.Fatalf("cleaned=%d error=%v", cleaned, e)
	}
	if _, e = s.Get(ctx, ws, service.NewID(), v.ID); e == nil {
		t.Fatal("outsider read")
	}
	if _, e = s.File(ctx, ws, actor, v.ID, v.CurrentVersion.ID, "../SKILL.md"); e == nil {
		t.Fatal("traversal read")
	}
	if _, e = s.Update(ctx, ws, actor, v.ID, service.SkillPatch{Version: 1}); e == nil {
		t.Fatal("stale update accepted")
	}
}
