package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"
)

type SharedHistoryContext struct {
	Ref      string `json:"context_ref"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Coverage string `json:"coverage"`
}
type SharedHistoryRuntimeStore interface {
	SharedHistoryInput(context.Context, DelegationIdentity) (string, error)
	ListSharedHistoryContexts(context.Context, DelegationIdentity) ([]SharedHistoryContext, error)
	SharedHistoryGrant(context.Context, DelegationIdentity, string) (SharedHistoryScope, error)
	RequestSharedGroupHistory(context.Context, SharedHistoryScope, SharedHistoryRequest) (SharedHistoryResult, error)
	ReadSharedGroupHistory(context.Context, string, string) (SharedHistoryResult, error)
	ContinueSharedGroupHistory(context.Context, string, string) (string, error)
}
type GroupHistoryRuntime struct {
	Store  SharedHistoryRuntimeStore
	Secret string
}
type GroupHistoryArgs struct {
	ContextRef string `json:"context_ref,omitempty"`
	QueryID    string `json:"query_id,omitempty"`
	StartMS    int64  `json:"start_ms,omitempty"`
	EndMS      int64  `json:"end_ms,omitempty"`
	Limit      int    `json:"limit,omitempty"`
	Next       bool   `json:"next,omitempty"`
}

func groupHistorySecret(secret string) string {
	sum := sha256.Sum256([]byte("group-history:" + secret))
	return hex.EncodeToString(sum[:])
}
func SignGroupHistory(secret string, v DelegationIdentity) (string, error) {
	return SignDelegation(groupHistorySecret(secret), v)
}
func VerifyGroupHistory(secret, token string) (DelegationIdentity, error) {
	if len(secret) < 32 {
		return DelegationIdentity{}, Fail("unauthenticated", 401, "群历史运行身份无效")
	}
	return VerifyDelegation(groupHistorySecret(secret), token)
}
func (s *GroupHistoryRuntime) Authorize(ctx context.Context, token string) (DelegationIdentity, error) {
	id, err := VerifyGroupHistory(s.Secret, token)
	if err != nil {
		return id, err
	}
	_, err = s.Store.SharedHistoryInput(ctx, id)
	return id, err
}
func (s *GroupHistoryRuntime) Call(ctx context.Context, id DelegationIdentity, operation string, args GroupHistoryArgs) (any, error) {
	input, err := s.Store.SharedHistoryInput(ctx, id)
	if err != nil {
		return nil, err
	}
	switch operation {
	case "list_contexts":
		items, err := s.Store.ListSharedHistoryContexts(ctx, id)
		return map[string]any{"items": items, "coverage": "explicit_authorized_contexts", "warning": "Only controlled group-history reads enforce source lineage; this is not a shell/MCP data-loss-prevention boundary."}, err
	case "read_messages":
		if !ValidID(args.ContextRef) {
			return nil, Fail("invalid_argument", 400, "context_ref无效")
		}
		scope, err := s.Store.SharedHistoryGrant(ctx, id, args.ContextRef)
		if err != nil {
			return nil, err
		}
		if args.EndMS == 0 {
			args.EndMS = time.Now().UTC().UnixMilli()
		}
		if args.StartMS == 0 {
			args.StartMS = args.EndMS - 86400000
		}
		if args.Limit == 0 {
			args.Limit = 50
		}
		return s.Store.RequestSharedGroupHistory(ctx, scope, SharedHistoryRequest{StartMS: args.StartMS, EndMS: args.EndMS, Limit: args.Limit})
	case "get_query":
		if !ValidID(args.QueryID) {
			return nil, Fail("invalid_argument", 400, "query_id无效")
		}
		if args.Next {
			next, err := s.Store.ContinueSharedGroupHistory(ctx, args.QueryID, input)
			if err != nil {
				return nil, err
			}
			return s.Store.ReadSharedGroupHistory(ctx, next, input)
		}
		return s.Store.ReadSharedGroupHistory(ctx, args.QueryID, input)
	default:
		return nil, Fail("invalid_argument", 400, "不支持的群历史操作")
	}
}
