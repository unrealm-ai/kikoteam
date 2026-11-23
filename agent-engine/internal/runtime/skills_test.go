package runtime

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/unrealm-ai/kikoteam/agent-engine/internal/integration"
	"github.com/unrealm-ai/kikoteam/agent-engine/internal/repository"
)

func TestRejectOverlappingNodeRoots(t *testing.T) {
	root := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KIKO_CONTEXT_ROOT", t.TempDir())
	for _, temp := range []string{root, filepath.Join(alias, "workspaces"), os.Getenv("KIKO_CONTEXT_ROOT")} {
		t.Setenv("KIKO_RUNTIME_TEMP_ROOT", temp)
		if err := ValidateNodeRoots(root); err == nil {
			t.Fatal("accepted overlapping root", temp)
		}
	}
	t.Setenv("KIKO_RUNTIME_TEMP_ROOT", t.TempDir())
	if err := ValidateNodeRoots(root); err != nil {
		t.Fatal(err)
	}
}

func TestSessionSkillLinks(t *testing.T) {
	t.Setenv("KIKO_CONTEXT_ROOT", t.TempDir())
	w := Worker{Root: t.TempDir()}
	work := repository.Work{WorkspaceID: NewID(), Assignment: repository.Assignment{SessionID: NewID()}}
	skills := []integration.Skill{
		{Scope: "official", Name: "first", SkillID: NewID(), VersionID: NewID()},
		{Scope: "workspace", Name: "second", SkillID: NewID(), VersionID: NewID()},
	}
	publish := func(s integration.Skill) string {
		t.Helper()
		base := filepath.Join(w.Root, "official")
		if s.Scope == "workspace" {
			base = filepath.Join(w.Root, "workspaces", work.WorkspaceID)
		}
		files := filepath.Join(base, "skills", s.SkillID, "versions", s.VersionID, "files")
		if err := os.MkdirAll(files, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(files, "SKILL.md"), []byte(s.VersionID), 0600); err != nil {
			t.Fatal(err)
		}
		return files
	}
	targets := []string{publish(skills[0]), publish(skills[1])}
	dir, problems, err := w.linkSkills(t.Context(), work, skills)
	if err != nil || len(problems) != 0 || dir != filepath.Join(os.Getenv("KIKO_CONTEXT_ROOT"), work.SessionID, "skills") {
		t.Fatal(dir, problems, err)
	}
	for i, s := range skills {
		if target, err := os.Readlink(filepath.Join(dir, s.Name)); err != nil || target != targets[i] {
			t.Fatal(target, err)
		}
	}
	skills[0].VersionID = NewID()
	next := publish(skills[0])
	skills[1].VersionID = NewID() // The pinned workspace asset is missing.
	_, problems, err = w.linkSkills(t.Context(), work, skills)
	if err != nil || len(problems) != 1 || problems[0] != "skill_unavailable:"+skills[1].SkillID {
		t.Fatal(problems, err)
	}
	if target, err := os.Readlink(filepath.Join(dir, "first")); err != nil || target != next {
		t.Fatal(target, err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "second")); !os.IsNotExist(err) {
		t.Fatal("stale Skill", err)
	}
	for _, s := range []integration.Skill{{Scope: "workspace", SkillID: "../other", Name: "x", VersionID: NewID()}, {Scope: "official", SkillID: NewID(), Name: "../other", VersionID: NewID()}} {
		if _, _, err := w.linkSkills(t.Context(), work, []integration.Skill{s}); err == nil {
			t.Fatal("accepted path escape")
		}
	}
	file := filepath.Join(next, "SKILL.md")
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(targets[1], "SKILL.md"), file); err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.linkSkills(t.Context(), work, skills); err == nil {
		t.Fatal("followed a foreign Skill entry")
	}
	if _, _, err := w.linkSkills(t.Context(), work, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("revoked context survived", err)
	}
	if _, err := os.Stat(targets[1]); err != nil {
		t.Fatal("removed published asset", err)
	}
}
