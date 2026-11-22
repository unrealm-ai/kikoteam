// Package mcp reserves external business MCP adapters for personal agents and integrations.
// Future handlers must verify external identity and authorization before delegating
// durable acceptance and control to Manager; they must not own a task state machine.
// No external MCP endpoint or login flow is implemented yet. This is not internal
// task delegation (manager/mcp/taskdelegation), Sandbox builtin MCP or outbound plugins.
package mcp
