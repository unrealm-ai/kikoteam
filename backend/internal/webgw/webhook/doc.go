// Package webhook reserves external HTTP event adapters.
//
// Adapters will verify provider signatures, decode events and map protocol
// responses. They will delegate durable acceptance and business authorization to
// Manager rather than maintain a parallel business workflow. No endpoint or
// acknowledgement policy is implemented yet.
// Manager is implemented in internal/manager. See backend/README.md for layering.
package webhook
