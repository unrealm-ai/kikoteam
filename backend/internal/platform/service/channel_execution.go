package service

import "context"

type webEmployeeKey struct{}

func WithWebEmployee(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, webEmployeeKey{}, id)
}

type webExecutionTx interface {
	AuthorizeWebEmployee(context.Context, string, string) error
}

// ChannelExecution carries trusted Manager provenance, never browser identity.
// Context alone grants nothing: every runtime store operation revalidates it.
type ChannelExecution struct {
	ConfirmationOnly bool
	PrincipalID      string
	Generation       int64
	ConversationID   string
	InputID          string
}
type channelExecutionKey struct{}

func WithChannelExecution(ctx context.Context, actor ChannelExecution) context.Context {
	return context.WithValue(ctx, channelExecutionKey{}, actor)
}
func ChannelExecutionFrom(ctx context.Context) (ChannelExecution, bool) {
	v, ok := ctx.Value(channelExecutionKey{}).(ChannelExecution)
	return v, ok
}

type channelExecutionTx interface {
	AuthorizeChannelExecution(context.Context, ChannelExecution, string, bool) error
}

func authorizeRuntime(ctx context.Context, tx any, actor, employee string, web func() error) error {
	if g, ok := WorkExecutionFrom(ctx); ok {
		return authorizeWorkRuntime(ctx, tx, g, actor, employee)
	}
	if g, ok := TriggerExecutionFrom(ctx); ok {
		return authorizeTriggerRuntime(ctx, tx, g, actor, employee)
	}
	if g, ok := TaskSpaceExecutionFrom(ctx); ok {
		return authorizeTaskSpaceRuntime(ctx, tx, g, actor, employee, web)
	}
	if g, group := WebGroupExecutionFrom(ctx); group {
		return authorizeWebGroupRuntime(ctx, tx, g, actor, employee, web)
	}
	c, channel := ChannelExecutionFrom(ctx)
	if !channel {
		if e := web(); e != nil {
			return e
		}
		if id, _ := ctx.Value(webEmployeeKey{}).(string); id != "" {
			if employee != "" && employee != id {
				return Fail("employee_access_denied", 403, "员工运行身份不匹配")
			}
			employee = id
		}
		if employee != "" {
			if verifier, ok := tx.(webExecutionTx); ok {
				return verifier.AuthorizeWebEmployee(ctx, actor, employee)
			}
			return Fail("employee_access_denied", 403, "员工运行身份不可验证")
		}
		return nil
	}
	if c.PrincipalID != actor || c.PrincipalID == "" || c.ConversationID == "" || c.InputID == "" || c.Generation < 1 {
		return Fail("channel_execution_forbidden", 403, "渠道运行身份无效")
	}
	verifier, ok := tx.(channelExecutionTx)
	if !ok {
		return Fail("channel_execution_forbidden", 403, "渠道运行身份不可验证")
	}
	return verifier.AuthorizeChannelExecution(ctx, c, employee, true)
}

// PackageAllowsExecution never interprets a channel principal as a Web member.
// Until channel-specific package scopes exist, only unqualified inheritance is
// usable; Web allow/deny lists conservatively exclude all channel execution.
func PackageAllowsExecution(ctx context.Context, p CapabilityPackage, actor string) bool {
	if g, work := WorkExecutionFrom(ctx); work {
		return actor == "" && p.Enabled && p.WorkScope != nil && p.WorkScope.Allows(g.SourceAgentID)
	}
	if g, ok := TriggerExecutionFrom(ctx); ok {
		return actor == "" && PackageAllowsTrigger(p, g.TriggerID)
	}
	if p.ConversationScope != nil {
		return false
	} // Requires trusted repository scene evaluation.
	if _, channel := ChannelExecutionFrom(ctx); channel {
		return p.Enabled && p.Scope.Mode == "inherit_with_blacklist" && len(p.Scope.AllowMemberIDs) == 0 && len(p.Scope.DenyMemberIDs) == 0 && len(p.Scope.Allow) == 0 && len(p.Scope.Deny) == 0
	}
	return PackageAllowsMember(p, actor, true)
}

func (s *PackageService) runtimePackage(ctx context.Context, w, actor, employee, id string) (CapabilityPackage, error) {
	var out CapabilityPackage
	err := s.Store.WithWorkspace(ctx, w, func(t PackageTx) error {
		if err := authorizeRuntime(ctx, t, actor, employee, func() error { return packageAuthorize(ctx, t, actor, false) }); err != nil {
			return err
		}
		var err error
		out, err = t.Get(ctx, employee, id)
		return err
	})
	return out, err
}
