package workitems

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	contract "github.com/unrealm-ai/kikoteam/backend/contracts/workitems"
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

type scopeKey struct{}

func Register(mux *http.ServeMux, store service.WorkToolStore, secret string) error {
	var specs map[string]*sdk.Tool
	if err := json.Unmarshal(contract.Tools, &specs); err != nil {
		return err
	}
	for _, role := range []string{"ordinary", "assignee", "review"} {
		server := sdk.NewServer(&sdk.Implementation{Name: "work_items", Version: "1.0.0"}, nil)
		addTool(server, specs["artifact_publish"], store, "artifact_publish")
		addTool(server, specs["work_item_list_handoff_employees"], store, "employees")
		if role != "ordinary" {
			addTool(server, specs["work_item_get_current"], store, "get")
			addTool(server, specs["work_item_read_file"], store, "file")
		}
		if role == "review" {
			addTool(server, specs["work_item_accept"], store, "accept")
		}
		mux.Handle("/mcp/work-items/"+role, handler(server, secret, role))
	}
	return nil
}

func addTool(server *sdk.Server, spec *sdk.Tool, store service.WorkToolStore, op string) {
	sdk.AddTool(server, spec, func(ctx context.Context, _ *sdk.CallToolRequest, args service.WorkArguments) (*sdk.CallToolResult, any, error) {
		scope, ok := ctx.Value(scopeKey{}).(service.WorkScope)
		if !ok {
			return nil, nil, errors.New("unauthenticated")
		}
		out, err := store.WorkTool(ctx, scope, op, args)
		if err == nil {
			return nil, out, nil
		}
		code, message := "dependency_unavailable", "事项服务暂不可用"
		var failure *service.Error
		if errors.As(err, &failure) {
			code, message = failure.Code, failure.Message
		}
		return &sdk.CallToolResult{IsError: true}, map[string]any{"error": map[string]string{"code": code, "message": message}}, nil
	})
}

func handler(server *sdk.Server, secret, role string) http.Handler {
	transport := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, &sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Request-ID", service.NewID())
		token, bearer := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		input := r.Header.Get(service.WorkInputHeader)
		id, err := service.VerifyWorkTool(secret, "mcp-"+role, input, token)
		if !bearer || err != nil || r.Header.Get("Origin") != "" || r.Header.Get("Cookie") != "" {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		scope := service.WorkScope{Identity: id, InputID: input}
		transport.ServeHTTP(w, r.WithContext(context.WithValue(ctx, scopeKey{}, scope)))
	})
}
