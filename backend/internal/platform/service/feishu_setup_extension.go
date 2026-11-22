package service

import (
	"context"
	"errors"
	"time"
)

type FeishuPermissionInspector interface {
	InspectPermissions(context.Context, string, string) (FeishuDiagnostics, error)
}

func extensionPending(r FeishuRegistration) bool {
	if r.Profile == nil {
		return false
	}
	for _, g := range permissionGroups(r.Profile, r.Diagnostics) {
		if !g.Required && g.Status != "granted" {
			return true
		}
	}
	return r.ContactRange != nil && (r.ContactRange.Status == "waiting_permission" || r.ContactRange.Status == "failed")
}
func (s *FeishuSetupService) retryExtension(ctx context.Context, r FeishuRegistration) (FeishuSetupStatus, error) {
	if r.Profile == nil {
		return s.registrationResult(r), nil
	}
	var b ChannelBinding
	claimed := false
	err := s.with(ctx, r.WorkspaceID, r.ActorID, r.AgentID, func(t FeishuSetupTx) error {
		current, e := setupOwned(ctx, t, r.ID, r.ActorID, r.AgentID)
		if e != nil {
			return e
		}
		r = current
		if r.State != "connected" || (r.ExtensionChecking && s.checkActive(r)) {
			return nil
		}
		b, e = t.Get(ctx, r.AgentID)
		if e != nil {
			return e
		}
		if r.ConnectionID == "" || r.ConnectionID != b.ID || b.AppID != r.AppID || !b.Enabled || b.Generation != max(1, r.ConnectionGeneration) {
			r.AutoRetryBlocked = true
			r.ErrorCode = "feishu_registration_connection_changed"
			r.UpdatedAt = s.now()
			return t.SaveRegistration(ctx, r)
		}
		if r.ContactRange != nil && r.ContactRange.Status == "applying" {
			r.ContactRange = &FeishuContactRange{Status: "uncertain", UpdatedAt: s.now(), ErrorCode: "feishu_contact_range_outcome_unknown"}
		}
		r.ExtensionChecking = true
		lease := s.now().Add(90 * time.Second)
		r.CheckLeaseUntil = &lease
		r.AutoRetryBlocked = false
		r.UpdatedAt = s.now()
		r.ErrorCode = ""
		if e = t.SaveRegistration(ctx, r); e != nil {
			return e
		}
		r.Version++
		s.activeChecks.Store(r.ID, true)
		claimed = true
		return nil
	})
	if err != nil {
		return FeishuSetupStatus{}, err
	}
	if !claimed {
		return s.registrationResult(r), nil
	}
	defer s.activeChecks.Delete(r.ID)
	secret, err := s.Channels.open(b)
	var report FeishuDiagnostics
	if err == nil {
		if inspector, ok := s.Provider.(FeishuPermissionInspector); ok {
			report, err = inspector.InspectPermissions(ctx, r.AppID, secret)
		} else {
			report, err = s.Provider.Diagnose(ctx, r.AppID, secret)
		}
	}
	var d FeishuDiagnostics
	if r.Diagnostics != nil {
		d = *r.Diagnostics
	}
	if err == nil {
		d.Permissions = report.Permissions
		d.ScopeStatus = report.ScopeStatus
		err = s.ensureContactRange(ctx, &r, &b, secret, report)
	}
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	fence := s.guardRegistration(finish, &r, &b, nil)
	return s.finish(finish, r, func(_ FeishuSetupTx, current *FeishuRegistration) error {
		current.ExtensionChecking = false
		current.CheckLeaseUntil = nil
		s.deferFeishuCheck(current)
		if fence != nil {
			current.AutoRetryBlocked = true
			current.ErrorCode = "feishu_registration_connection_changed"
			return nil
		}
		if err != nil {
			current.ErrorCode = "feishu_registration_diagnostics_failed"
			d.Permissions = profilePermissions(current.Profile)
			d.ScopeStatus = "unknown"
			current.Diagnostics = &d
			var failure *Error
			if errors.As(err, &failure) {
				current.ErrorCode = failure.Code
				current.AutoRetryBlocked = failure.Code == "feishu_authentication_failed" || failure.Code == "feishu_registration_connection_changed"
			}
			return nil
		}
		current.Diagnostics = &d
		checked := s.now()
		current.PermissionCheckedAt = &checked
		if report.ErrorCode == "feishu_authentication_failed" {
			current.AutoRetryBlocked = true
			current.ErrorCode = report.ErrorCode
		}
		if !extensionPending(*current) {
			current.NextCheckAt = nil
		}
		return nil
	})
}
