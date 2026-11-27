package service

import (
	"context"
	"crypto/rand"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/config"
)

var dingSetupKey = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func (s *DingTalkSetupService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
func (s *DingTalkSetupService) with(ctx context.Context, w, a, agent string, fn func(DingTalkSetupTx) error) error {
	if !agentUUID(w) || !agentUUID(agent) {
		return Fail("invalid_channel", 400, "员工或工作区标识无效")
	}
	return s.Store.WithSetup(ctx, w, func(t DingTalkSetupTx) error {
		if e := AuthorizeEmployee(ctx, t, a, agent); e != nil {
			return e
		}
		if e := t.AgentExists(ctx, agent); e != nil {
			return e
		}
		return fn(t)
	})
}
func dingSetupOwned(ctx context.Context, t DingTalkSetupTx, id, a, agent string) (DingTalkRegistration, error) {
	if !agentUUID(id) {
		return DingTalkRegistration{}, Fail("invalid_dingtalk_registration", 400, "扫码登记标识无效")
	}
	r, e := t.Registration(ctx, id)
	if e != nil {
		return r, e
	}
	if r.ActorID != a || r.AgentID != agent {
		return r, Fail("dingtalk_registration_not_found", 404, "扫码登记不存在")
	}
	return r, nil
}
func (s *DingTalkSetupService) Status(ctx context.Context, w, a, agent string) (DingTalkSetupStatus, error) {
	out := DingTalkSetupStatus{}
	e := s.with(ctx, w, a, agent, func(t DingTalkSetupTx) error {
		var e error
		out.Registration, e = t.LatestRegistration(ctx, agent, a)
		if e != nil {
			return e
		}
		if r := out.Registration; r != nil && r.State == "consumed" {
			binding, lookup := getChannelProvider(ctx, t, agent, "dingtalk")
			if lookup != nil && !IsChannelMissing(lookup) {
				return lookup
			}
			if lookup != nil || binding.AppID != r.AppID {
				r.State = "failed"
				r.ErrorCode = "dingtalk_registration_connection_changed"
			}
		}
		return nil
	})
	if r := out.Registration; r != nil {
		s.publicState(r)
	}
	return out, e
}
func (s *DingTalkSetupService) publicState(r *DingTalkRegistration) {
	if (r.State == "starting" || r.State == "awaiting_confirmation" || r.State == "polling" || r.State == "ready") && !s.now().Before(r.ExpiresAt) {
		r.State = "expired"
		r.ErrorCode = "dingtalk_registration_expired"
	}
	if (r.State == "polling" || r.State == "checking") && s.now().After(r.UpdatedAt.Add(90*time.Second)) {
		r.State = "interrupted"
		r.ErrorCode = "dingtalk_registration_outcome_unknown"
	}
	if r.State != "awaiting_confirmation" && r.State != "polling" {
		r.VerificationURL = ""
	}
}
func (s *DingTalkSetupService) sealRegistration(r DingTalkRegistration, purpose, value string) ([]byte, error) {
	a, e := s.Channels.aead()
	if e != nil {
		return nil, e
	}
	nonce := make([]byte, a.NonceSize())
	if _, e = rand.Read(nonce); e != nil {
		return nil, e
	}
	return a.Seal(nonce, nonce, []byte(value), dingSetupAAD(r, purpose)), nil
}
func (s *DingTalkSetupService) openRegistration(r DingTalkRegistration, purpose string, data []byte) (string, error) {
	a, e := s.Channels.aead()
	if e != nil {
		return "", e
	}
	if len(data) < a.NonceSize() {
		return "", errors.New("invalid setup ciphertext")
	}
	v, e := a.Open(nil, data[:a.NonceSize()], data[a.NonceSize():], dingSetupAAD(r, purpose))
	return string(v), e
}
func dingSetupAAD(r DingTalkRegistration, purpose string) []byte {
	return []byte("kiko:dingtalk-setup:" + r.WorkspaceID + ":" + r.AgentID + ":" + r.ActorID + ":" + r.ID + ":" + purpose)
}
func (s *DingTalkSetupService) Start(ctx context.Context, w, a, agent, key string) (DingTalkSetupStatus, error) {
	if !dingSetupKey.MatchString(key) {
		return DingTalkSetupStatus{}, Fail("invalid_idempotency_key", 400, "需要有效的 Idempotency-Key")
	}
	if s.Provider == nil {
		return DingTalkSetupStatus{}, Fail("dingtalk_setup_unavailable", 503, "扫码连接不可用")
	}
	if _, e := s.Channels.aead(); e != nil {
		return DingTalkSetupStatus{}, e
	}
	var r DingTalkRegistration
	fresh := false
	e := s.with(ctx, w, a, agent, func(t DingTalkSetupTx) error {
		previous, e := t.RegistrationByKey(ctx, agent, key)
		if e != nil {
			return e
		}
		if previous != nil {
			if previous.ActorID != a {
				return Fail("dingtalk_registration_not_found", 404, "扫码登记不存在")
			}
			r = *previous
			return nil
		}
		_, e = getChannelProvider(ctx, t, agent, "dingtalk")
		if !IsChannelMissing(e) {
			if e != nil {
				return e
			}
			return Fail("dingtalk_registration_already_connected", 409, "已有渠道，请使用现有凭据诊断")
		}
		r = DingTalkRegistration{ID: NewID(), WorkspaceID: w, AgentID: agent, ActorID: a, OperationKey: key, Version: 1, State: "starting", ExpiresAt: s.now().Add(10 * time.Minute), UpdatedAt: s.now(), Interval: 5}
		if e = t.InsertRegistration(ctx, r); e != nil {
			return e
		}
		fresh = true
		return nil
	})
	if e != nil {
		return DingTalkSetupStatus{}, e
	}
	if !fresh {
		return s.registrationResult(r), nil
	}
	source := s.Source
	if source == "" {
		source = config.DingTalkRegistrationSource
	}
	begin, e := s.Provider.Begin(ctx, source)
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	result, writeErr := s.finish(finish, r, func(_ DingTalkSetupTx, current *DingTalkRegistration) error {
		if e != nil {
			current.State = "failed"
			current.ErrorCode = "dingtalk_registration_begin_failed"
			return nil
		}
		ciphertext, err := s.sealRegistration(*current, "device", begin.DeviceCode)
		if err != nil {
			return err
		}
		if begin.DeviceCode == "" || begin.ExpiresIn < 1 || begin.ExpiresIn > 600 || begin.Interval < 1 || begin.Interval > 60 {
			current.State = "failed"
			current.ErrorCode = "dingtalk_registration_invalid_response"
			return nil
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
func (s *DingTalkSetupService) registrationResult(r DingTalkRegistration) DingTalkSetupStatus {
	s.publicState(&r)
	return DingTalkSetupStatus{Registration: &r}
}

// finish rechecks actor authorization and the exact operation version after I/O.
// A cancellation, manual binding, or another request cannot be overwritten.
func (s *DingTalkSetupService) finish(ctx context.Context, r DingTalkRegistration, fn func(DingTalkSetupTx, *DingTalkRegistration) error) (DingTalkSetupStatus, error) {
	var out DingTalkRegistration
	e := s.with(ctx, r.WorkspaceID, r.ActorID, r.AgentID, func(t DingTalkSetupTx) error {
		current, e := dingSetupOwned(ctx, t, r.ID, r.ActorID, r.AgentID)
		if e != nil {
			return e
		}
		out = current
		if current.Version != r.Version || current.State != r.State {
			return nil
		}
		_, bindingErr := getChannelProvider(ctx, t, r.AgentID, "dingtalk")
		if bindingErr == nil {
			current.State = "failed"
			current.ErrorCode = "channel_configuration_changed"
			current.DeviceCipher = nil
			current.SecretCipher = nil
		} else if !IsChannelMissing(bindingErr) {
			return bindingErr
		} else if !s.now().Before(current.ExpiresAt) {
			current.State = "expired"
			current.DeviceCipher = nil
			current.SecretCipher = nil
		} else if e = fn(t, &current); e != nil {
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
func (s *DingTalkSetupService) Poll(ctx context.Context, w, a, agent, id string) (DingTalkSetupStatus, error) {
	var r DingTalkRegistration
	claimed := false
	e := s.with(ctx, w, a, agent, func(t DingTalkSetupTx) error {
		var e error
		r, e = dingSetupOwned(ctx, t, id, a, agent)
		if e != nil {
			return e
		}
		if r.State != "awaiting_confirmation" || s.now().Before(r.NextPollAt) {
			return nil
		}
		if !s.now().Before(r.ExpiresAt) || r.RetryUntil != nil && !s.now().Before(*r.RetryUntil) {
			r.State = "expired"
			r.ErrorCode = "dingtalk_registration_expired"
			r.DeviceCipher = nil
			r.UpdatedAt = s.now()
			return t.SaveRegistration(ctx, r)
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
		return DingTalkSetupStatus{}, e
	}
	if !claimed {
		return s.registrationResult(r), nil
	}
	device, e := s.openRegistration(r, "device", r.DeviceCipher)
	var result DingTalkPoll
	if e == nil {
		result, e = s.Provider.Poll(ctx, device)
	}
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return s.finish(finish, r, func(_ DingTalkSetupTx, current *DingTalkRegistration) error {
		if e != nil {
			current.State = "awaiting_confirmation"
			current.ErrorCode = "dingtalk_registration_poll_unavailable"
			current.NextPollAt = s.now().Add(time.Duration(current.Interval) * time.Second)
			return nil
		}
		return s.applyPoll(current, result)
	})
}
func (s *DingTalkSetupService) applyPoll(r *DingTalkRegistration, p DingTalkPoll) error {
	switch p.State {
	case "WAITING":
		r.RetryUntil = nil
		r.RetryStatus = ""
		r.NextPollAt = s.now().Add(time.Duration(r.Interval) * time.Second)
		r.State = "awaiting_confirmation"
	case "FAIL", "EXPIRED", "UNKNOWN":
		if r.RetryUntil == nil {
			until := minTime(r.ExpiresAt, s.now().Add(120*time.Second))
			r.RetryUntil = &until
		}
		r.RetryStatus = p.State
		r.State = "awaiting_confirmation"
		r.NextPollAt = s.now().Add(time.Duration(r.Interval) * time.Second)
	case "SUCCESS":
		if !validChannelAppID("dingtalk", p.AppID) || len(p.Secret) < 1 || len(p.Secret) > 4096 || strings.ContainsAny(p.Secret, "\r\n\x00") {
			r.State = "failed"
			r.ErrorCode = "dingtalk_registration_invalid_credentials"
			r.DeviceCipher = nil
			return nil
		}
		ciphertext, e := s.sealRegistration(*r, "secret", p.Secret)
		if e != nil {
			return e
		}
		r.SecretCipher = ciphertext
		r.AppID = p.AppID
		r.CredentialSaved = false
		r.State = "ready"
	default:
		r.State = "failed"
		r.ErrorCode = "dingtalk_registration_unsupported"
	}
	if r.State != "awaiting_confirmation" {
		r.DeviceCipher = nil
		if r.State != "ready" {
			r.SecretCipher = nil
		}
		r.VerificationURL = ""
	}
	return nil
}
func (s *DingTalkSetupService) Cancel(ctx context.Context, w, a, agent, id string) (DingTalkSetupStatus, error) {
	var r DingTalkRegistration
	e := s.with(ctx, w, a, agent, func(t DingTalkSetupTx) error {
		var e error
		r, e = dingSetupOwned(ctx, t, id, a, agent)
		if e != nil {
			return e
		}
		if r.State == "consumed" {
			return Fail("dingtalk_registration_already_connected", 409, "渠道已连接，取消不会删除钉钉应用")
		}
		r.State = "cancelled"
		r.ErrorCode = "dingtalk_registration_cancelled"
		r.DeviceCipher = nil
		r.SecretCipher = nil
		r.VerificationURL = ""
		r.UpdatedAt = s.now()
		return t.SaveRegistration(ctx, r)
	})
	return s.registrationResult(r), e
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
