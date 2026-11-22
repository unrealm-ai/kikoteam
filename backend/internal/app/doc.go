// Package app is the Backend process composition boundary.
//
// Startup assembles Platform and Manager repositories, authenticated HTTP,
// runtime dispatch and optional Web assets in one process. Webhook/MCP adapters
// reuse Manager use cases as they are implemented. PostgreSQL migrations remain
// owned by Event Hub. See backend/README.md for layering rules.
package app
