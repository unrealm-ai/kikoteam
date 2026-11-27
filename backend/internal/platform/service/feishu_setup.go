package service

import (
	"context"
	"crypto/rand"
	"errors"
	"regexp"
	"time"
)

var setupKey = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func (s *FeishuSetupService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
func (s *FeishuSetupService) with(ctx context.Context, w, a, agent string, fn func(FeishuSetupTx) error) error {
	if !agentUUID(w) || !agentUUID(agent) {
		return Fail("invalid_channel", 400, "员工或工作区标识无效")
	}
	return s.Store.WithSetup(ctx, w, func(t FeishuSetupTx) error {
		if e := AuthorizeEmployee(ctx, t, a, agent); e != nil {
			return e
		}
		if e := t.AgentExists(ctx, agent); e != nil {
			return e
		}
		return fn(t)
	})
}
func setupOwned(ctx context.Context, t FeishuSetupTx, id, a, agent string) (FeishuRegistration, error) {
	if !agentUUID(id) {
		return FeishuRegistration{}, Fail("invalid_feishu_registration", 400, "扫码登记标识无效")
	}
	r, e := t.Registration(ctx, id)
	if e != nil {
		return r, e
	}
	if r.ActorID != a || r.AgentID != agent {
		return r, Fail("feishu_registration_not_found", 404, "扫码登记不存在")
	}
	return r, nil
}
func (s *FeishuSetupService) Status(ctx context.Context, w, a, agent string) (FeishuSetupStatus, error) {
	out := FeishuSetupStatus{Permissions: FeishuPermissions()}
	e := s.with(ctx, w, a, agent, func(t FeishuSetupTx) error {
		var e error
		out.Registration, e = t.LatestRegistration(ctx, agent, a)
		if e != nil {
			return e
		}
		if r := out.Registration; r != nil && setupPending(r.State) && !s.registrationActive(r.ID) {
			current, e := setupOwned(ctx, t, r.ID, a, agent)
			if e != nil {
				return e
			}
			if setupPending(current.State) {
				current.State = "interrupted"
				current.ErrorCode = "feishu_registration_outcome_unknown"
				current.DeviceCipher = nil
				current.VerificationURL = ""
				current.UpdatedAt = s.now()
				if e = t.SaveRegistration(ctx, current); e != nil {
					return e
				}
				current.Version++
				*r = current
			}
		}
		if r := out.Registration; r != nil && r.State == "connected" {
			binding, lookup := t.Get(ctx, agent)
			if lookup != nil && !IsChannelMissing(lookup) {
				return lookup
			}
			if lookup != nil || binding.AppID != r.AppID {
				r.State = "needs_attention"
				r.ErrorCode = "feishu_registration_connection_changed"
			}
		}
		return nil
	})
	if r := out.Registration; r != nil {
		s.publicState(r)
		out.Permissions = profilePermissions(r.Profile)
	}
	return out, e
}
func (s *FeishuSetupService) publicState(r *FeishuRegistration) {
	r.PermissionGroups = permissionGroups(r.Profile, r.Diagnostics)
	if setupPending(r.State) && !s.registrationActive(r.ID) {
		r.State = "interrupted"
		r.ErrorCode = "feishu_registration_outcome_unknown"
	}
	if (r.State == "starting" || r.State == "awaiting_confirmation" || r.State == "polling") && !s.now().Before(r.ExpiresAt) {
		r.State = "expired"
		r.ErrorCode = "feishu_registration_expired"
	}
	if (r.State == "starting" || r.State == "polling") && s.now().After(r.UpdatedAt.Add(90*time.Second)) {
		r.State = "interrupted"
		r.ErrorCode = "feishu_registration_outcome_unknown"
	}
	if r.State == "checking" && !s.checkActive(*r) {
		r.State = "needs_attention"
		r.ErrorCode = "feishu_registration_check_interrupted"
	}
	if r.ExtensionChecking && !s.checkActive(*r) {
		r.ExtensionChecking = false
	}
	r.AutoCheck = &FeishuAutoCheck{Eligible: !r.AutoRetryBlocked && len(r.SecretCipher) > 0 && (r.State == "credentials_saved" || r.State == "needs_attention"), InProgress: r.State == "checking" || r.ExtensionChecking, NextCheckAt: r.NextCheckAt}
	if r.State == "connected" {
		r.AutoCheck.Eligible = !r.AutoRetryBlocked && extensionPending(*r)
	}
	if r.State != "awaiting_confirmation" && r.State != "polling" {
		r.VerificationURL = ""
	}
}
func (s *FeishuSetupService) sealRegistration(r FeishuRegistration, purpose, value string) ([]byte, error) {
	a, e := s.Channels.aead()
	if e != nil {
		return nil, e
	}
	nonce := make([]byte, a.NonceSize())
	if _, e = rand.Read(nonce); e != nil {
		return nil, e
	}
	return a.Seal(nonce, nonce, []byte(value), setupAAD(r, purpose)), nil
}
func (s *FeishuSetupService) openRegistration(r FeishuRegistration, purpose string, data []byte) (string, error) {
	a, e := s.Channels.aead()
	if e != nil {
		return "", e
	}
	if len(data) < a.NonceSize() {
		return "", errors.New("invalid setup ciphertext")
	}
	v, e := a.Open(nil, data[:a.NonceSize()], data[a.NonceSize():], setupAAD(r, purpose))
	return string(v), e
}
func setupAAD(r FeishuRegistration, purpose string) []byte {
	return []byte("kiko:feishu-setup:" + r.WorkspaceID + ":" + r.AgentID + ":" + r.ActorID + ":" + r.ID + ":" + purpose)
}
func (s *FeishuSetupService) Start(ctx context.Context, w, a, agent, key string) (FeishuSetupStatus, error) {
	return s.StartWithContactRange(ctx, w, a, agent, key, true)
}

func (s *FeishuSetupService) StartWithContactRange(ctx context.Context, w, a, agent, key string, allContacts bool) (FeishuSetupStatus, error) {
	return s.StartRegistration(ctx, w, a, agent, key, &allContacts, "")
}

func (s *FeishuSetupService) StartRegistration(ctx context.Context, w, a, agent, key string, allContacts *bool, replaceID string) (FeishuSetupStatus, error) {
	if !setupKey.MatchString(key) {
		return FeishuSetupStatus{}, Fail("invalid_idempotency_key", 400, "需要有效的 Idempotency-Key")
	}
	if s.Provider == nil {
		return FeishuSetupStatus{}, Fail("feishu_setup_unavailable", 503, "扫码连接不可用")
	}
	if _, e := s.Channels.aead(); e != nil {
		return FeishuSetupStatus{}, e
	}
	var r FeishuRegistration
	var name, description string
	fresh := false
	e := s.with(ctx, w, a, agent, func(t FeishuSetupTx) error {
		previous, e := t.RegistrationByKey(ctx, agent, key)
		if e != nil {
			return e
		}
		if previous != nil {
			if previous.ActorID != a {
				return Fail("feishu_registration_not_found", 404, "扫码登记不存在")
			}
			if previous.ReplacesRegistrationID != replaceID {
				return Fail("feishu_registration_changed", 409, "该请求标识已用于其他扫码登记")
			}
			r = *previous
			return nil
		}
		_, e = t.Get(ctx, agent)
		if !IsChannelMissing(e) {
			if e != nil {
				return e
			}
			return Fail("feishu_registration_already_connected", 409, "已有渠道，请使用现有凭据诊断")
		}
		name, description, e = t.AgentPreset(ctx, agent)
		if e != nil {
			return e
		}
		r = FeishuRegistration{Profile: FeishuProfile(), ID: NewID(), WorkspaceID: w, AgentID: agent, ActorID: a, OperationKey: key, Version: 1, State: "starting", ExpiresAt: s.now().Add(10 * time.Minute), UpdatedAt: s.now(), Interval: 5}
		includeRange := true
		if replaceID != "" {
			old, err := setupOwned(ctx, t, replaceID, a, agent)
			if err != nil {
				return err
			}
			failedCheck := old.State == "needs_attention" || (old.State == "checking" && !s.checkActive(old))
			if !failedCheck || !old.CredentialSaved || len(old.SecretCipher) == 0 || old.ConnectionID != "" {
				return Fail("feishu_registration_changed", 409, "接入状态已变化，请重新读取后再授权")
			}
			includeRange = old.Profile != nil && old.Profile.ContactRange == "all"
			old.State, old.ErrorCode = "cancelled", "feishu_registration_replaced"
			old.SecretCipher, old.DeviceCipher, old.VerificationURL = nil, nil, ""
			old.CredentialSaved, old.AutoRetryBlocked = false, true
			old.NextCheckAt, old.CheckLeaseUntil, old.UpdatedAt = nil, nil, s.now()
			if err = t.SaveRegistration(ctx, old); err != nil {
				return err
			}
			r.ReplacesRegistrationID = replaceID
		}
		if allContacts != nil {
			includeRange = *allContacts
		}
		if includeRange {
			r.ContactRange = &FeishuContactRange{Status: "waiting_permission", UpdatedAt: s.now()}
		} else {
			r.Profile.ContactRange = ""
			// The range-management group is only requested when selected.
			r.Profile.Groups = r.Profile.Groups[:len(r.Profile.Groups)-1]
		}
		if e = t.InsertRegistration(ctx, r); e != nil {
			return e
		}
		fresh = true
		s.activeRegistrations.Store(r.ID, r)
		return nil
	})
	if e != nil {
		return FeishuSetupStatus{}, e
	}
	if !fresh {
		return s.registrationResult(r), nil
	}
	var begin FeishuBegin
	if configured, ok := s.Provider.(interface {
		BeginWithPermissions(context.Context, string, string, []FeishuPermission) (FeishuBegin, error)
	}); ok {
		begin, e = configured.BeginWithPermissions(ctx, name, description, profilePermissions(r.Profile))
	} else {
		begin, e = s.Provider.Begin(ctx, name, description)
	}
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	result, writeErr := s.finish(finish, r, func(_ FeishuSetupTx, current *FeishuRegistration) error {
		if e != nil {
			current.State = "failed"
			current.ErrorCode = "feishu_registration_begin_failed"
			return nil
		}
		ciphertext, err := s.sealRegistration(*current, "device", begin.DeviceCode)
		if err != nil {
			return err
		}
		current.DeviceCipher = ciphertext
		current.VerificationURL = begin.URL
		current.ExpiresAt = minTime(current.ExpiresAt, s.now().Add(time.Duration(begin.ExpiresIn)*time.Second))
		current.Interval = begin.Interval
		current.NextPollAt = s.now().Add(time.Duration(begin.Interval) * time.Second)
		current.State = "awaiting_confirmation"
		return nil
	})
	return result, writeErr
}
func (s *FeishuSetupService) registrationResult(r FeishuRegistration) FeishuSetupStatus {
	s.publicState(&r)
	return FeishuSetupStatus{Registration: &r, Permissions: profilePermissions(r.Profile)}
}

// finish rechecks actor authorization and the exact operation version after I/O.
// A cancellation, manual binding, or another request cannot be overwritten.
func (s *FeishuSetupService) finish(ctx context.Context, r FeishuRegistration, fn func(FeishuSetupTx, *FeishuRegistration) error) (FeishuSetupStatus, error) {
	var out FeishuRegistration
	e := s.with(ctx, r.WorkspaceID, r.ActorID, r.AgentID, func(t FeishuSetupTx) error {
		current, e := setupOwned(ctx, t, r.ID, r.ActorID, r.AgentID)
		if e != nil {
			return e
		}
		out = current
		if current.Version != r.Version || current.State != r.State {
			return nil
		}
		if e = fn(t, &current); e != nil {
			return e
		}
		current.UpdatedAt = s.now()
		if e = t.SaveRegistration(ctx, current); e != nil {
			return e
		}
		current.Version++
		out = current
		return nil
	})
	return s.registrationResult(out), e
}
func (s *FeishuSetupService) Poll(ctx context.Context, w, a, agent, id string) (FeishuSetupStatus, error) {
	var r FeishuRegistration
	claimed := false
	e := s.with(ctx, w, a, agent, func(t FeishuSetupTx) error {
		var e error
		r, e = setupOwned(ctx, t, id, a, agent)
		if e != nil {
			return e
		}
		if r.State != "awaiting_confirmation" {
			return nil
		}
		if !s.registrationActive(r.ID) {
			r.State = "interrupted"
			r.ErrorCode = "feishu_registration_outcome_unknown"
			r.DeviceCipher = nil
			r.VerificationURL = ""
			r.UpdatedAt = s.now()
			return t.SaveRegistration(ctx, r)
		}
		if !s.now().Before(r.ExpiresAt) {
			r.State = "expired"
			r.ErrorCode = "feishu_registration_expired"
			r.DeviceCipher = nil
			r.UpdatedAt = s.now()
			return t.SaveRegistration(ctx, r)
		}
		if s.now().Before(r.NextPollAt) {
			return nil
		}
		r.State = "polling"
		r.UpdatedAt = s.now()
		if e = t.SaveRegistration(ctx, r); e != nil {
			return e
		}
		r.Version++
		claimed = true
		return nil
	})
	if e != nil {
		return FeishuSetupStatus{}, e
	}
	if !claimed {
		return s.registrationResult(r), nil
	}
	device, e := s.openRegistration(r, "device", r.DeviceCipher)
	var result FeishuPoll
	if e == nil {
		result, e = s.Provider.Poll(ctx, device)
	}
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return s.finish(finish, r, func(_ FeishuSetupTx, current *FeishuRegistration) error {
		if e != nil {
			current.State = "interrupted"
			current.ErrorCode = "feishu_registration_outcome_unknown"
			current.DeviceCipher = nil
			return nil
		}
		return s.applyPoll(current, result)
	})
}
func (s *FeishuSetupService) applyPoll(r *FeishuRegistration, p FeishuPoll) error {
	switch p.State {
	case "authorization_pending", "slow_down":
		if p.State == "slow_down" {
			r.Interval += 5
		}
		r.NextPollAt = s.now().Add(time.Duration(r.Interval) * time.Second)
		r.State = "awaiting_confirmation"
	case "access_denied":
		r.State = "denied"
		r.ErrorCode = "feishu_registration_denied"
	case "expired_token":
		r.State = "expired"
		r.ErrorCode = "feishu_registration_expired"
	case "credentials_saved":
		ciphertext, e := s.sealRegistration(*r, "secret", p.Secret)
		if e != nil {
			return e
		}
		r.SecretCipher = ciphertext
		r.AppID = p.AppID
		r.CredentialSaved = true
		r.State = "credentials_saved"
	default:
		r.State = "failed"
		r.ErrorCode = "feishu_registration_unsupported"
	}
	if r.State != "awaiting_confirmation" {
		r.DeviceCipher = nil
		r.VerificationURL = ""
	}
	return nil
}
func (s *FeishuSetupService) Cancel(ctx context.Context, w, a, agent, id string) (FeishuSetupStatus, error) {
	var r FeishuRegistration
	e := s.with(ctx, w, a, agent, func(t FeishuSetupTx) error {
		var e error
		r, e = setupOwned(ctx, t, id, a, agent)
		if e != nil {
			return e
		}
		// Once credentials are received, stopping the browser wait must not
		// discard or freeze a recoverable application. It does not revoke it.
		if r.CredentialSaved || len(r.SecretCipher) > 0 || r.State == "connected" {
			return nil
		}
		r.State = "cancelled"
		r.ErrorCode = "feishu_registration_cancelled"
		r.DeviceCipher = nil
		r.VerificationURL = ""
		r.UpdatedAt = s.now()
		return t.SaveRegistration(ctx, r)
	})
	return s.registrationResult(r), e
}
