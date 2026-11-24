// Package taskdelegation adapts the internal chat MCP to Manager use cases.
package taskdelegation

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	contract "github.com/unrealm-ai/kikoteam/backend/contracts/taskdelegation"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

type scopeKey struct{}

type toolAccess struct {
	scope                               manager.DelegationScope
	schemaOnly, channel, report, stable bool
}

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
			access, ok := ctx.Value(scopeKey{}).(toolAccess)
			if !ok {
				return nil, nil, errors.New("unauthenticated")
			}
			scope := access.scope
			if access.schemaOnly || access.report && (name != "artifact_select" || !access.channel) {
				return &sdk.CallToolResult{IsError: true}, failedToolResult(name, "user_authorization_required", "当前输入没有调用此工具的授权", args, nil), nil
			}
			out, err := d.Call(ctx, scope, name, args)
			if err == nil && name == "create_session" {
				out, err = acceptedCreation(out)
			}
			if err == nil {
				return nil, chatResult(out), nil
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
			return &sdk.CallToolResult{IsError: true}, failedToolResult(name, code, message, args, err), nil
		}
		sdk.AddTool[manager.DelegationArgs, any](server, &spec, call)
		if name == "artifact_select" {
			sdk.AddTool[manager.DelegationArgs, any](report, &spec, call)
		} else {
			sdk.AddTool[manager.DelegationArgs, any](web, &spec, call)
		}
	}
	transport := sdk.NewStreamableHTTPHandler(func(r *http.Request) *sdk.Server {
		a, _ := r.Context().Value(scopeKey{}).(toolAccess)
		if a.report && !a.stable {
			if a.channel {
				return report
			}
			return empty
		}
		if !a.channel {
			return web
		}
		return server
	}, &sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Request-ID", manager.NewID())
		if r.Header.Get("Origin") != "" || r.Header.Get("Cookie") != "" {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		access, err := authorizeToolAccess(ctx, r, d)
		if err != nil {
			http.Error(w, "execution authority unavailable", http.StatusUnauthorized)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		transport.ServeHTTP(w, r.WithContext(context.WithValue(ctx, scopeKey{}, access)))
	}), nil
}

func authorizeToolAccess(ctx context.Context, r *http.Request, d *manager.Delegation) (toolAccess, error) {
	a := toolAccess{stable: r.URL.Query().Get("catalog") == "stable", report: r.URL.Query().Get("mode") == "report"}
	if auth := r.Header.Values("Authorization"); len(auth) != 0 {
		token, bearer := strings.CutPrefix(auth[0], "Bearer ")
		if !bearer || len(auth) != 1 {
			return a, errors.New("unauthenticated")
		}
		var err error
		a.scope, err = d.Authorize(ctx, token)
		a.report = a.report || a.scope.ReportOnly
		a.channel = a.scope.Channel != nil
		return a, err
	}
	if !a.stable {
		return a, errors.New("unauthenticated")
	}
	grant, err := manager.VerifyChatToolCatalog(d.Secret, r.Header.Get("X-Kiko-Catalog-Input"), r.Header.Get("X-Kiko-Chat-Catalog"))
	if err != nil || grant.Family != manager.ChatToolCatalogDelegation {
		return a, errors.New("unauthenticated")
	}
	a.schemaOnly = true
	a.channel = grant.Channel
	return a, nil
}

// Only an immediately created task with durable IDs gets this acceptance receipt.
func acceptedCreation(out any) (any, error) {
	v, _ := out.(map[string]any)
	task, _ := v["task_id"].(*string)
	session, _ := v["session_id"].(string)
	input, _ := v["input_id"].(*string)
	if task == nil || input == nil || !manager.ValidID(*task) || !manager.ValidID(session) || !manager.ValidID(*input) {
		return nil, manager.Fail("dependency_unavailable", 503, "暂未确认任务的受理回执")
	}
	return map[string]any{"accepted": true, "task_id": *task, "session_id": session, "input_id": *input}, nil
}
