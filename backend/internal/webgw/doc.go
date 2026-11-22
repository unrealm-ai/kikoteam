// Package webgw reserves external inbound protocol adapters in the Backend process.
// Webhook and public business MCP adapters will authenticate their external callers
// and reuse Manager use cases. No external endpoint is implemented here yet.
// Internal task delegation lives in manager/mcp/taskdelegation instead.
// Package separation does not provide listener or network isolation.
package webgw
