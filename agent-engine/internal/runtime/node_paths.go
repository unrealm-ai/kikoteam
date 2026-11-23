package runtime

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// Node-local context and process files stay outside the durable storage root.
func nodeDirectory(name string) (string, error) {
	key := "KIKO_CONTEXT_ROOT"
	if name == "temp" {
		key = "KIKO_RUNTIME_TEMP_ROOT"
	}
	dir := os.Getenv(key)
	if dir == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(cache, "Kiko", name)
	}
	if !filepath.IsAbs(dir) {
		return "", errors.New(key + " must be absolute")
	}
	return dir, os.MkdirAll(dir, 0700)
}

// ValidateNodeRoots prevents cleanup of a node-local directory from deleting
// durable Session state or the other node-local tree.
func ValidateNodeRoots(storage string) error {
	roots := []string{storage}
	for _, kind := range []string{"temp", "context"} {
		dir, err := nodeDirectory(kind)
		if err != nil {
			return err
		}
		roots = append(roots, dir)
	}
	for i, path := range roots {
		dir, err := filepath.EvalSymlinks(path)
		if err != nil {
			return err
		}
		roots[i] = dir
		for _, prior := range roots[:i] {
			rel, err := filepath.Rel(prior, dir)
			if err != nil {
				return err
			}
			back, err := filepath.Rel(dir, prior)
			if err != nil {
				return err
			}
			if filepath.IsLocal(rel) || filepath.IsLocal(back) {
				return errors.New("durable storage, node context and runtime temp roots must be disjoint")
			}
		}
	}
	return nil
}

func sessionNodePath(kind, id string) (string, error) {
	if !uuidPattern.MatchString(id) {
		return "", errors.New("invalid Session ID")
	}
	root, err := nodeDirectory(kind)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, id), nil
}

func ensurePrivateDir(path string) error {
	if err := os.Mkdir(path, 0700); !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode().Perm() != 0700 || !ok || int(owner.Uid) != os.Geteuid() {
		return errors.New("unsafe Session directory")
	}
	return nil
}
