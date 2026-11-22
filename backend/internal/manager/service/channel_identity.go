package service

import "context"

// ChannelActor is server-created evidence, never decoded from a public Web
// request. It is reconstructed from durable inbox/active Input authorization.
type ChannelActor struct {
	InboxID             string
	ConfirmationOnly    bool
	NeedsContextReceipt bool
	ID                  string
	Generation          int64
}
type channelActorKey struct{}

func WithChannelActor(ctx context.Context, actor ChannelActor) context.Context {
	return context.WithValue(ctx, channelActorKey{}, actor)
}
func ChannelActorFrom(ctx context.Context) (ChannelActor, bool) {
	a, ok := ctx.Value(channelActorKey{}).(ChannelActor)
	return a, ok
}
