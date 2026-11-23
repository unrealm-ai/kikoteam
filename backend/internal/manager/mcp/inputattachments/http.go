// Package inputattachments exposes the read-only task attachment lookup.
package inputattachments

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

type identityKey struct{}
type query struct {
	AttachmentID string `json:"attachment_id" jsonschema:"附件 ID，来自任务输入的附件引用"`
}

func Register(mux *http.ServeMux, reader service.InputAttachmentReader, manager *service.Service, secret string) {
	server := sdk.NewServer(&sdk.Implementation{Name: "input_attachments", Version: "1.0.0"}, nil)
	sdk.AddTool(server, &sdk.Tool{Name: "attachment_get", Description: "查询已授权的输入附件。ID 错误时可返回当前任务的候选；先确认目标，再复制完整 ID，不自动换用其他文件。downloading 或 failed 不返回路径；ready 返回项目相对路径。不要立即反复调用。"}, func(ctx context.Context, _ *sdk.CallToolRequest, args query) (*sdk.CallToolResult, any, error) {
		id, ok := ctx.Value(identityKey{}).(service.DelegationIdentity)
		if !ok {
			return nil, nil, errors.New("unauthenticated")
		}
		result, conv, err := reader.ReadInputAttachment(ctx, id, args.AttachmentID)
		if err != nil {
			return &sdk.CallToolResult{IsError: true}, attachmentErrorResult(err), nil
		}
		validateAttachmentResult(manager, conv, &result)
		return nil, result, nil
	})
	handler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, &sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	mux.HandleFunc("/mcp/input-attachments", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || r.Header.Get("Cookie") != "" || r.Header.Get("Origin") != "" {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		id, err := service.VerifyInputAttachment(secret, token)
		if err != nil {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		handler.ServeHTTP(w, r.WithContext(context.WithValue(ctx, identityKey{}, id)))
	})
}

func attachmentErrorResult(err error) map[string]any {
	code, msg := "dependency_unavailable", "附件查询暂时不可用"
	var problem *service.Error
	if errors.As(err, &problem) {
		code, msg = problem.Code, problem.Message
	}
	out := map[string]any{"error": map[string]string{"code": code, "message": msg}}
	var lookup *service.InputAttachmentLookupError
	if errors.As(err, &lookup) {
		out["candidates"] = lookup.Recovery.Candidates
		out["advice"] = lookup.Recovery.Advice
		if lookup.Recovery.Truncated {
			out["candidates_truncated"] = true
		}
	}
	return out
}

func validateAttachmentResult(manager *service.Service, conv service.Conversation, result *service.InputAttachmentStatus) {
	if result.Status == "ready" {
		file, _, err := manager.OpenProjectFile(conv, result.Path)
		if err == nil {
			_ = file.Close() //nolint:errcheck // This only validates the relative path.
			return
		}
		result.Status = "failed"
	}
	result.Path = ""
	switch result.Status {
	case "downloading":
		result.Advice = "附件仍在准备；不要立即反复查询。需要时稍后用同一 ID 查询。"
	case "failed":
		result.Advice = "此附件未能准备好，无可用路径。不要自行替换；如仍需此文件，请用户重新发送。"
	}
}
