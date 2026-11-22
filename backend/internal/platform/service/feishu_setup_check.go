package service

import (
	"context"
	"errors"
	"time"
)

func (s *FeishuSetupService) Retry(ctx context.Context, w, a, agent, id string) (FeishuSetupStatus, error) {
	return s.retry(ctx, w, a, agent, id, false)
}
func (s *FeishuSetupService) retry(ctx context.Context, w, a, agent, id string, automatic bool) (FeishuSetupStatus, error) {
	var r FeishuRegistration
	claimed := false
	extension := false
	e := s.with(ctx, w, a, agent, func(t FeishuSetupTx) error {
		var e error
		r, e = setupOwned(ctx, t, id, a, agent)
		if e != nil {
			return e
		}
		if automatic && (r.AutoRetryBlocked || (r.NextCheckAt != nil && s.now().Before(*r.NextCheckAt))) {
			return nil
		}
		if r.State == "connected" {
			extension = true
			return nil
		}
		if r.State == "checking" && s.checkActive(r) {
			return nil
		}
		if r.State == "cancelled" {
			return Fail("feishu_registration_cancelled", 409, "已取消，请使用手动凭据连接已有应用")
		}
		if len(r.SecretCipher) == 0 || r.AppID == "" {
			return Fail("feishu_registration_credentials_required", 409, "尚未取得应用凭据")
		}
		existing, lookup := t.Get(ctx, agent)
		if lookup == nil {
			r.State = "needs_attention"
			r.ErrorCode = "feishu_registration_connection_changed"
			r.AutoRetryBlocked = true
			r.UpdatedAt = s.now()
			if existing.AppID == r.AppID {
				r.ErrorCode = "feishu_registration_already_configured"
			}
			return t.SaveRegistration(ctx, r)
		}
		if !IsChannelMissing(lookup) {
			return lookup
		}
		r.State = "checking"
		lease := s.now().Add(90 * time.Second)
		r.CheckLeaseUntil = &lease
		r.AutoRetryBlocked = false
		r.ErrorCode = ""
		r.UpdatedAt = s.now()
		if e = t.SaveRegistration(ctx, r); e != nil {
			return e
		}
		r.Version++
		s.activeChecks.Store(r.ID, true)
		claimed = true
		return nil
	})
	if e != nil {
		return FeishuSetupStatus{}, e
	}
	if extension {
		return s.retryExtension(ctx, r)
	}
	if !claimed {
		return s.registrationResult(r), nil
	}
	defer s.activeChecks.Delete(r.ID)
	secret, e := s.openRegistration(r, "secret", r.SecretCipher)
	var d FeishuDiagnostics
	if e == nil {
		d, e = s.Provider.Diagnose(ctx, r.AppID, secret)
		if e == nil {
			d = registrationDiagnostics(r.Profile, d)
			e = s.ensureContactRange(ctx, &r, nil, secret, d)
		}
	}
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return s.finish(finish, r, func(t FeishuSetupTx, current *FeishuRegistration) error {
		current.State = "needs_attention"
		current.CheckLeaseUntil = nil
		defer func() {
			if current.State == "needs_attention" {
				s.deferFeishuCheck(current)
			}
		}()
		if e != nil {
			current.ErrorCode = "feishu_registration_diagnostics_failed"
			var failure *Error
			if errors.As(e, &failure) {
				current.ErrorCode = failure.Code
				current.AutoRetryBlocked = failure.Code == "feishu_registration_connection_changed" || failure.Code == "feishu_authentication_failed"
			}
			return nil
		}
		current.Diagnostics = &d
		if !d.Ready {
			current.ErrorCode = d.ErrorCode
			current.AutoRetryBlocked = d.CredentialStatus == "failed" || d.ErrorCode == "feishu_authentication_failed"
			return nil
		}
		existing, lookup := t.Get(finish, agent)
		if lookup == nil {
			current.ErrorCode = "feishu_registration_connection_changed"
			current.AutoRetryBlocked = true
			if existing.AppID == current.AppID {
				current.ErrorCode = "feishu_registration_already_configured"
			}
			return nil
		}
		if !IsChannelMissing(lookup) {
			return lookup
		}
		b := ChannelBinding{ID: NewID(), WorkspaceID: w, AgentID: agent, Provider: "feishu", IdentityEpoch: 1, Generation: 1, RulesVersion: 1, Revision: 1, Enabled: true, AppID: current.AppID, TenantID: d.TenantID, BotID: d.BotID, HasSecret: true, Trigger: ChannelTrigger{Mention: true, Keywords: []string{}, Regexes: []string{}}, History: ChannelHistory{Limit: 100}, Access: ChannelAccess{Mode: "all", AllowIDs: []string{}, DenyIDs: []string{}, AllowChatIDs: []string{}, DenyChatIDs: []string{}}, Applied: ChannelApplied{Status: "pending"}}
		if err := s.Channels.prepareRegistrationBinding(finish, t, &b, secret); err != nil {
			return err
		}
		if saveErr := t.Save(finish, b, true); saveErr != nil {
			var conflict *Error
			if errors.As(saveErr, &conflict) && conflict.Code == "channel_app_binding_ambiguous" {
				current.ErrorCode = conflict.Code
				current.AutoRetryBlocked = true
				return nil
			}
			return saveErr
		}
		current.ConnectionID = b.ID
		current.ConnectionGeneration = b.Generation
		current.State = "connected"
		current.ErrorCode = ""
		current.SecretCipher = nil
		current.NextCheckAt = nil
		current.CheckAttempts = 0
		if extensionPending(*current) {
			s.deferFeishuCheck(current)
		}
		return nil
	})
}
func (s *FeishuSetupService) Diagnose(ctx context.Context, w, a, agent string) (FeishuDiagnostics, error) {
	var b ChannelBinding
	e := s.with(ctx, w, a, agent, func(t FeishuSetupTx) error { var e error; b, e = t.Get(ctx, agent); return e })
	if e != nil {
		return FeishuDiagnostics{}, e
	}
	d, e := s.diagnoseBinding(ctx, b)
	if e != nil {
		return d, e
	}
	e = s.with(ctx, w, a, agent, func(t FeishuSetupTx) error {
		latest, e := t.Get(ctx, agent)
		if e != nil {
			return e
		}
		if latest.ID != b.ID || latest.Revision != b.Revision || latest.Generation != b.Generation {
			return Fail("channel_version_conflict", 409, "诊断期间渠道配置已变化")
		}
		return nil
	})
	if e != nil {
		return FeishuDiagnostics{}, e
	}
	return d, nil
}

func (s *FeishuSetupService) deferFeishuCheck(r *FeishuRegistration) {
	delays := []time.Duration{5, 10, 20, 30, 60}
	delay := delays[min(max(r.CheckAttempts, 0), len(delays)-1)] * time.Second
	next := s.now().Add(delay)
	r.NextCheckAt = &next
	r.CheckAttempts++
}
