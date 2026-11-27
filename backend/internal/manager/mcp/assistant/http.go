// Package assistant binds an internal MCP transport to the actual running input.
package assistant

import (
	"context"
	"net/http"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	ms "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	ps "github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type Engine interface {
	HarnessConfig(context.Context, string, string, string, int64, string) (ms.HarnessConfig, string, error)
}

type Allow func(context.Context, string, string, string, string, *ps.ExecutionCapabilities) (bool, error)

func scope(secret, input, family string) string {
	return "kiko-assistant:" + family + ":" + input + ":" + secret
}

func Token(secret, input, family string, id ms.DelegationIdentity) (string, error) {
	return ms.SignDelegation(scope(secret, input, family), id)
}

type configKey struct{}

func Config(ctx context.Context) ms.HarnessConfig {
	v, _ := ctx.Value(configKey{}).(ms.HarnessConfig)
	return v
}

func Mount(mux *http.ServeMux, engine Engine, allow Allow, secret, family, mode string, server *sdk.Server) {
	transport := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, &sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	mux.HandleFunc("/mcp/assistant/"+family, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		input := r.Header.Get("X-Kiko-Assistant-Input")
		token, bearer := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if len(secret) < 32 || !ms.ValidID(input) || !bearer || r.Header.Get("Origin") != "" || r.Header.Get("Cookie") != "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		id, err := ms.VerifyDelegation(scope(secret, input, family), token)
		if err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		cfg, actor, err := engine.HarnessConfig(ctx, id.EngineID, id.SessionID, id.AssignmentID, id.Generation, input)
		if err != nil || cfg.Mode != mode || cfg.ReportOnly || cfg.ConfirmationOnly || mode == "task" && cfg.Configuration.Capabilities == nil {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		ok, err := allow(cfg.ExecutionContext(ctx), cfg.WorkspaceID, actor, cfg.AgentID, family, cfg.Configuration.Capabilities)
		if err != nil || !ok {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		transport.ServeHTTP(w, r.WithContext(context.WithValue(ctx, configKey{}, cfg)))
	})
}
