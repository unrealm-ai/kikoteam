package controller

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	platform "github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

// Bind the internal credential to one input, connection, assignment and generation.
// A new harness configuration issues a new credential; upstream tokens never leave Backend.
func relayKey(secret, connection, input, deadline string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte("plugin-relay:" + connection + ":" + input + ":" + deadline))
	return hex.EncodeToString(mac.Sum(nil))
}
func signRelay(secret, connection, input string, id service.DelegationIdentity) (string, error) {
	deadline := strconv.FormatInt(time.Now().Add(2*time.Hour).Unix(), 10)
	token, e := service.SignDelegation(relayKey(secret, connection, input, deadline), id)
	return input + "." + deadline + "." + token, e
}

type relayStore interface {
	service.CapabilityStore
	FinishRelayCall(context.Context, service.DelegationIdentity, string, string) error
}
type remoteToolCaller interface {
	CallTool(context.Context, platform.RuntimePlugin, string, json.RawMessage) (json.RawMessage, error)
}
type relayRPC struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
		Protocol  string          `json:"protocolVersion"`
	} `json:"params"`
}

func (s *Server) pluginRelay(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	conn := r.PathValue("connection_id")
	v, input, ok := s.relayIdentity(r, conn)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method != "POST" {
		w.Header().Set("Allow", "POST")
		w.WriteHeader(405)
		return
	}
	store, ok := s.Engines.(relayStore)
	if !ok || s.Runtime == nil {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	var rpc relayRPC
	data, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if e != nil || json.Unmarshal(data, &rpc) != nil || rpc.JSONRPC != "2.0" || len(rpc.ID) > 128 {
		http.Error(w, "invalid request", 400)
		return
	}
	cfg, actor, e := s.Engines.HarnessConfig(r.Context(), v.EngineID, v.SessionID, v.AssignmentID, v.Generation, input)
	if e != nil || cfg.Mode != "task" || cfg.Configuration.Capabilities == nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	plugin := ""
	for _, pin := range cfg.Configuration.Capabilities.Accounts {
		if pin.ID == conn {
			plugin = pin.PluginID
			break
		}
	}
	if plugin == "" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	s.serveRelayRPC(w, r, store, v, cfg, actor, input, conn, plugin, rpc)
}
func (s *Server) relayIdentity(r *http.Request, conn string) (service.DelegationIdentity, string, bool) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	parts := strings.SplitN(token, ".", 3)
	if !ok || len(parts) != 3 || len(s.EngineToken) < 32 || !service.ValidID(conn) || !service.ValidID(parts[0]) || r.Header.Get("Cookie") != "" || r.Header.Get("Origin") != "" {
		return service.DelegationIdentity{}, "", false
	}
	expires, e := strconv.ParseInt(parts[1], 10, 64)
	v, e2 := service.VerifyDelegation(relayKey(s.EngineToken, conn, parts[0], parts[1]), parts[2])
	if e != nil || e2 != nil || time.Now().Unix() >= expires {
		return service.DelegationIdentity{}, "", false
	}
	return v, parts[0], true
}
func (s *Server) serveRelayRPC(w http.ResponseWriter, r *http.Request, store relayStore, v service.DelegationIdentity, cfg service.HarnessConfig, actor, input, conn, plugin string, rpc relayRPC) {
	ctx := cfg.ExecutionContext(r.Context())
	w.Header().Set("Content-Type", "application/json")
	send := func(value any) {
		//nolint:errcheck // The caller may disconnect after the upstream outcome was recorded.
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": value})
	}
	fail := func(code int, message string) {
		//nolint:errcheck // The caller may disconnect after the upstream outcome was recorded.
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "error": map[string]any{"code": code, "message": message}})
	}
	switch rpc.Method {
	case "initialize":
		version := rpc.Params.Protocol
		switch version {
		case "2024-11-05", "2025-03-26", "2025-06-18", "2025-11-25":
		default:
			version = "2025-03-26"
		}
		send(map[string]any{"protocolVersion": version, "serverInfo": map[string]string{"name": "kiko-plugin-connection", "version": "1"}, "capabilities": map[string]any{"tools": map[string]any{}}})
		return
	case "notifications/initialized", "notifications/cancelled":
		w.WriteHeader(202)
		return
	case "ping":
		send(map[string]any{})
		return
	case "tools/list", "tools/call":
	default:
		fail(-32601, "Unsupported MCP method")
		return
	}
	if rpc.Method == "tools/call" {
		_, _, active, err := store.CapabilityContext(ctx, v)
		if err != nil || active != input {
			fail(-32001, "Execution window closed")
			return
		}
	}
	resources, e := s.Runtime.Resolve(ctx, cfg.WorkspaceID, actor, cfg.AgentID, cfg.Configuration.Capabilities.ForPlugin(plugin), cfg.SessionID, rpc.Method == "tools/call")
	if e != nil {
		fail(-32001, "Connection unavailable")
		return
	}
	var current platform.RuntimePlugin
	for _, p := range resources.Plugins {
		if p.AccountID == conn {
			current = p
			break
		}
	}
	if rpc.Method == "tools/list" {
		list := []map[string]any{}
		for _, tool := range current.Tools {
			list = append(list, map[string]any{"name": tool.Name, "description": tool.Description, "inputSchema": tool.InputSchema})
		}
		send(map[string]any{"tools": list})
		return
	}
	if len(rpc.ID) == 0 || len(rpc.Params.Name) == 0 || len(rpc.Params.Name) > 200 {
		fail(-32602, "Invalid tool call")
		return
	}
	reason := "capability_forbidden"
	if slices.Contains(resources.Problems, "plugin_probe_failed:"+plugin) {
		reason = "plugin_probe_failed"
	}
	call := service.CapabilityCall{ID: service.NewID(), NativeID: "relay:" + conn + ":" + string(rpc.ID), InputID: input, Tool: rpc.Params.Name, PluginID: plugin, Status: "denied", ErrorCode: &reason, Actor: actor, Source: current.Source, SourceRevision: current.SourceRevision}
	for _, tool := range current.Tools {
		if tool.Name == call.Tool {
			call.Status = "authorized"
			call.ErrorCode = nil
			call.VersionID = &current.VersionID
			call.AccountID = &current.AccountID
			call.AccountVersion = &current.AccountVersion
			call.SchemaDigest = &tool.SchemaDigest
			break
		}
	}
	caller, ok := s.Runtime.Plugins.Probe.(remoteToolCaller)
	if !ok {
		fail(-32000, "Transport unavailable")
		return
	}
	if e = store.RecordCapabilityCall(ctx, v, call); e != nil {
		fail(-32001, "Execution window closed")
		return
	}
	if reason == "plugin_probe_failed" && call.Status != "authorized" {
		fail(-32000, "Unable to verify upstream tools; retry the read")
		return
	}
	if call.Status != "authorized" {
		fail(-32001, "Tool not authorized")
		return
	}
	args := rpc.Params.Arguments
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	result, e := caller.CallTool(ctx, current, call.Tool, args)
	if e != nil {
		fail(-32000, "Remote operation outcome unavailable")
		return
	}
	var outcome struct {
		Error bool `json:"isError"`
	}
	if json.Unmarshal(result, &outcome) != nil {
		fail(-32000, "Remote operation outcome unavailable")
		return
	}
	status := "succeeded"
	if outcome.Error {
		status = "failed"
	}
	if e = store.FinishRelayCall(ctx, v, call.ID, status); e != nil {
		fail(-32000, "Remote outcome could not be recorded")
		return
	}
	send(result)
}
