package workitems

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	contract "github.com/unrealm-ai/kikoteam/backend/contracts/taskprogress"
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func RegisterProgress(mux *http.ServeMux, store service.ProgressStore, secret string) error {
	var spec sdk.Tool
	if err := json.Unmarshal(contract.Tool, &spec); err != nil {
		return err
	}
	server := sdk.NewServer(&sdk.Implementation{Name: "task_progress", Version: "1.0.0"}, nil)
	sdk.AddTool(server, &spec, func(ctx context.Context, _ *sdk.CallToolRequest, update service.ProgressUpdate) (*sdk.CallToolResult, any, error) {
		scope, ok := ctx.Value(scopeKey{}).(service.WorkScope)
		if !ok {
			return nil, nil, errors.New("unauthenticated")
		}
		current, err := store.UpdateTaskProgress(ctx, scope, update)
		if err == nil {
			return nil, map[string]any{"plan_version": current.PlanVersion, "event_seq": current.Seq, "updated_at": current.UpdatedAt}, nil
		}
		code, message := "dependency_unavailable", "进展暂时无法保存"
		var failure *service.Error
		if errors.As(err, &failure) {
			code, message = failure.Code, failure.Message
		}
		return &sdk.CallToolResult{IsError: true}, map[string]any{"error": map[string]string{"code": code, "message": message}, "current": current}, nil
	})
	mux.Handle("/mcp/task-progress", scopedHandler(server, secret, service.ProgressAudience, service.ProgressInputHeader))
	return nil
}
