package service

import "golang.org/x/sys/unix"

func renameExclusive(src int, old string, dst int, name string) error {
	return unix.Renameat2(src, old, dst, name, unix.RENAME_NOREPLACE)
}
