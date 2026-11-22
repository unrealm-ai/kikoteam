// Package channel owns elected channel transport workers.
// Credentials and rules are fetched only from authenticated Backend interfaces.
// Business authorization and durable inbox/outbox state remain in Manager.
// Feishu is implemented; other providers and the scheduler are not enabled.
package channel
