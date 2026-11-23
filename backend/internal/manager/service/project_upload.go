package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const ProjectUploadLimit = 32 << 20
const ChannelMediaUploadLimit = 100 << 20
const uploadLedgerBase = "state/project-uploads-v2"

func uploadKeyDirectory(key string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(key)))
}

type UploadedFile struct {
	Attachment
	MediaType string `json:"media_type"`
}
type uploadJournal struct {
	Fingerprint string
	File        UploadedFile
	Candidate   int
	Published   bool `json:",omitempty"` // Written by v1 receipts before removing the staging link.
}

func (s *Service) WebProject(ctx context.Context, w, actor, id string) (Conversation, error) {
	c, err := s.Store.Conversation(ctx, w, actor, id)
	if err == nil {
		err = projectOwner(c, actor)
	}
	return c, err
}

func unlinkTemporary(dir *os.File, name string) error {
	err := unix.Unlinkat(int(dir.Fd()), name, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
func journalWrite(dir *os.File, record uploadJournal) error {
	const tmp = "next"
	if err := unlinkTemporary(dir, tmp); err != nil {
		return err
	}
	f, err := childFile(dir, tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL)
	if err != nil {
		return err
	}
	err = json.NewEncoder(f).Encode(record)
	if err == nil {
		err = f.Sync()
	}
	if err = errors.Join(err, f.Close()); err != nil {
		return err
	}
	if err = unix.Renameat(int(dir.Fd()), tmp, int(dir.Fd()), "receipt"); err != nil {
		return err
	}
	return dir.Sync()
}
func readJournal(dir *os.File) (uploadJournal, error) {
	var record uploadJournal
	f, err := childFile(dir, "receipt", unix.O_RDONLY)
	var e *Error
	if errors.As(err, &e) && e.Status == 404 {
		return record, nil
	}
	if err != nil {
		return record, err
	}
	defer closeProjectHandle(f)
	err = json.NewDecoder(f).Decode(&record)
	return record, err
}

func nextFilename(name string, n int) string {
	if n == 1 {
		return name
	}
	ext := path.Ext(name)
	if ext == name {
		ext = ""
	}
	return fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(name, ext), n, ext)
}

// Caller holds the conversation admission lock for this short publication.
// A durable candidate plus a consumed payload identifies a completed rename.
func publishUpload(root string, c Conversation, directory, name string, journal *os.File, record uploadJournal) (UploadedFile, error) {
	base := projectBase(c)
	project, err := projectDir(root, base, true)
	if err != nil {
		return UploadedFile{}, err
	}
	closeProjectHandle(project)
	target, err := projectDir(root, path.Join(base, directory), directory == "uploads")
	if err != nil {
		return UploadedFile{}, err
	}
	defer closeProjectHandle(target)
	src, dst := int(journal.Fd()), int(target.Fd())
	for n := max(1, record.Candidate); ; n++ {
		candidate := nextFilename(name, n)
		var existing unix.Stat_t
		err = unix.Fstatat(dst, candidate, &existing, unix.AT_SYMLINK_NOFOLLOW)
		if err == nil {
			continue
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return UploadedFile{}, fileError(err)
		}
		record.Candidate = n
		record.File.Name = candidate
		record.File.Path = path.Join(directory, candidate)
		if err = journalWrite(journal, record); err != nil {
			return UploadedFile{}, err
		}
		err = renameExclusive(src, "payload", dst, candidate)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrExist) {
			return UploadedFile{}, fileError(err)
		}
	}
	if err = target.Sync(); err != nil {
		return UploadedFile{}, err
	}
	return record.File, journal.Sync()
}

func (s *Service) UploadProject(ctx context.Context, c Conversation, actor, key, directory, name string, stream io.ReadSeeker) (UploadedFile, bool, error) {
	return s.uploadProject(ctx, c, actor, key, directory, name, stream, projectUploadGuards{})
}

// UploadChannelMedia shares the normal upload journal and verifies the lease
// while holding the same lock used by recovery, before publishing any bytes.
func (s *Service) UploadChannelMedia(ctx context.Context, c Conversation, actor, key, name string, stream io.ReadSeeker, check func() error) (UploadedFile, error) {
	file, _, err := s.uploadProject(ctx, c, actor, key, "uploads", name, stream, projectUploadGuards{preflight: check, sizeLimit: ChannelMediaUploadLimit})
	return file, err
}

type projectUploadGuards struct {
	sizeLimit int64
	preflight func() error
	publish   func(func() (UploadedFile, error)) (UploadedFile, error)
}

func (s *Service) uploadProject(ctx context.Context, c Conversation, actor, key, directory, name string, stream io.ReadSeeker, guards projectUploadGuards) (UploadedFile, bool, error) {
	var empty UploadedFile
	if !validKey(key) {
		return empty, false, Fail("idempotency_key_required", 400, "需要有效的上传去重键")
	}
	if !validProjectPath(name) || strings.Contains(name, "/") || directory != "" && !validProjectPath(directory) {
		return empty, false, Fail("invalid_argument", 400, "目录或文件名无效")
	}
	if err := projectActor(ctx, c, actor); err != nil {
		return empty, false, err
	}
	if c.Lifecycle != "active" {
		return empty, false, Fail("conversation_archived", 409, "会话已归档")
	}
	limit := guards.sizeLimit
	if limit == 0 {
		limit = ProjectUploadLimit
	}
	digest := sha256.New()
	size, err := io.Copy(digest, io.LimitReader(stream, limit+1))
	if err != nil {
		return empty, false, err
	}
	if size > limit {
		return empty, false, Fail("file_too_large", 413, fmt.Sprintf("文件超过 %d MiB", limit>>20))
	}
	fp, err := fingerprint([]string{directory, name, hex.EncodeToString(digest.Sum(nil))})
	if err != nil {
		return empty, false, err
	}
	ledger, err := s.lockUploadLedger(ctx, c, actor, key, guards.preflight != nil || guards.publish != nil)
	if err != nil {
		return empty, false, err
	}
	defer closeProjectHandle(ledger)
	if guards.preflight != nil {
		if err := guards.preflight(); err != nil {
			return empty, false, err
		}
	}
	record, err := readJournal(ledger)
	if err != nil {
		return empty, false, err
	}
	if record.Fingerprint != "" && record.Fingerprint != fp {
		return empty, false, Fail("idempotency_conflict", 409, "上传去重键已用于其他内容")
	}
	if record.Fingerprint != "" {
		var payload unix.Stat_t
		err = unix.Fstatat(int(ledger.Fd()), "payload", &payload, unix.AT_SYMLINK_NOFOLLOW)
		if errors.Is(err, fs.ErrNotExist) {
			sync := func() (UploadedFile, error) {
				return record.File, syncRecoveredUpload(s.ProjectRoot, c, directory, ledger)
			}
			if guards.publish != nil {
				result, err := guards.publish(sync)
				return result, err == nil, err
			}
			if guards.preflight != nil {
				result, err := sync()
				return result, err == nil, err
			}
			return record.File, true, nil
		}
		if err != nil {
			return empty, false, err
		}
	}
	if record.Fingerprint == "" {
		// A payload with no journal is an uncommitted staging attempt.
		if err = unlinkTemporary(ledger, "payload"); err != nil {
			return empty, false, err
		}
		payload, e := childFile(ledger, "payload", unix.O_RDWR|unix.O_CREAT|unix.O_EXCL)
		if e != nil {
			return empty, false, e
		}
		if _, e = stream.Seek(0, io.SeekStart); e == nil {
			_, e = io.Copy(payload, stream)
		}
		if e == nil {
			e = payload.Sync()
		}
		head := make([]byte, 512)
		count, readErr := payload.ReadAt(head, 0)
		if !errors.Is(readErr, io.EOF) {
			e = errors.Join(e, readErr)
		}
		if e = errors.Join(e, payload.Close()); e != nil {
			return empty, false, errors.Join(e, unlinkTemporary(ledger, "payload"))
		}
		mt := mime.TypeByExtension(path.Ext(name))
		if mt == "" {
			mt = http.DetectContentType(head[:count])
		}
		record = uploadJournal{Fingerprint: fp, File: UploadedFile{Attachment: Attachment{Size: size}, MediaType: mt}}
	}
	publish := func() (UploadedFile, error) {
		return publishUpload(s.ProjectRoot, c, directory, name, ledger, record)
	}
	result, err := s.publishProjectUpload(ctx, Target{Workspace: c.WorkspaceID, Actor: actor, Conversation: c.ID}, guards.publish, publish)
	if err != nil {
		if saved, readErr := readJournal(ledger); readErr == nil && saved.Fingerprint == "" {
			err = errors.Join(err, unlinkTemporary(ledger, "payload"))
		}
	}
	return result, false, err
}

// Publish only while the project is active; the journal makes filesystem
// publication idempotent even if the transaction result is lost.
func (s *Service) publishProjectUpload(ctx context.Context, target Target, override func(func() (UploadedFile, error)) (UploadedFile, error), publish func() (UploadedFile, error)) (UploadedFile, error) {
	if override != nil {
		return override(publish)
	}
	var result UploadedFile
	err := s.Store.WithProjectPublication(ctx, target, func(state SessionState) error {
		if err := admitted(state); err != nil {
			return err
		}
		var err error
		result, err = publish()
		return err
	})
	return result, err
}

func (s *Service) lockUploadLedger(ctx context.Context, c Conversation, actor, key string, cancelable bool) (*os.File, error) {
	ledger, err := projectDir(s.ProjectRoot, path.Join(uploadLedgerBase, c.WorkspaceID, c.ID, actor, uploadKeyDirectory(key)), true)
	if err != nil {
		return nil, err
	}
	if cancelable {
		err = waitForUploadLock(ctx, ledger)
	} else {
		err = unix.Flock(int(ledger.Fd()), unix.LOCK_EX)
	}
	if err != nil {
		closeProjectHandle(ledger)
		return nil, err
	}
	return ledger, nil
}

func waitForUploadLock(ctx context.Context, ledger *os.File) error {
	// A canceled settlement must not wait indefinitely behind an in-flight
	// upload. Use the request deadline even when the OS lock is contended.
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := unix.Flock(int(ledger.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EINTR) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// ReconcileChannelMedia observes a prior upload without publishing again. The
// settlement runs under the journal lock so no late uploader can slip between
// detecting a missing receipt and invalidating its lease.
func (s *Service) ReconcileChannelMedia(ctx context.Context, c Conversation, actor, key string, settle func(string) error) error {
	if err := projectActor(ctx, c, actor); err != nil {
		return err
	}
	if !validKey(key) {
		return Fail("idempotency_key_required", 400, "需要有效的上传去重键")
	}
	ledger, err := s.lockUploadLedger(ctx, c, actor, key, true)
	if err != nil {
		return err
	}
	defer closeProjectHandle(ledger)
	record, err := readJournal(ledger)
	if err != nil {
		return err
	}
	if record.Fingerprint != "" {
		var payload unix.Stat_t
		err = unix.Fstatat(int(ledger.Fd()), "payload", &payload, unix.AT_SYMLINK_NOFOLLOW)
		if errors.Is(err, fs.ErrNotExist) {
			if err := syncRecoveredUpload(s.ProjectRoot, c, "uploads", ledger); err != nil {
				return err
			}
			return settle(record.File.Path)
		}
		if err != nil {
			return err
		}
	}
	return settle("")
}

// Recovery may follow an interrupted rename before the original fsync.
func syncRecoveredUpload(root string, c Conversation, directory string, ledger *os.File) error {
	target, err := projectDir(root, path.Join(projectBase(c), directory), false)
	if err != nil {
		return err
	}
	defer closeProjectHandle(target)
	return errors.Join(target.Sync(), ledger.Sync())
}
