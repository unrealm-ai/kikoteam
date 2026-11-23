package service

import (
	"context"
	"strconv"
	"strings"
)

type InputAttachmentStatus struct {
	Status string `json:"status"`
	Name   string `json:"name,omitempty"`
	Path   string `json:"path,omitempty"`
	Advice string `json:"advice,omitempty"`
}

// InputAttachmentCandidate identifies an exact authorized reference, not a file path.
type InputAttachmentCandidate struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type InputAttachmentRecovery struct {
	Candidates []InputAttachmentCandidate `json:"candidates"`
	Truncated  bool                       `json:"candidates_truncated,omitempty"`
	Advice     string                     `json:"advice"`
}

// InputAttachmentLookupError retains the original argument/authorization error.
type InputAttachmentLookupError struct {
	Cause    error
	Recovery InputAttachmentRecovery
}

func (e *InputAttachmentLookupError) Error() string { return e.Cause.Error() }
func (e *InputAttachmentLookupError) Unwrap() error { return e.Cause }

type InputAttachmentReader interface {
	ReadInputAttachment(context.Context, DelegationIdentity, string) (InputAttachmentStatus, Conversation, error)
}

func SignInputAttachment(secret string, id DelegationIdentity) (string, error) {
	if len(secret) < 32 {
		return "", Fail("unauthenticated", 401, "附件执行身份无效")
	}
	return SignDelegation("input-attachments:"+secret, id)
}

func VerifyInputAttachment(secret, token string) (DelegationIdentity, error) {
	if len(secret) < 32 {
		return DelegationIdentity{}, Fail("unauthenticated", 401, "附件执行身份无效")
	}
	return VerifyDelegation("input-attachments:"+secret, token)
}

func SplitInputAttachmentID(id string) (inbox string, ordinal int, valid bool) {
	inbox, part, channel := strings.Cut(id, ":")
	if !ValidID(inbox) {
		return "", 0, false
	}
	if !channel {
		return inbox, -1, true
	}
	ordinal, err := strconv.Atoi(part)
	return inbox, ordinal, err == nil && ordinal >= 0 && ordinal < 32 && strconv.Itoa(ordinal) == part
}
