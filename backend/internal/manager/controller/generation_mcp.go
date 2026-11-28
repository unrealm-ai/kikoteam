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
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/videogen"
)

type generationPlugin struct {
	key, handler, name string
	timeout            time.Duration
}

var imagePlugin = generationPlugin{imagegen.Key, "image_generation", "generate_image", 330 * time.Second}
var videoPlugin = generationPlugin{videogen.Key, "video_generation", "generate_video", 70 * time.Second}

func (s *Server) imageMCP(w http.ResponseWriter, r *http.Request) {
	serveGenerationTool(s, w, r, imagePlugin, s.Images != nil, s.Images.Run)
}
func (s *Server) videoMCP(w http.ResponseWriter, r *http.Request) {
	serveGenerationTool(s, w, r, videoPlugin, s.Videos != nil, s.Videos.Run)
}
func (p generationPlugin) tool(cfg manager.HarnessConfig) (platform.MCPTool, string, bool) {
	if cfg.Mode == "task" && !cfg.ReportOnly && !cfg.ConfirmationOnly && cfg.Configuration.Capabilities != nil {
		for _, pin := range cfg.Configuration.Capabilities.DefaultPlugins {
			if pin.Handler != p.handler || pin.PluginID != platform.CatalogID("plugin", p.key) {
				continue
			}
			for _, tool := range pin.Tools {
				if tool.Name == p.name {
					return tool, pin.VersionID, true
				}
			}
		}
	}
	return platform.MCPTool{}, "", false
}
func serveGenerationTool[A, R any](s *Server, w http.ResponseWriter, r *http.Request, plugin generationPlugin, available bool, run func(context.Context, manager.GenerationScope, A) (R, error)) {
	w.Header().Set("Cache-Control", "no-store")
	id, input, ok := s.relayIdentity(r, platform.CatalogID("plugin", plugin.key))
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !available || s.Engines == nil {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	cfg, _, err := s.Engines.HarnessConfig(r.Context(), id.EngineID, id.SessionID, id.AssignmentID, id.Generation, input)
	tool, version, ok := plugin.tool(cfg)
	if err != nil || !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	server := sdk.NewServer(&sdk.Implementation{Name: plugin.handler, Version: version}, nil)
	scope := manager.GenerationScope{DelegationIdentity: id, Input: input, Version: version}
	sdk.AddTool(server, &sdk.Tool{Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema}, func(ctx context.Context, _ *sdk.CallToolRequest, args A) (*sdk.CallToolResult, any, error) {
		result, e := run(ctx, scope, args)
		if e == nil {
			return nil, result, nil
		}
		code, message := "dependency_unavailable", "生成服务暂时不可用，请使用同一 request_key 查询"
		var failure *platform.Error
		if errors.As(e, &failure) {
			code, message = failure.Code, failure.Message
		}
		return &sdk.CallToolResult{IsError: true}, map[string]any{"error": map[string]string{"code": code, "message": message}}, nil
	})
	ctx, cancel := context.WithTimeout(r.Context(), plugin.timeout)
	defer cancel()
	if e := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(plugin.timeout + 5*time.Second)); e != nil && !errors.Is(e, http.ErrNotSupported) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	transport := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, &sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	transport.ServeHTTP(w, r.WithContext(ctx))
}
