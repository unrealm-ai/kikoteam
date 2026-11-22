package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

type capabilityRequest struct {
	AccountID string          `json:"account_id"`
	Phase     string          `json:"phase"`
	CallID    string          `json:"call_id"`
	NativeID  string          `json:"native_tool_call_id"`
	PluginID  string          `json:"plugin_id"`
	Tool      string          `json:"tool_name"`
	Schema    json.RawMessage `json:"input_schema"`
	Status    string          `json:"status"`
}

func (s *Server) capabilityCredential(r *http.Request) string {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	v, err := service.VerifyCapability(s.EngineToken, token)
	if err != nil || v.SessionID != r.PathValue("session_id") {
		return "invalid-scoped-credential"
	}
	return token
}

func (s *Server) capabilityCall(r *http.Request) (any, int, error) {
	v, err := service.VerifyCapability(s.EngineToken, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if err != nil {
		return nil, 401, err
	}
	store, ok := s.Engines.(service.CapabilityStore)
	if !ok || s.Runtime == nil {
		return nil, 503, service.Fail("dependency_unavailable", 503, "能力执行尚未配置")
	}
	var req capabilityRequest
	if err = decode(r, &req); err != nil {
		return nil, 400, err
	}
	if req.Phase == "finish" {
		if !service.ValidID(req.CallID) || req.Status != "succeeded" && req.Status != "failed" {
			return nil, 400, service.Fail("invalid_argument", 400, "调用结果无效")
		}
		err = store.FinishCapabilityCall(r.Context(), v, req.CallID, req.Status)
		return map[string]bool{"recorded": err == nil}, 200, err
	}
	if (req.Phase != "authorize" && req.Phase != "check") || !service.ValidID(req.PluginID) || strings.HasPrefix(req.NativeID, "relay:") || len(req.NativeID) < 1 || len(req.NativeID) > 256 || len(req.Tool) < 1 || len(req.Tool) > 200 || len(req.Schema) == 0 {
		return nil, 400, service.Fail("invalid_argument", 400, "工具授权请求无效")
	}
	cfg, actor, input, err := store.CapabilityContext(r.Context(), v)
	if err != nil {
		return nil, 0, err
	}
	ctx := cfg.ExecutionContext(r.Context())
	// A credentialed relay verifies the upstream catalog again immediately
	// before forwarding the operation. Its local preflight only needs the
	// current account, package and pinned schema.
	resources, err := s.Runtime.Resolve(ctx, cfg.WorkspaceID, actor, cfg.AgentID, cfg.Configuration.Capabilities.ForPlugin(req.PluginID), cfg.SessionID, req.Phase != "check")
	if err != nil {
		return nil, 0, err
	}
	call := service.CapabilityCall{ID: service.NewID(), InputID: input, NativeID: req.NativeID, PluginID: req.PluginID, Tool: req.Tool, Status: "denied"}
	reason := "capability_forbidden"
	call.ErrorCode = &reason
	for _, p := range resources.Plugins {
		if req.Phase == "check" && len(p.Headers) == 0 {
			continue
		}
		if p.PluginID != req.PluginID || req.AccountID != "" && p.AccountID != req.AccountID {
			continue
		}
		for _, tool := range p.Tools {
			if tool.Name != req.Tool || !matchesNativeSchema(req.Schema, tool.InputSchema) {
				continue
			}
			call.Status = "authorized"
			call.ErrorCode = nil
			call.VersionID = &p.VersionID
			call.AccountID = &p.AccountID
			call.AccountVersion = &p.AccountVersion
			call.SchemaDigest = &tool.SchemaDigest
			call.SourceRevision = p.SourceRevision
			call.Source = p.Source
			call.Actor = actor
		}
	}
	if call.Status == "authorized" && req.Phase == "check" {
		return map[string]bool{"allowed": true}, 200, nil
	}
	if err = store.RecordCapabilityCall(ctx, v, call); err != nil {
		return nil, 0, err
	}
	return map[string]any{"allowed": call.Status == "authorized", "call_id": call.ID}, 200, nil
}

func matchesNativeSchema(actual, expected json.RawMessage) bool {
	var a any
	var e map[string]any
	if json.Unmarshal(actual, &a) != nil || json.Unmarshal(expected, &e) != nil || e == nil {
		return false
	}
	if _, ok := e["type"]; !ok {
		e["type"] = "object"
	}
	if _, ok := e["properties"]; !ok {
		e["properties"] = map[string]any{}
	}
	hash := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		sum := sha256.Sum256(b)
		return hex.EncodeToString(sum[:])
	}
	return hash(a) == hash(e)
}
