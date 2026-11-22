// Package taskspacehistory exposes Manager's TaskSpace summary tools.
package taskspacehistory

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	contract "github.com/unrealm-ai/kikoteam/backend/contracts/taskspacehistory"
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

type identityKey struct{}

func Register(mux *http.ServeMux, history *service.TaskSpaceHistory, secret string, log *slog.Logger) error {
	var specs []sdk.Tool
	if err := json.Unmarshal(contract.Tools, &specs); err != nil {
		return err
	}
	server := sdk.NewServer(&sdk.Implementation{Name: "task_space_history", Version: "2.0.0"}, nil)
	for _, spec := range specs {
		switch spec.Name {
		case "task_space_history_list_tasks":
			addTool(server, &spec, history.List, log)
		case "task_space_history_get_task":
			addTool(server, &spec, history.Get, log)
		case "task_space_history_save_summary":
			addTool(server, &spec, history.Save, log)
		default:
			return errors.New("unknown TaskSpace history MCP tool: " + spec.Name)
		}
	}
	transport := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, &sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	mux.HandleFunc("/mcp/task-space-history", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Request-ID", service.NewID())
		token, bearer := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !bearer || r.Header.Get("Origin") != "" || r.Header.Get("Cookie") != "" {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		id, err := service.VerifyTaskSpaceHistory(secret, r.Header.Get("X-Kiko-Task-Space-Input"), token)
		if err != nil {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		transport.ServeHTTP(w, r.WithContext(context.WithValue(ctx, identityKey{}, id)))
	})
	return nil
}

func addTool[Args, Result any](server *sdk.Server, spec *sdk.Tool, call func(context.Context, service.TaskSpaceHistoryIdentity, Args) (Result, error), log *slog.Logger) {
	sdk.AddTool(server, spec, func(ctx context.Context, _ *sdk.CallToolRequest, args Args) (*sdk.CallToolResult, any, error) {
		id, ok := ctx.Value(identityKey{}).(service.TaskSpaceHistoryIdentity)
		if !ok {
			return nil, nil, errors.New("unauthenticated")
		}
		out, err := call(ctx, id, args)
		if err == nil {
			return nil, out, nil
		}
		code, message := "dependency_unavailable", "任务历史暂时不可用"
		var failure *service.Error
		if errors.As(err, &failure) {
			code, message = failure.Code, failure.Message
		} else if log != nil {
			log.Error("TaskSpace history read failed", "event", "task_space_history.failed", "source_session_id", id.SessionID, "tool_name", spec.Name, "error", err)
		}
		return &sdk.CallToolResult{IsError: true}, map[string]any{"error": map[string]string{"code": code, "message": message}}, nil
	})
}
