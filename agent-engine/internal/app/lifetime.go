package app

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// One immutable file per boot. The kernel releases the lock after the last
// Engine, Pi, Runner or builtin descriptor closes, including on process death.
func lockLifetime(root, id string) (*os.File, error) {
	dir := filepath.Join(root, "temp", "runtime")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, id+".lock"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, errors.Join(err, f.Close())
	}
	return f, nil
}
