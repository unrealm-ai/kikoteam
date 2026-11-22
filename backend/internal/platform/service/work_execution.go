package service

import "context"

// WorkExecution identifies an employee-to-employee assignment. Runtime
// stores verify its current Input before granting access to employee resources.
type WorkExecution struct{ AssignmentID, SourceAgentID, InputID string }
type workExecutionKey struct{}

func WithWorkExecution(ctx context.Context, v WorkExecution) context.Context {
	return context.WithValue(ctx, workExecutionKey{}, v)
}

func WorkExecutionFrom(ctx context.Context) (WorkExecution, bool) {
	v, ok := ctx.Value(workExecutionKey{}).(WorkExecution)
	return v, ok
}

type workExecutionTx interface {
	AuthorizeWorkExecution(context.Context, WorkExecution, string) error
}

func authorizeWorkRuntime(ctx context.Context, tx any, g WorkExecution, actor, employee string) error {
	_, channel := ChannelExecutionFrom(ctx)
	_, group := WebGroupExecutionFrom(ctx)
	_, space := TaskSpaceExecutionFrom(ctx)
	_, trigger := TriggerExecutionFrom(ctx)
	check, ok := tx.(workExecutionTx)
	if !ok || actor != "" || channel || group || space || trigger || g.InputID == "" || g.AssignmentID == "" || g.SourceAgentID == "" {
		return Fail("work_execution_forbidden", 403, "事项任务运行身份无效")
	}
	return check.AuthorizeWorkExecution(ctx, g, employee)
}

// Legacy unrestricted packages can participate in handoff. Packages with
// conversation rules or human restrictions are scoped to those entry points.
func PackageAllowsWork(p CapabilityPackage) bool {
	s := p.Scope
	return p.Enabled && p.ConversationScope == nil && s.Mode == "inherit_with_blacklist" &&
		len(s.AllowMemberIDs) == 0 && len(s.DenyMemberIDs) == 0 && len(s.Allow) == 0 && len(s.Deny) == 0
}
