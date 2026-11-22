package service

import "context"

// WebGroupExecution is trusted Manager provenance, not an HTTP parameter.
// It is always checked against the real conversation and current membership.
type WebGroupExecution struct {
	ConversationID string
	InputID        string
}
type webGroupExecutionKey struct{}

func WithWebGroupExecution(ctx context.Context, v WebGroupExecution) context.Context {
	return context.WithValue(ctx, webGroupExecutionKey{}, v)
}
func WebGroupExecutionFrom(ctx context.Context) (WebGroupExecution, bool) {
	v, ok := ctx.Value(webGroupExecutionKey{}).(WebGroupExecution)
	return v, ok
}

type webGroupExecutionTx interface {
	AuthorizeWebGroupExecution(context.Context, WebGroupExecution, string, string, bool) error
}

func authorizeWebGroupRuntime(ctx context.Context, tx any, g WebGroupExecution, actor, employee string, web func() error) error {
	if g.ConversationID == "" || g.InputID == "" || actor == "" {
		return Fail("web_group_execution_forbidden", 403, "群运行身份无效")
	}
	if _, conflict := ChannelExecutionFrom(ctx); conflict {
		return Fail("web_group_execution_forbidden", 403, "群运行身份冲突")
	}
	if err := web(); err != nil {
		return err
	}
	verifier, ok := tx.(webGroupExecutionTx)
	if !ok {
		return Fail("web_group_execution_forbidden", 403, "群运行身份不可验证")
	}
	return verifier.AuthorizeWebGroupExecution(ctx, g, actor, employee, true)
}
