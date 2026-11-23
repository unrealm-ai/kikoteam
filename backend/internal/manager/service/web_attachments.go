package service

import (
	"context"
	"io"
	"strings"
	"time"
)

const webAttachmentLifetime = 10 * time.Minute

type WebAttachmentStore interface {
	ReserveWebAttachment(context.Context, Target, Attachment, string, time.Duration) (Attachment, bool, error)
	WebAttachment(context.Context, Target, string) (Attachment, error)
	BeginWebAttachment(context.Context, Target, string, string) (Attachment, error)
	PublishWebAttachment(context.Context, Target, string, string, func(SessionState) (string, error)) (Attachment, error)
	FinishWebAttachmentFailure(context.Context, Target, string, string) error
}

type WebAttachmentUpload struct {
	Attachment
	Conversation Conversation
	Actor, Token string
}

func (s *Service) webAttachmentStore() (WebAttachmentStore, error) {
	if store, ok := s.Store.(WebAttachmentStore); ok {
		return store, nil
	}
	return nil, Fail("dependency_unavailable", 503, "附件存储不可用")
}

func (s *Service) ReserveWebAttachment(ctx context.Context, w, a, c, key, name string, size int64) (Attachment, bool, error) {
	if !ValidID(c) || !validKey(key) || !validProjectPath(name) || strings.Contains(name, "/") || len(name) > 255 || size < 0 {
		return Attachment{}, false, Fail("invalid_argument", 400, "附件名称、大小或去重键无效")
	}
	if size > ProjectUploadLimit {
		return Attachment{}, false, Fail("file_too_large", 413, "文件超过 32 MiB")
	}
	store, err := s.webAttachmentStore()
	if err != nil {
		return Attachment{}, false, err
	}
	return store.ReserveWebAttachment(ctx, Target{Workspace: w, Actor: a, Conversation: c}, Attachment{ID: NewID(), Name: name, Size: size}, key, webAttachmentLifetime)
}

func (s *Service) WebAttachment(ctx context.Context, w, a, c, id string) (Attachment, error) {
	if !ValidID(c) || !ValidID(id) {
		return Attachment{}, Fail("invalid_argument", 400, "附件标识无效")
	}
	store, err := s.webAttachmentStore()
	if err != nil {
		return Attachment{}, err
	}
	return store.WebAttachment(ctx, Target{Workspace: w, Actor: a, Conversation: c}, id)
}

func (s *Service) BeginWebAttachment(ctx context.Context, w, a, c, id string) (WebAttachmentUpload, error) {
	if !ValidID(c) || !ValidID(id) {
		return WebAttachmentUpload{}, Fail("invalid_argument", 400, "附件标识无效")
	}
	conv, err := s.WebProject(ctx, w, a, c)
	if err != nil {
		return WebAttachmentUpload{}, err
	}
	store, err := s.webAttachmentStore()
	if err != nil {
		return WebAttachmentUpload{}, err
	}
	token := NewID()
	item, err := store.BeginWebAttachment(ctx, Target{Workspace: w, Actor: a, Conversation: c}, id, token)
	if item.Status != "downloading" || err != nil {
		token = ""
	}
	return WebAttachmentUpload{Attachment: item, Conversation: conv, Actor: a, Token: token}, err
}

func (s *Service) PublishWebAttachment(ctx context.Context, v WebAttachmentUpload, name string, size int64, stream io.ReadSeeker) (Attachment, error) {
	if size != v.Size || name != v.Name {
		return Attachment{}, Fail("invalid_argument", 400, "上传内容与预留附件不一致")
	}
	store, err := s.webAttachmentStore()
	if err != nil {
		return Attachment{}, err
	}
	target := Target{Workspace: v.Conversation.WorkspaceID, Actor: v.Actor, Conversation: v.Conversation.ID}
	var ready Attachment
	guards := projectUploadGuards{publish: func(publish func() (UploadedFile, error)) (UploadedFile, error) {
		var file UploadedFile
		var err error
		ready, err = store.PublishWebAttachment(ctx, target, v.ID, v.Token, func(state SessionState) (string, error) {
			if err := projectOwner(state.Conversation, v.Actor); err != nil {
				return "", err
			}
			if err := admitted(state); err != nil {
				return "", err
			}
			f, err := publish()
			file = f
			return file.Path, err
		})
		return file, err
	}}
	_, _, err = s.uploadProject(ctx, v.Conversation, v.Actor, "attachment-"+v.ID, "uploads", v.Name, stream, guards)
	return ready, err
}

func (s *Service) FailWebAttachment(ctx context.Context, v WebAttachmentUpload) error {
	store, err := s.webAttachmentStore()
	if err != nil {
		return err
	}
	return store.FinishWebAttachmentFailure(ctx, Target{Workspace: v.Conversation.WorkspaceID, Actor: v.Actor, Conversation: v.Conversation.ID}, v.ID, v.Token)
}
