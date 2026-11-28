package controller

import (
	"context"
	"errors"
	"net/http"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/imagegen"
	platform "github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func imageTool(cfg manager.HarnessConfig) (platform.MCPTool, string, bool) {
	if cfg.Mode == "task" && !cfg.ReportOnly && !cfg.ConfirmationOnly && cfg.Configuration.Capabilities != nil {
		for _, p := range cfg.Configuration.Capabilities.DefaultPlugins {
			if p.Handler != "image_generation" || p.PluginID != platform.CatalogID("plugin", imagegen.Key) {
				continue
			}
			for _, tool := range p.Tools {
				if tool.Name == "generate_image" {
					return tool, p.VersionID, true
				}
			}
		}
	}
	return platform.MCPTool{}, "", false
}
func (s *Server) imageMCP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	id, input, ok := s.relayIdentity(r, platform.CatalogID("plugin", imagegen.Key))
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if s.Images == nil || s.Engines == nil {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	cfg, _, err := s.Engines.HarnessConfig(r.Context(), id.EngineID, id.SessionID, id.AssignmentID, id.Generation, input)
	tool, version, ok := imageTool(cfg)
	if err != nil || !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	server := sdk.NewServer(&sdk.Implementation{Name: "image_generation", Version: version}, nil)
	scope := imagegen.Scope{DelegationIdentity: id, Input: input, Version: version}
	sdk.AddTool(server, &sdk.Tool{Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema}, func(ctx context.Context, _ *sdk.CallToolRequest, args imagegen.Arguments) (*sdk.CallToolResult, any, error) {
		result, e := s.Images.Run(ctx, scope, args)
		if e == nil {
			return nil, result, nil
		}
		code, message := "dependency_unavailable", "出图暂时不可用，请使用同一 request_key 查询"
		var failure *platform.Error
		if errors.As(e, &failure) {
			code, message = failure.Code, failure.Message
		}
		return &sdk.CallToolResult{IsError: true}, map[string]any{"error": map[string]string{"code": code, "message": message}}, nil
	})
	ctx, cancel := context.WithTimeout(r.Context(), 330*time.Second)
	defer cancel()
	if e := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(335 * time.Second)); e != nil && !errors.Is(e, http.ErrNotSupported) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	transport := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, &sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	transport.ServeHTTP(w, r.WithContext(ctx))
}
