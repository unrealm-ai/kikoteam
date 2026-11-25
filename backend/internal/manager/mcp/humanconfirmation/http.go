// Package humanconfirmation exposes existing Manager confirmation operations to Pi.
package humanconfirmation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	contract "github.com/unrealm-ai/kikoteam/backend/contracts/humanconfirmation"
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

type Store interface {
	HumanChatList(context.Context, service.HumanRuntimeScope, service.HumanListQuery) (any, error)
	HumanTaskTool(context.Context, service.HumanRuntimeScope, string, string, *service.HumanQuestion) (any, error)
	HumanChatSubmission(context.Context, service.HumanRuntimeScope, string, string, *service.HumanAnswer) (any, error)
	HumanChatSubmitValues(context.Context, service.HumanRuntimeScope, string, string, service.HumanAnswer, []service.HumanModelAnswerItem) (any, error)
}

type scopeKey struct{}
type catalogOnlyKey struct{}

func Register(mux *http.ServeMux, store Store, secret string) error {
	var specs map[string][]sdk.Tool
	if err := json.Unmarshal(contract.Tools, &specs); err != nil {
		return err
	}
	for _, mode := range []string{"chat", "task"} {
		server := sdk.NewServer(&sdk.Implementation{Name: "human_confirmation", Version: "1.0.0"}, nil)
		for _, spec := range specs[mode] {
			if err := registerTool(server, &spec, store); err != nil {
				return err
			}
		}
		mux.Handle("/mcp/human-confirmations/"+mode, handler(server, secret, mode))
	}
	return nil
}

type operationArgs struct {
	RequestID   string `json:"request_id"`
	OperationID string `json:"operationId,omitempty"`
}

type answerArgs struct {
	operationArgs
	service.HumanAnswer
	Answers []service.HumanModelAnswerItem `json:"answers"`
}

func registerTool(server *sdk.Server, spec *sdk.Tool, store Store) error {
	switch spec.Name {
	case "list_pending_human_confirmations":
		addTool(server, spec, func(ctx context.Context, scope service.HumanRuntimeScope, args service.HumanListQuery) (any, error) {
			return store.HumanChatList(ctx, scope, args)
		})
	case "submit_human_confirmation":
		addTool(server, spec, func(ctx context.Context, scope service.HumanRuntimeScope, args answerArgs) (any, error) {
			if !service.ValidID(args.RequestID) {
				return nil, service.Fail("invalid_reference", 400, "确认请求标识无效")
			}
			return store.HumanChatSubmitValues(ctx, scope, args.RequestID, args.OperationID, args.HumanAnswer, args.Answers)
		})
	case "get_human_confirmation_receipt":
		addTool(server, spec, func(ctx context.Context, scope service.HumanRuntimeScope, args operationArgs) (any, error) {
			return store.HumanChatSubmission(ctx, scope, args.RequestID, args.OperationID, nil)
		})
	case "list_human_confirmation_scopes":
		addTool(server, spec, func(ctx context.Context, scope service.HumanRuntimeScope, _ struct{}) (any, error) {
			return store.HumanTaskTool(ctx, scope, "recipients", "", nil)
		})
	default:
		return errors.New("unknown human confirmation MCP tool: " + spec.Name)
	}
	return nil
}

func addTool[Args any](server *sdk.Server, spec *sdk.Tool, call func(context.Context, service.HumanRuntimeScope, Args) (any, error)) {
	sdk.AddTool(server, spec, func(ctx context.Context, _ *sdk.CallToolRequest, args Args) (*sdk.CallToolResult, any, error) {
		if ctx.Value(catalogOnlyKey{}) == true {
			return &sdk.CallToolResult{IsError: true}, map[string]any{"error": map[string]string{
				"code": "human_tool_forbidden", "message": "当前消息无权使用人工确认工具",
			}}, nil
		}
		scope, ok := ctx.Value(scopeKey{}).(service.HumanRuntimeScope)
		if !ok {
			return nil, nil, errors.New("unauthenticated")
		}
		out, err := call(ctx, scope, args)
		if err == nil {
			return nil, out, nil
		}
		code, message := "dependency_unavailable", "人工确认服务暂不可用"
		var failure *service.Error
		if errors.As(err, &failure) {
			code, message = failure.Code, failure.Message
		}
		return &sdk.CallToolResult{IsError: true}, map[string]any{"error": map[string]string{"code": code, "message": message}}, nil
	})
}

func handler(server *sdk.Server, secret, mode string) http.Handler {
	transport := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, &sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Request-ID", service.NewID())
		if r.Header.Get("Origin") != "" || r.Header.Get("Cookie") != "" {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		scope, catalogOnly, ok := requestScope(r, secret, mode)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if catalogOnly {
			ctx = context.WithValue(ctx, catalogOnlyKey{}, true)
		} else {
			ctx = context.WithValue(ctx, scopeKey{}, scope)
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		transport.ServeHTTP(w, r.WithContext(ctx))
	})
}

func requestScope(r *http.Request, secret, mode string) (scope service.HumanRuntimeScope, catalogOnly, ok bool) {
	if auth := r.Header.Values("Authorization"); len(auth) != 0 {
		token, bearer := strings.CutPrefix(auth[0], "Bearer ")
		if !bearer || len(auth) != 1 {
			return scope, false, false
		}
		input := r.Header.Get("X-Kiko-Human-Input")
		identity, err := service.VerifyHumanMCP(secret, mode, input, token)
		if err != nil {
			return scope, false, false
		}
		return service.HumanRuntimeScope{Identity: identity, InputID: input}, false, true
	}
	if mode != "chat" || r.URL.Query().Get("catalog") != "stable" {
		return scope, false, false
	}
	grant, err := service.VerifyChatToolCatalog(secret, r.Header.Get("X-Kiko-Catalog-Input"), r.Header.Get("X-Kiko-Chat-Catalog"))
	if err != nil || grant.Family != service.ChatToolCatalogHuman {
		return scope, false, false
	}
	// Advertise the normal schemas without creating an execution scope.
	return scope, true, true
}
