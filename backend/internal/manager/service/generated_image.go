package service

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"path"

	"golang.org/x/sys/unix"
)

// Originals live outside writable project directories and are addressed by the admission ID.
func ImageOriginal(root, workspace, id string) ([]byte, error) {
	if !ValidID(workspace) || !ValidID(id) {
		return nil, Fail("invalid_argument", 400, "图片归属无效")
	}
	f, info, err := openProjectFile(root, path.Join("state/image-generations", workspace), id+".png")
	if err != nil {
		return nil, err
	}
	defer closeProjectHandle(f)
	if info.Size() > ProjectUploadLimit {
		return nil, Fail("file_too_large", 413, "图片过大")
	}
	return io.ReadAll(io.LimitReader(f, ProjectUploadLimit+1))
}
func SaveImageOriginal(root, workspace, id string, data []byte) error {
	if !ValidID(workspace) || !ValidID(id) {
		return Fail("invalid_argument", 400, "图片归属无效")
	}
	return putImageFile(root, path.Join("state/image-generations", workspace), id+".png", data)
}
func MaterializeImage(root, workspace, agent, conversation, space, task, id string, data []byte) error {
	if !ValidID(id) {
		return Fail("invalid_argument", 400, "图片标识无效")
	}
	base, err := workProjectBase(workspace, agent, conversation, space, task)
	if err != nil {
		return err
	}
	return putImageFile(root, path.Join(base, "outputs/images"), id+".png", data)
}
func putImageFile(root, base, name string, data []byte) error {
	dir, err := projectDir(root, base, true)
	if err != nil {
		return err
	}
	defer closeProjectHandle(dir)
	tmp := ".image-" + NewID()
	f, err := childFile(dir, tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Unlinkat(int(dir.Fd()), tmp, 0) }() //nolint:errcheck // The exclusive rename removes the temporary name on success.
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	if err = errors.Join(err, f.Close()); err != nil {
		return err
	}
	fd := int(dir.Fd())
	err = renameExclusive(fd, tmp, fd, name)
	if errors.Is(err, fs.ErrExist) {
		prior, _, e := openProjectFile(root, base, name)
		if e != nil {
			return e
		}
		defer closeProjectHandle(prior)
		old, e := io.ReadAll(io.LimitReader(prior, int64(len(data))+1))
		if e != nil {
			return e
		}
		if !bytes.Equal(old, data) {
			return Fail("image_file_conflict", 409, "同名文件内容已变化")
		}
	} else if err != nil {
		return err
	}
	return dir.Sync()
}
