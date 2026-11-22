package service

import (
	"context"
	"errors"
	"time"
)

type FeishuContactRangeProvider interface {
	ExpandContactRange(context.Context, string, string, func() error) error
}

// guardRegistration fences both the saved registration and any current binding.
// Contact range expansion never targets manually connected or replaced apps.
func (s *FeishuSetupService) guardRegistration(ctx context.Context, r *FeishuRegistration, b *ChannelBinding, write func(*FeishuRegistration)) error {
	return s.with(ctx, r.WorkspaceID, r.ActorID, r.AgentID, func(t FeishuSetupTx) error {
		current, err := setupOwned(ctx, t, r.ID, r.ActorID, r.AgentID)
		if err != nil {
			return err
		}
		if current.Version != r.Version || current.State != r.State {
			return Fail("feishu_registration_connection_changed", 409, "扫码登记已变化")
		}
		latest, err := t.Get(ctx, r.AgentID)
		if b == nil {
			if err == nil {
				return Fail("feishu_registration_connection_changed", 409, "渠道已变化")
			}
			if !IsChannelMissing(err) {
				return err
			}
		} else {
			if err != nil {
				return err
			}
			if latest.ID != b.ID || latest.AppID != r.AppID || latest.Revision != b.Revision || latest.Generation != b.Generation || !latest.Enabled {
				return Fail("feishu_registration_connection_changed", 409, "渠道已变化")
			}
		}
		if write != nil {
			write(&current)
			current.UpdatedAt = s.now()
			if err = t.SaveRegistration(ctx, current); err != nil {
				return err
			}
			current.Version++
			*r = current
		}
		return nil
	})
}
func (s *FeishuSetupService) ensureContactRange(ctx context.Context, r *FeishuRegistration, b *ChannelBinding, secret string, d FeishuDiagnostics) error {
	if r.Profile == nil || r.Profile.ContactRange != "all" || r.ContactRange == nil {
		return nil
	}
	if r.ContactRange.Status == "applying" {
		return s.guardRegistration(ctx, r, b, func(v *FeishuRegistration) {
			v.ContactRange = &FeishuContactRange{Status: "uncertain", UpdatedAt: s.now(), ErrorCode: "feishu_contact_range_outcome_unknown"}
		})
	}
	if r.ContactRange.Status != "waiting_permission" && r.ContactRange.Status != "failed" {
		return nil
	}
	granted := false
	for _, p := range d.Permissions {
		if p.Scope == FeishuContactRangeScope && p.Status == "granted" {
			granted = true
		}
	}
	if !granted {
		return nil
	}
	provider, ok := s.Provider.(FeishuContactRangeProvider)
	if !ok {
		return nil
	}
	if err := s.guardRegistration(ctx, r, b, nil); err != nil {
		return err
	}
	dispatched := false
	err := provider.ExpandContactRange(ctx, r.AppID, secret, func() error {
		if err := s.guardRegistration(ctx, r, b, func(v *FeishuRegistration) {
			v.ContactRange = &FeishuContactRange{Status: "applying", UpdatedAt: s.now()}
		}); err != nil {
			return err
		}
		dispatched = true
		return nil
	})
	status, code := "applied", ""
	if err != nil {
		status = "failed"
		code = "feishu_contact_range_update_failed"
		var failure *Error
		if errors.As(err, &failure) {
			code = failure.Code
		}
		if dispatched && code != "feishu_authentication_failed" && code != "feishu_authorization_denied" {
			status = "uncertain"
		}
	}
	// Do not lose the outcome of an in-flight write solely because the browser disconnected.
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	saveErr := s.guardRegistration(finish, r, b, func(v *FeishuRegistration) {
		v.ContactRange = &FeishuContactRange{Status: status, UpdatedAt: s.now(), ErrorCode: code}
	})
	if saveErr != nil {
		return saveErr
	}
	if status == "applied" && b != nil && b.TenantID != "" {
		if store, ok := s.Channels.Store.(interface {
			QueueDirectoryRecheck(context.Context, ChannelBinding) error
		}); ok {
			if err := store.QueueDirectoryRecheck(finish, *b); err != nil {
				return err
			}
		}
	}
	if code == "feishu_authentication_failed" {
		return err
	}
	return nil
}
