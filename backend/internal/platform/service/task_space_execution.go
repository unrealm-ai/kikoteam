package service

import "context"

// TaskSpaceExecution is trusted Manager provenance. Context alone grants no access.
type TaskSpaceExecution struct{ TaskSpaceID, InputID string }
type taskSpaceExecutionKey struct{}

func WithTaskSpaceExecution(ctx context.Context, v TaskSpaceExecution) context.Context {
	return context.WithValue(ctx, taskSpaceExecutionKey{}, v)
}
func TaskSpaceExecutionFrom(ctx context.Context) (TaskSpaceExecution, bool) {
	v, ok := ctx.Value(taskSpaceExecutionKey{}).(TaskSpaceExecution)
	return v, ok
}

type taskSpaceExecutionTx interface {
	AuthorizeTaskSpaceExecution(context.Context, TaskSpaceExecution, string, string, bool) error
}

func authorizeTaskSpaceRuntime(ctx context.Context, tx any, g TaskSpaceExecution, actor, employee string, web func() error) error {
	_, channel := ChannelExecutionFrom(ctx)
	_, group := WebGroupExecutionFrom(ctx)
	if channel || group || g.TaskSpaceID == "" || g.InputID == "" || actor == "" {
		return Fail("task_space_execution_forbidden", 403, "任务空间运行身份无效")
	}
	if err := web(); err != nil {
		return err
	}
	verifier, ok := tx.(taskSpaceExecutionTx)
	if !ok {
		return Fail("task_space_execution_forbidden", 403, "任务空间运行身份不可验证")
	}
	return verifier.AuthorizeTaskSpaceExecution(ctx, g, actor, employee, true)
}
