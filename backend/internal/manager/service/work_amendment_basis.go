package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

// ChatWorkItemView holds the authorized read snapshot. The MCP only exposes the
// item and its opaque basis, never asks the model for a source Input ID.
type ChatWorkItemView struct {
	ItemID       string
	AssignmentID string
	Version      int64
	Item         map[string]any
}

// WorkAmendmentRebase is returned only for a live assignment with a different
// requirements version. It is not a successful amendment receipt.
type WorkAmendmentRebase struct {
	Latest     ChatWorkItemView
	SourceText string
}

type workAmendmentBasis struct {
	Identity   DelegationIdentity
	Actor      string
	Input      string
	Item       string
	Assignment string
	Version    int64
	RetryFor   string
}

func (d *Delegation) signAmendmentBasis(v workAmendmentBasis) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, []byte(d.Secret))
	if _, err = mac.Write([]byte("work-amendment-basis:" + payload)); err != nil {
		return "", err
	}
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (d *Delegation) verifyAmendmentBasis(scope DelegationScope, args DelegationArgs) (workAmendmentBasis, error) {
	var v workAmendmentBasis
	bad := Fail("work_amendment_basis_invalid", 409, "本次事项补充与已查询的来源或版本不匹配，未写入")
	payload, _, ok := strings.Cut(args.AmendmentBasis, ".")
	if !ok || len(args.AmendmentBasis) > 2048 || len(d.Secret) < 32 {
		return v, bad
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil || json.Unmarshal(raw, &v) != nil {
		return v, bad
	}
	expect, err := d.signAmendmentBasis(v)
	if err != nil {
		return v, err
	}
	if !hmac.Equal([]byte(expect), []byte(args.AmendmentBasis)) ||
		v.Identity != scope.Identity || v.Actor != scope.ActorID ||
		!ValidID(v.Input) || v.Item != args.WorkItemID || v.Version != args.RequirementsVersion {
		return v, bad
	}
	return v, nil
}

func (d *Delegation) readChatWorkItem(ctx context.Context, scope DelegationScope, task string) (any, error) {
	view, err := d.Store.ChatWorkItem(ctx, scope, task)
	if err != nil {
		return nil, err
	}
	if view == nil {
		return map[string]any{"item": nil}, nil
	}
	out := map[string]any{"item": view.Item}
	if ValidID(scope.InputID) {
		out["amendment_basis"], err = d.signAmendmentBasis(workAmendmentBasis{
			Identity: scope.Identity, Actor: scope.ActorID, Input: scope.InputID, Item: view.ItemID, Assignment: view.AssignmentID, Version: view.Version,
		})
	}
	return out, err
}

func (d *Delegation) writeChatWorkItem(ctx context.Context, scope DelegationScope, args DelegationArgs, content string) (any, error) {
	basis, err := d.verifyAmendmentBasis(scope, args)
	if err != nil {
		return nil, err
	}
	key, err := delegationKey(scope.Identity.SessionID+":"+basis.Input, args.RequestKey)
	if err != nil {
		return nil, err
	}
	attemptDigest, err := fingerprint([]string{key, content})
	if err != nil {
		return nil, err
	}
	if basis.RetryFor != "" && basis.RetryFor != attemptDigest {
		return nil, Fail("work_amendment_basis_invalid", 409, "重试必须使用同一次补充的请求键和内容")
	}
	scope.InputID = basis.Input
	out, err := d.Store.AmendChatWorkItem(ctx, scope, args.WorkItemID, basis.Assignment, key, content, args.RequirementsVersion)
	rebase, ok := out.(WorkAmendmentRebase)
	var e *Error
	if !ok || !errors.As(err, &e) || e.Code != "work_version_changed" || basis.RetryFor != "" {
		return out, err
	}
	basis.Version, basis.RetryFor = rebase.Latest.Version, attemptDigest
	token, err := d.signAmendmentBasis(basis)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"status": "version_conflict", "applied": false, "item": rebase.Latest.Item,
		"requirements_version": strconv.FormatInt(basis.Version, 10),
		"original_message":     rebase.SourceText, "amendment_basis": token,
	}, nil
}
