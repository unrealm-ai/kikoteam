package service

import "context"

// TriggerExecution is authenticated Manager provenance, never a human member.
// Every operation revalidates the binding, immutable event and current Input.
type TriggerExecution struct {
	TriggerID string
	Version   int64
	EventID   string
	InputID   string
}
type triggerExecutionKey struct{}

func WithTriggerExecution(ctx context.Context, v TriggerExecution) context.Context {
	return context.WithValue(ctx, triggerExecutionKey{}, v)
}
func TriggerExecutionFrom(ctx context.Context) (TriggerExecution, bool) {
	v, ok := ctx.Value(triggerExecutionKey{}).(TriggerExecution)
	return v, ok
}

type triggerExecutionTx interface {
	AuthorizeTriggerExecution(context.Context, TriggerExecution, string, string, bool) error
}

func authorizeTriggerRuntime(ctx context.Context, tx any, g TriggerExecution, actor, employee string) error {
	_, channel := ChannelExecutionFrom(ctx)
	_, group := WebGroupExecutionFrom(ctx)
	_, space := TaskSpaceExecutionFrom(ctx)
	if channel || group || space || actor != "" || g.TriggerID == "" || g.EventID == "" || g.InputID == "" || g.Version < 1 {
		return Fail("trigger_execution_forbidden", 403, "触发任务运行身份无效")
	}
	verifier, ok := tx.(triggerExecutionTx)
	if !ok {
		return Fail("trigger_execution_forbidden", 403, "触发任务身份不可验证")
	}
	return verifier.AuthorizeTriggerExecution(ctx, g, actor, employee, true)
}
