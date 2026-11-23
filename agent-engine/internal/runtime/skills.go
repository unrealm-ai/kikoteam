package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"regexp"

	"github.com/unrealm-ai/kikoteam/agent-engine/internal/integration"
	"github.com/unrealm-ai/kikoteam/agent-engine/internal/repository"
)

func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

var skillName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// The Backend authorizes fixed versions. Derive each target from its owner
// here so that a configuration can never point Pi at an arbitrary directory.
func skillTarget(root, workspace string, s integration.Skill) (string, error) {
	if !uuidPattern.MatchString(workspace) || !uuidPattern.MatchString(s.SkillID) || !uuidPattern.MatchString(s.VersionID) || len(s.Name) > 64 || !skillName.MatchString(s.Name) {
		return "", errors.New("invalid Skill identity")
	}
	base := "official"
	switch s.Scope {
	case "workspace":
		base = filepath.Join("workspaces", workspace)
	case "official":
	default:
		return "", errors.New("invalid Skill scope")
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	relative := filepath.Join(base, "skills", s.SkillID, "versions", s.VersionID, "files")
	entry := filepath.Join(canonical, relative, "SKILL.md")
	resolved, err := filepath.EvalSymlinks(entry)
	if err != nil {
		return "", err
	}
	if resolved != entry {
		return "", errors.New("unexpected symlink in published Skill")
	}
	info, err := os.Stat(entry)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("invalid Skill entry point")
	}
	return filepath.Join(root, relative), nil
}

// Only the Session's assignment may write this directory. Existing links are
// kept while the same pinned version is selected, including across Turns.
func (w *Worker) linkSkills(ctx context.Context, work repository.Work, skills []integration.Skill) (string, []string, error) {
	session, err := sessionNodePath("context", work.SessionID)
	if err != nil {
		return "", nil, err
	}
	dir := filepath.Join(session, "skills")
	wanted := make(map[string]string, len(skills))
	var problems []string
	for _, skill := range skills {
		if err := ctx.Err(); err != nil {
			return "", nil, err
		}
		target, err := skillTarget(w.Root, work.WorkspaceID, skill)
		if errors.Is(err, os.ErrNotExist) {
			problems = append(problems, "skill_unavailable:"+skill.SkillID)
			continue
		}
		if err != nil {
			return "", nil, err
		}
		if _, exists := wanted[skill.Name]; exists {
			return "", nil, errors.New("duplicate Skill name")
		}
		wanted[skill.Name] = target
	}
	if len(wanted) == 0 {
		return "", problems, os.RemoveAll(session)
	}
	for _, path := range []string{session, dir} {
		if err := ensurePrivateDir(path); err != nil {
			return "", nil, err
		}
	}
	existing, err := os.ReadDir(dir)
	if err != nil {
		return "", nil, err
	}
	for _, entry := range existing {
		path := filepath.Join(dir, entry.Name())
		target, err := os.Readlink(path)
		if err != nil {
			return "", nil, err
		}
		if wanted[entry.Name()] == target {
			delete(wanted, entry.Name())
		} else if err := os.Remove(path); err != nil {
			return "", nil, err
		}
	}
	for name, target := range wanted {
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			return "", nil, err
		}
	}
	return dir, problems, nil
}
