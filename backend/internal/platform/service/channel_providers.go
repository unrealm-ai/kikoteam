package service

import (
	"context"
	"reflect"
	"regexp"
)

func ValidChannelProvider(provider string) bool {
	return provider == "feishu" || provider == "dingtalk" || provider == "wecom"
}
func channelProvider(provider string) string {
	if provider == "" {
		return "feishu"
	}
	return provider
}
func validChannelAppID(provider, id string) bool {
	if provider == "feishu" {
		return regexp.MustCompile(`^cli_[A-Za-z0-9_-]{1,120}$`).MatchString(id)
	}
	return regexp.MustCompile(`^[A-Za-z0-9_.:@-]{1,200}$`).MatchString(id)
}
func validateProviderCapabilities(provider string, r *ChannelPut) error {
	if provider == "feishu" {
		return nil
	}
	r.Trigger.Mention = true // Non-Feishu group delivery is provider-addressed, not a configurable text trigger.
	if len(r.Trigger.Keywords)+len(r.Trigger.Regexes) > 0 || provider == "dingtalk" && r.History.Enabled {
		return Fail("channel_capability_unsupported", 400, "此渠道不支持关键词触发或该历史能力")
	}
	if provider == "wecom" && (len(r.Access.AllowDepartmentIDs)+len(r.Access.DenyDepartmentIDs)+len(r.Access.AllowDepartmentRules)+len(r.Access.DenyDepartmentRules) > 0) {
		return Fail("channel_capability_unsupported", 400, "此渠道部门授权尚未接入")
	}
	return nil
}
func getChannelProvider(ctx context.Context, t ChannelTx, employee, provider string) (ChannelBinding, error) {
	if scoped, ok := t.(interface {
		GetProvider(context.Context, string, string) (ChannelBinding, error)
	}); ok {
		return scoped.GetProvider(ctx, employee, provider)
	}
	if provider == "feishu" {
		return t.Get(ctx, employee)
	}
	return ChannelBinding{}, Fail("channel_provider_unavailable", 503, "渠道存储尚不可用")
}
func (s *ChannelService) Get(ctx context.Context, w, a, employee string) (ChannelBinding, error) {
	return s.GetProvider(ctx, w, a, employee, "feishu")
}
func (s *ChannelService) Put(ctx context.Context, w, a, employee string, r ChannelPut) (ChannelBinding, error) {
	return s.PutProvider(ctx, w, a, employee, "feishu", r)
}
func validateChannel(r *ChannelPut) error { return validateChannelFor(r, "feishu") }

func SetChannelIdentityPresentation(b *ChannelBinding) {
	b.IdentityConfirmed = b.TenantID != "" && b.BotID != "" && (b.Provider == "feishu" || b.IdentityVerifiedGeneration == b.Generation)
	b.UserNamespace = "user_id"
	b.TenantNamespace = "corp_id"
	if b.Provider == "feishu" {
		b.UserNamespace = "open_id"
		b.TenantNamespace = "tenant_key"
	}
	if b.Provider == "wecom" {
		b.TenantNamespace = "app_id"
	}
}

func applyChannelUsageProjection(ctx context.Context, t ChannelTx, previous ChannelBinding, out *ChannelBinding, create bool) error {
	if projector, ok := t.(interface {
		ApplyUsageProjection(context.Context, *ChannelBinding) error
	}); ok {
		if err := projector.ApplyUsageProjection(ctx, out); err != nil {
			return err
		}
	}
	if !create && (!reflect.DeepEqual(previous.Trigger, out.Trigger) || previous.History != out.History) {
		out.RulesVersion++
	}
	return nil
}

func (s *ChannelService) prepareRegistrationBinding(ctx context.Context, t ChannelTx, b *ChannelBinding, secret string) error {
	if err := applyChannelUsageProjection(ctx, t, ChannelBinding{}, b, true); err != nil {
		return err
	}
	ciphertext, err := s.seal(*b, secret)
	b.Ciphertext = ciphertext
	return err
}
