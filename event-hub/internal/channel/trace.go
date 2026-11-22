package channel

import (
	"context"
	"log/slog"
	"strings"
	"unicode"
)

// Trace values are correlation identifiers only, never payloads, credentials,
// lease/query tokens, callback URLs or raw errors. Bound untrusted wire IDs.
func traceID(value string) string {
	if len(value) > 256 || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return "invalid_identifier"
	}
	return value
}
func (b *Backend) trace(ctx context.Context, stage string, fields ...any) {
	logger := slog.Default()
	if b != nil && b.Logger != nil {
		logger = b.Logger
	}
	logger.InfoContext(ctx, "Channel pipeline", append([]any{"event", stage}, fields...)...)
}
func (b *Backend) traceInbound(ctx context.Context, stage, provider string, v Inbound, outcome string) {
	if provider == "" {
		provider = v.LogProvider
	}
	fields := []any{"provider", provider, "binding_id", traceID(v.BindingID), "connection_generation", v.Generation, "provider_event_id", traceID(v.EventID), "provider_message_id", traceID(v.MessageID), "outcome", outcome}
	if v.CreatedAtMS > 0 && provider != "wecom" {
		fields = append(fields, "provider_created_at_ms", v.CreatedAtMS)
	}
	if v.LogCallbackID != "" {
		fields = append(fields, "callback_id", traceID(v.LogCallbackID))
	}
	b.trace(ctx, stage, fields...)
}
func (b *Backend) traceDelivery(ctx context.Context, stage string, d Delivery, outcome string) {
	kind := b.deliveryKind()
	b.trace(ctx, stage, "delivery_kind", kind, "delivery_id", traceID(d.ID), "provider", traceID(d.Provider), "binding_id", traceID(d.BindingID), "connection_generation", d.Generation, "provider_request_id", traceID(d.ProviderRequestID), "outcome", outcome)
}
