package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/unrealm-ai/kikoteam/agent-engine/internal/integration"
	"github.com/unrealm-ai/kikoteam/agent-engine/internal/repository"
)

func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func validateSkill(skill integration.Skill) error {
	if skill.Manifest.FormatVersion != 1 || len(skill.Manifest.Files) == 0 || len(skill.Manifest.Files) > 1000 || len(skill.Manifest.Digest) != 64 {
		return errors.New("invalid Skill manifest")
	}
	data, err := json.Marshal(skill.Manifest.Files)
	if err != nil {
		return err
	}
	if digest(data) != skill.Manifest.Digest {
		return errors.New("skill manifest digest mismatch")
	}
	total := int64(0)
	seen := map[string]bool{}
	for _, f := range skill.Manifest.Files {
		if !fs.ValidPath(f.Path) || strings.ContainsAny(f.Path, "\\\r\n\x00") || len(f.SHA256) != 64 || f.Size < 0 || f.Size > 20<<20 || seen[f.Path] {
			return errors.New("invalid Skill file")
		}
		seen[f.Path] = true
		total += f.Size
	}
	if !seen["SKILL.md"] || total > 100<<20 {
		return errors.New("invalid Skill directory")
	}
	return nil
}

func validSkillCache(dir string, skill integration.Skill) bool {
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return false
	}
	defer func() { _ = root.Close() }() //nolint:errcheck // Read-only cache handle.
	count := 0
	err = fs.WalkDir(root.FS(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("linked Skill asset")
		}
		if !d.IsDir() && !d.Type().IsRegular() {
			return errors.New("non-regular Skill cache entry")
		}
		if !d.IsDir() {
			count++
			if count > len(skill.Manifest.Files) {
				return errors.New("unexpected Skill cache file")
			}
		}
		return nil
	})
	if err != nil || count != len(skill.Manifest.Files) {
		return false
	}
	for _, f := range skill.Manifest.Files {
		info, err := root.Stat(f.Path)
		if err != nil || !info.Mode().IsRegular() || info.Size() != f.Size {
			return false
		}
		file, err := root.Open(f.Path)
		if err != nil {
			return false
		}
		data, err := io.ReadAll(io.LimitReader(file, f.Size+1))
		closeErr := file.Close()
		if err != nil || closeErr != nil || int64(len(data)) != f.Size || digest(data) != f.SHA256 {
			return false
		}
	}
	return true
}

func (w *Worker) materializeSkill(ctx context.Context, work repository.Work, input, native string, skill integration.Skill) (string, error) {
	if err := validateSkill(skill); err != nil {
		return "", err
	}
	// Names come from trusted UUIDs in the Backend snapshot; manifest paths
	// are independently checked before any filesystem operation.
	base := filepath.Join(native, "skills", skill.SkillID, skill.VersionID)
	if len(skill.SkillID) != 36 || len(skill.VersionID) != 36 || strings.ContainsAny(skill.SkillID+skill.VersionID, "/\\.") {
		return "", errors.New("invalid Skill identity")
	}
	if err := os.MkdirAll(native, 0700); err != nil {
		return "", err
	}
	root, err := os.OpenRoot(native)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }() //nolint:errcheck // Directory handle; materialization errors are returned.
	relative := filepath.Join("skills", skill.SkillID, skill.VersionID)
	if err = root.MkdirAll(relative, 0700); err != nil {
		return "", err
	}
	baseRoot, err := root.OpenRoot(relative)
	if err != nil {
		return "", err
	}
	defer func() { _ = baseRoot.Close() }() //nolint:errcheck // Directory handle; publication errors are returned.
	dir := filepath.Join(base, skill.Manifest.Digest)
	if validSkillCache(dir, skill) {
		return dir, nil
	}
	stage := ".prepare-" + NewID()
	if err = baseRoot.Mkdir(stage, 0700); err != nil {
		return "", err
	}
	defer func() { _ = baseRoot.RemoveAll(stage) }() //nolint:errcheck // Only this unpublished staging directory is removed.
	stageRoot, err := baseRoot.OpenRoot(stage)
	if err != nil {
		return "", err
	}
	defer func() { _ = stageRoot.Close() }() //nolint:errcheck // File writes and publication errors are checked.
	for _, f := range skill.Manifest.Files {
		encoded, err := w.Backend.SkillFile(ctx, work, input, skill, f)
		if err != nil {
			return "", err
		}
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || int64(len(data)) != f.Size || digest(data) != f.SHA256 {
			return "", errors.New("skill asset digest mismatch")
		}
		path := filepath.FromSlash(f.Path)
		if err = stageRoot.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return "", err
		}
		if err = stageRoot.WriteFile(path, data, 0444); err != nil {
			return "", err
		}
	}
	if err = baseRoot.RemoveAll(skill.Manifest.Digest); err != nil {
		return "", err
	}
	if err = baseRoot.Rename(stage, skill.Manifest.Digest); err != nil {
		return "", err
	}
	return dir, nil
}
