package service

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode"

	"golang.org/x/sys/unix"
)

type Attachment struct {
	ID     string `json:"id,omitempty"`
	Path   string `json:"path,omitempty"`
	Name   string `json:"name"`
	Size   int64  `json:"size_bytes"`
	Status string `json:"status,omitempty"`
}

type ProjectEntry struct {
	Kind     string     `json:"kind"`
	Name     string     `json:"name"`
	Path     string     `json:"path"`
	Size     *int64     `json:"size_bytes,omitempty"`
	Modified *time.Time `json:"modified_at,omitempty"`
}
type Directory struct {
	Path    string         `json:"path"`
	Entries []ProjectEntry `json:"entries"`
}

func validProjectPath(p string) bool {
	return p != "." && fs.ValidPath(p) && len(p) <= 4096 && !strings.Contains(p, "\\") && strings.IndexFunc(p, unicode.IsControl) < 0
}
func fileError(err error) error {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return Fail("resource_not_found", 404, "文件或目录已移动或删除")
	case errors.Is(err, unix.ELOOP), errors.Is(err, unix.ENOTDIR), errors.Is(err, unix.ENAMETOOLONG):
		return Fail("invalid_argument", 400, "文件类型或路径无效")
	default:
		return err
	}
}
func projectOwner(c Conversation, actor string) error {
	if c.WebUserID == nil || *c.WebUserID != actor || !ValidID(c.ID) || !ValidID(c.WorkspaceID) || !ValidID(c.AgentID) {
		return Fail("resource_not_found", 404, "私聊项目不存在")
	}
	return nil
}
func closeProjectHandle(f *os.File) { _ = f.Close() } //nolint:errcheck // Read handles and directory locks have no buffered writes.
func childFile(parent *os.File, name string, flags int) (*os.File, error) {
	fd, err := unix.Openat(int(parent.Fd()), name, flags|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
	if err != nil {
		return nil, fileError(err)
	}
	return os.NewFile(uintptr(fd), name), nil
}

// Walk directory descriptors without following links, including the owner prefix.
func projectDir(root, rel string, create bool) (*os.File, error) {
	if !filepath.IsAbs(root) {
		return nil, Fail("dependency_unavailable", 503, "项目存储未配置")
	}
	if create {
		if err := os.MkdirAll(root, 0700); err != nil {
			return nil, err
		}
	}
	dir, err := os.Open(root)
	if err != nil {
		return nil, fileError(err)
	}
	for _, name := range strings.Split(rel, "/") {
		if create {
			err = unix.Mkdirat(int(dir.Fd()), name, 0700)
			if err != nil && !errors.Is(err, fs.ErrExist) {
				closeProjectHandle(dir)
				return nil, fileError(err)
			}
		}
		next, e := childFile(dir, name, unix.O_DIRECTORY|unix.O_RDONLY)
		closeProjectHandle(dir)
		if e != nil {
			return nil, e
		}
		dir = next
	}
	return dir, nil
}
func projectBase(c Conversation) string {
	return path.Join("state/sandboxes/workspaces", c.WorkspaceID, "employees", c.AgentID, "conversations", c.ID, "project")
}
func (s *Service) OpenProjectFile(c Conversation, p string) (*os.File, os.FileInfo, error) {
	return openProjectFile(s.ProjectRoot, projectBase(c), p)
}
func openProjectFile(root, base, p string) (*os.File, os.FileInfo, error) {
	if !validProjectPath(p) {
		return nil, nil, Fail("invalid_argument", 400, "需要项目内的文件路径")
	}
	dir, err := projectDir(root, path.Join(base, path.Dir(p)), false)
	if err != nil {
		return nil, nil, err
	}
	defer closeProjectHandle(dir)
	file, err := childFile(dir, path.Base(p), unix.O_RDONLY|unix.O_NONBLOCK)
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Sys().(*syscall.Stat_t).Nlink != 1 {
		closeProjectHandle(file)
		if err != nil {
			return nil, nil, err
		}
		return nil, nil, Fail("invalid_argument", 400, "请选择普通文件")
	}
	return file, info, nil
}
func (s *Service) ListProject(c Conversation, p string) (Directory, error) {
	out := Directory{Path: p, Entries: []ProjectEntry{}}
	if p != "" && !validProjectPath(p) {
		return out, Fail("invalid_argument", 400, "目录路径无效")
	}
	dir, err := projectDir(s.ProjectRoot, path.Join(projectBase(c), p), false)
	var missing *Error
	if p == "" && errors.As(err, &missing) && missing.Status == 404 {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	defer closeProjectHandle(dir)
	// Readdirnames and fstatat both address the pinned directory even if it moves.
	names, err := dir.Readdirnames(-1)
	if err != nil {
		return out, err
	}
	for _, name := range names {
		if !validProjectPath(name) {
			continue
		}
		var stat unix.Stat_t
		if e := unix.Fstatat(int(dir.Fd()), name, &stat, unix.AT_SYMLINK_NOFOLLOW); e != nil {
			if errors.Is(e, fs.ErrNotExist) {
				continue
			}
			return out, e
		}
		entry := ProjectEntry{Name: name, Path: path.Join(p, name), Kind: "directory"}
		switch stat.Mode & unix.S_IFMT {
		case unix.S_IFDIR:
		case unix.S_IFREG:
			if stat.Nlink != 1 {
				continue
			}
			size := stat.Size
			modified := time.Unix(stat.Mtim.Sec, stat.Mtim.Nsec).UTC()
			entry.Kind = "file"
			entry.Size = &size
			entry.Modified = &modified
		default:
			continue
		}
		out.Entries = append(out.Entries, entry)
	}
	sort.Slice(out.Entries, func(i, j int) bool {
		a, b := out.Entries[i], out.Entries[j]
		if a.Kind != b.Kind {
			return a.Kind == "directory"
		}
		return a.Name < b.Name
	})
	return out, nil
}
func (s *Service) ResolveAttachments(c Conversation, actor string, paths []string) ([]Attachment, error) {
	return s.resolveInputAttachments(context.Background(), c, actor, paths)
}
func projectActor(ctx context.Context, c Conversation, actor string) error {
	if ch, ok := ChannelActorFrom(ctx); ok && ch.ID == actor && !ch.ConfirmationOnly && ch.Generation > 0 && c.ChannelBindingID != nil && ValidID(actor) && ValidID(c.ID) && ValidID(c.WorkspaceID) && ValidID(c.AgentID) {
		return nil
	}
	return projectOwner(c, actor)
}
func (s *Service) resolveInputAttachments(ctx context.Context, c Conversation, actor string, paths []string) ([]Attachment, error) {
	if len(paths) == 0 {
		return []Attachment{}, nil
	}
	if err := projectActor(ctx, c, actor); err != nil {
		return nil, err
	}
	if len(paths) > 32 {
		return nil, Fail("invalid_argument", 400, "每条消息最多引用 32 个文件")
	}
	seen := map[string]bool{}
	out := make([]Attachment, 0, len(paths))
	for _, p := range paths {
		if seen[p] {
			return nil, Fail("invalid_argument", 400, "附件路径重复")
		}
		seen[p] = true
		file, info, err := s.OpenProjectFile(c, p)
		if err != nil {
			var problem *Error
			if errors.As(err, &problem) && validProjectPath(p) {
				return nil, Fail(problem.Code, problem.Status, p+"："+problem.Message)
			}
			return nil, err
		}
		if err = file.Close(); err != nil {
			return nil, err
		}
		out = append(out, Attachment{Path: p, Name: path.Base(p), Size: info.Size()})
	}
	return out, nil
}

func ReadWorkFile(root, workspace, agent, conversation, space, task, p string) ([]byte, error) {
	if !ValidID(workspace) || !ValidID(agent) {
		return nil, Fail("invalid_argument", 400, "事项文件归属无效")
	}
	base := path.Join("state/sandboxes/workspaces", workspace, "employees", agent)
	switch {
	case ValidID(conversation) && space == "":
		base = path.Join(base, "conversations", conversation, "project")
	case conversation == "" && ValidID(space) && ValidID(task):
		base = path.Join(base, "taskspaces", space, "tasks", task, "project")
	default:
		return nil, Fail("invalid_argument", 400, "事项文件归属无效")
	}
	f, info, err := openProjectFile(root, base, p)
	if err != nil {
		return nil, err
	}
	defer closeProjectHandle(f)
	if info.Size() > ProjectUploadLimit {
		return nil, Fail("file_too_large", 413, "单文件超过32 MiB")
	}
	data, err := io.ReadAll(io.LimitReader(f, ProjectUploadLimit+1))
	if err != nil {
		return nil, err
	}
	end, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != info.Size() || end.Size() != info.Size() || !end.ModTime().Equal(info.ModTime()) {
		return nil, Fail("file_changed", 409, "读取期间文件已改变，请重新提交")
	}
	return data, nil
}
