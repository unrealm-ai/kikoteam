// Package runtime executes one serial worker per assigned Session.
package runtime

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/unrealm-ai/kikoteam/agent-engine/internal/pi"
	"github.com/unrealm-ai/kikoteam/agent-engine/internal/repository"
)

func NewID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}

type toolState struct {
	ID, Status                        string
	Name, Title                       *string
	SourceKind                        string
	SourceName                        *string
	Number                            int
	Input, Output                     any
	InputKnown, OutputKnown, Terminal bool
	StartedAt, FinishedAt             *time.Time
}
type eventWriter struct {
	workBoundary    string
	workUnresolved  bool
	humanRequest    string
	humanUnresolved bool
	ctx             context.Context
	store           *repository.Store
	work            repository.Work
	turn            repository.Turn
	text            strings.Builder
	tools           map[string]*toolState
	err             error
	answer          string
}

func (e *eventWriter) update(u pi.Update) {
	if e.err != nil {
		return
	}
	kind, _ := u["type"].(string)
	if kind == "message_update" {
		if delta, ok := u["assistantMessageEvent"].(map[string]any); ok && delta["type"] == "text_delta" {
			if text, ok := delta["delta"].(string); ok {
				e.text.WriteString(text)
			}
		}
		return
	}
	if kind == "message_end" {
		e.endMessage(u)
		return
	}
	if kind != "tool_execution_start" && kind != "tool_execution_update" && kind != "tool_execution_end" {
		return
	}
	e.flush("completed")
	if e.err != nil {
		return
	}
	id, _ := u["toolCallId"].(string)
	if id == "" {
		return
	}
	state := e.tools[id]
	if state == nil {
		state = &toolState{ID: NewID(), Number: len(e.tools) + 1, Status: "pending", SourceKind: "unknown"}
		e.tools[id] = state
	}
	if state.Terminal {
		return
	}
	if name, ok := u["toolName"].(string); ok {
		state.Name, state.Title = &name, &name
	}
	state.Status = "running"
	if kind == "tool_execution_end" {
		state.Status = "completed"
		if failed, _ := u["isError"].(bool); failed {
			state.Status = "failed"
		}
	}
	if input, ok := u["args"]; ok {
		state.Input = input
		state.InputKnown = true
	}
	if output, ok := u["result"]; ok {
		state.Output = output
		state.OutputKnown = true
	} else if output, ok := u["partialResult"]; ok {
		state.Output = output
		state.OutputKnown = true
	}
	if state.Name != nil && (e.privateHumanTool(*state.Name, u, state) || e.privateWorkTool(*state.Name, u, state)) {
		e.publishTool(state)
		return
	}
	// Actual MCP origin is supplied by Pi's structured result metadata.
	if raw, ok := state.Output.(map[string]any); ok {
		if details, ok := raw["details"].(map[string]any); ok {
			tool, t := details["tool"].(string)
			server, s := details["server"].(string)
			if t && s {
				state.Name = &tool
				state.SourceKind = "mcp"
				state.SourceName = &server
			}
		}
	}
	e.publishTool(state)
}
func (e *eventWriter) privateHumanTool(name string, update pi.Update, state *toolState) bool {
	name, mcp := strings.CutPrefix(name, "mcp__"+humanMCPServer+"__")
	switch name {
	case "ask_human":
		raw, _ := update["result"].(map[string]any)
		details, _ := raw["details"].(map[string]any)
		if id, ok := details["human_request_id"].(string); ok && id != "" && state.Status == "completed" {
			e.humanRequest = id
		}
		if unresolved, _ := details["human_request_unresolved"].(bool); unresolved {
			e.humanUnresolved = true
		}
	case "list_human_recipients", "list_human_confirmation_scopes", "list_pending_human_confirmations", "submit_human_confirmation", "get_human_confirmation_receipt":
	default:
		return false
	}
	if mcp {
		server := humanMCPServer
		state.Name, state.Title = &name, &name
		state.SourceKind, state.SourceName = "mcp", &server
	}
	state.Input, state.Output = nil, nil
	state.InputKnown, state.OutputKnown = false, false
	return true
}

func (e *eventWriter) privateWorkTool(name string, update pi.Update, state *toolState) bool {
	server := workMCPServer
	name, mcp := strings.CutPrefix(name, "mcp__"+server+"__")
	if tool, ok := strings.CutPrefix(name, "mcp__task_delegation__"); ok && (tool == "work_item_get_current" || tool == "work_item_amend_requirements") {
		name, mcp, server = tool, true, "task_delegation"
	}
	switch name {
	case "work_item_handoff", "work_item_submit_result":
		raw, _ := update["result"].(map[string]any)
		details, _ := raw["details"].(map[string]any)
		if id, ok := details["work_boundary_id"].(string); ok && id != "" && state.Status == "completed" {
			e.workBoundary = id
		}
		if unresolved, _ := details["work_boundary_unresolved"].(bool); unresolved {
			e.workUnresolved = true
		}
	case "work_item_list_handoff_employees", "work_item_get_current", "work_item_read_file", "work_item_accept", "work_item_amend_requirements":
	default:
		return false
	}
	if mcp {
		state.Name, state.Title = &name, &name
		state.SourceKind, state.SourceName = "mcp", &server
	}
	state.Input, state.Output = nil, nil
	state.InputKnown, state.OutputKnown = false, false
	return true
}

func (e *eventWriter) endMessage(u pi.Update) {
	message, ok := u["message"].(map[string]any)
	if !ok || message["role"] != "assistant" {
		return
	}
	// The finalized native message is authoritative; replacing the buffer avoids
	// duplicating streamed text and also handles messages without text deltas.
	e.text.Reset()
	if blocks, ok := message["content"].([]any); ok {
		for _, raw := range blocks {
			if block, ok := raw.(map[string]any); ok && block["type"] == "text" {
				if text, ok := block["text"].(string); ok {
					e.text.WriteString(text)
				}
			}
		}
	}
	status := "completed"
	if stop, _ := message["stopReason"].(string); stop == "error" || stop == "aborted" || stop == "length" {
		status = "interrupted"
	}
	e.flush(status)
}
func (e *eventWriter) publishTool(state *toolState) {
	now := time.Now().UTC()
	if state.Status == "running" && state.StartedAt == nil {
		state.StartedAt = &now
	}
	kind := "agent.tool_use"
	var failure any
	if state.Status != "pending" && state.Status != "running" {
		kind = "agent.tool_result"
		state.Terminal = true
		if state.Status != "unknown" {
			state.FinishedAt = &now
		}
	}
	if state.Status == "failed" || state.Status == "unknown" {
		failure = map[string]string{"code": "tool_" + state.Status, "message": "上游工具未提供成功的确定结果"}
	}
	inState, outState := "unavailable", "unavailable"
	if state.InputKnown {
		inState = "available"
	}
	if state.OutputKnown {
		outState = "available"
	}
	fields := map[string]any{"tool_use_id": state.ID, "call_no": strconv.Itoa(state.Number), "name": state.Name, "title": state.Title, "source": map[string]any{"kind": state.SourceKind, "name": state.SourceName}, "status": state.Status, "input": state.Input, "output": state.Output, "input_state": inState, "output_state": outState, "summary": nil, "error": failure, "started_at": state.StartedAt, "finished_at": state.FinishedAt}
	data, err := json.Marshal(fields)
	if err != nil {
		e.err = err
		return
	}
	if len(data) > 8<<20 {
		fields["input"] = nil
		fields["output"] = nil
		fields["input_state"] = "too_large"
		fields["output_state"] = "too_large"
	}
	e.err = e.store.Event(e.ctx, e.work, e.turn, kind, fields)
}
func (e *eventWriter) finish() {
	states := make([]*toolState, 0, len(e.tools))
	for _, s := range e.tools {
		if !s.Terminal {
			states = append(states, s)
		}
	}
	sort.Slice(states, func(i, j int) bool { return states[i].Number < states[j].Number })
	for _, s := range states {
		if e.err != nil {
			return
		}
		s.Status = "unknown"
		e.publishTool(s)
	}
}
func (e *eventWriter) flush(status string) {
	if e.err != nil || e.text.Len() == 0 {
		return
	}
	text := e.text.String()
	e.text.Reset()
	e.answer = text
	e.err = e.store.Event(e.ctx, e.work, e.turn, "agent.message", map[string]any{"message_id": NewID(), "content": []any{map[string]string{"type": "text", "text": text}}, "status": status})
}
