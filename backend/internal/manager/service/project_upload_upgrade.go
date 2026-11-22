package service

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// UpgradeProjectUploadReceipts rehomes v1 journals before accepting traffic.
// Directory enumeration preserves the original spelling even on case-insensitive volumes.
func UpgradeProjectUploadReceipts(root string) error {
	const legacy = "state/project-uploads"
	dir, err := projectDir(root, legacy, false)
	var missing *Error
	if errors.As(err, &missing) && missing.Status == 404 {
		return nil
	}
	if err != nil {
		return err
	}
	defer closeProjectHandle(dir)
	if err = unix.Flock(int(dir.Fd()), unix.LOCK_EX); err != nil {
		return err
	}
	return fs.WalkDir(os.DirFS(filepath.Join(root, legacy)), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() || strings.Count(p, "/") != 3 {
			return nil
		}
		ids := strings.Split(p, "/")
		if !ValidID(ids[0]) || !ValidID(ids[1]) || !ValidID(ids[2]) || !validKey(ids[3]) {
			return fs.SkipDir
		}
		if err := upgradeProjectUpload(root, path.Join(legacy, path.Dir(p)), path.Join(uploadLedgerBase, path.Dir(p)), ids[3]); err != nil {
			return err
		}
		return fs.SkipDir
	})
}

func upgradeProjectUpload(root, oldParent, newParent, key string) error {
	src, err := projectDir(root, oldParent, false)
	if err != nil {
		return err
	}
	defer closeProjectHandle(src)
	journal, err := childFile(src, key, unix.O_DIRECTORY|unix.O_RDONLY)
	if err != nil {
		return err
	}
	defer closeProjectHandle(journal)
	if err = unix.Flock(int(journal.Fd()), unix.LOCK_EX); err != nil {
		return err
	}
	record, err := readJournal(journal)
	if err != nil {
		return err
	}
	// V1 only added a second link after its exact candidate was journaled.
	var payload unix.Stat_t
	err = unix.Fstatat(int(journal.Fd()), "payload", &payload, unix.AT_SYMLINK_NOFOLLOW)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err == nil && (record.Published || (record.Candidate > 0 && payload.Nlink > 1)) {
		if err = unlinkTemporary(journal, "payload"); err != nil {
			return err
		}
		if err = journal.Sync(); err != nil {
			return err
		}
	}
	dst, err := projectDir(root, newParent, true)
	if err != nil {
		return err
	}
	defer closeProjectHandle(dst)
	if err = renameExclusive(int(src.Fd()), key, int(dst.Fd()), uploadKeyDirectory(key)); err != nil {
		return err
	}
	return errors.Join(dst.Sync(), src.Sync())
}
