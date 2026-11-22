// Package taskdelegation adapts the internal chat MCP to Manager use cases.
package taskdelegation

import (
	"context"
	"encoding/json"
	"errors"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	contract "github.com/unrealm-ai/kikoteam/backend/contracts/taskdelegation"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

type scopeKey struct{}

func Handler(d *manager.Delegation, log *slog.Logger) (http.Handler, error) {
	var specs []sdk.Tool
	if err := json.Unmarshal(contract.Tools, &specs); err != nil {
		return nil, err
	}
	web := sdk.NewServer(&sdk.Implementation{Name: "task_delegation", Version: "1.0.0"}, nil)
	report := sdk.NewServer(&sdk.Implementation{Name: "task_report", Version: "1.0.0"}, nil)
	empty := sdk.NewServer(&sdk.Implementation{Name: "task_report", Version: "1.0.0"}, nil)
	server := sdk.NewServer(&sdk.Implementation{Name: "task_delegation", Version: "1.0.0"}, nil)
	for _, spec := range specs {
		name := strings.TrimPrefix(spec.Name, "task_delegation_")
		call := func(ctx context.Context, _ *sdk.CallToolRequest, args manager.DelegationArgs) (*sdk.CallToolResult, any, error) {
			scope, ok := ctx.Value(scopeKey{}).(manager.DelegationScope)
			if !ok {
				return nil, nil, errors.New("unauthenticated")
			}
			out, err := d.Call(ctx, scope, name, args)
			if err == nil {
				return nil, out, nil
			}
			code, message := "dependency_unavailable", "委派服务暂时不可用"
			var e *manager.Error
			if errors.As(err, &e) {
				code = e.Code
				message = e.Message
			}
			if log != nil {
				log.Warn("Delegation operation failed", "event", "delegation.failed", "source_session_id", scope.Identity.SessionID, "operation", name, "error_code", code)
			}
			return &sdk.CallToolResult{IsError: true}, map[string]any{"error": map[string]string{"code": code, "message": message}}, nil
		}
		sdk.AddTool[manager.DelegationArgs, any](server, &spec, call)
		if name == "artifact_select" {
			sdk.AddTool[manager.DelegationArgs, any](report, &spec, call)
		} else {
			sdk.AddTool[manager.DelegationArgs, any](web, &spec, call)
		}
	}
	transport := sdk.NewStreamableHTTPHandler(func(r *http.Request) *sdk.Server {
		scope, _ := r.Context().Value(scopeKey{}).(manager.DelegationScope)
		if scope.ReportOnly || r.URL.Query().Get("mode") == "report" {
			if scope.Channel != nil {
				return report
			}
			return empty
		}
		if scope.Channel == nil {
			return web
		}
		return server
	}, &sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Request-ID", manager.NewID())
		if r.Header.Get("Origin") != "" || r.Header.Get("Cookie") != "" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		scope, err := d.Authorize(ctx, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if err != nil {
			http.Error(w, "execution authority unavailable", http.StatusUnauthorized)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		transport.ServeHTTP(w, r.WithContext(context.WithValue(ctx, scopeKey{}, scope)))
	}), nil
}
