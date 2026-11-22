package service

import "context"

// Confirm atomically consumes credentials and saves only a new binding. Neither
// QR success nor token exchange proves corporation identity; Stream pins it later.
func (s *DingTalkSetupService) Confirm(ctx context.Context, w, a, agent, id string) (DingTalkSetupStatus, error) {
	var r DingTalkRegistration
	e := s.with(ctx, w, a, agent, func(t DingTalkSetupTx) error {
		var e error
		r, e = dingSetupOwned(ctx, t, id, a, agent)
		if e != nil {
			return e
		}
		if r.State == "consumed" {
			return nil
		}
		if r.State != "ready" || len(r.SecretCipher) == 0 || !s.now().Before(r.ExpiresAt) {
			return Fail("dingtalk_registration_not_ready", 409, "扫码凭据未就绪或已过期")
		}
		_, e = getChannelProvider(ctx, t, agent, "dingtalk")
		if e == nil {
			return Fail("channel_configuration_changed", 409, "已有渠道配置，不能覆盖")
		}
		if !IsChannelMissing(e) {
			return e
		}
		secret, e := s.openRegistration(r, "secret", r.SecretCipher)
		if e != nil {
			return e
		}
		if !validChannelAppID("dingtalk", r.AppID) {
			return Fail("invalid_dingtalk_registration", 400, "应用身份无效")
		}
		b := ChannelBinding{ID: NewID(), WorkspaceID: w, AgentID: agent, Provider: "dingtalk", IdentityEpoch: 1, Generation: 1, RulesVersion: 1, Revision: 1, Enabled: true, AppID: r.AppID, HasSecret: true, Trigger: ChannelTrigger{Mention: true, Keywords: []string{}, Regexes: []string{}}, History: ChannelHistory{Limit: 100}, Access: ChannelAccess{Mode: "allowlist", AllowIDs: []string{}, DenyIDs: []string{}, AllowChatIDs: []string{}, DenyChatIDs: []string{}, AllowDepartmentIDs: []string{}, DenyDepartmentIDs: []string{}, AllowDepartmentRules: []ChannelDepartmentRule{}, DenyDepartmentRules: []ChannelDepartmentRule{}}, Applied: ChannelApplied{Status: "pending"}}
		if e = s.Channels.prepareRegistrationBinding(ctx, t, &b, secret); e != nil {
			return e
		}
		if e = t.Save(ctx, b, true); e != nil {
			return e
		}
		r.State = "consumed"
		r.CredentialSaved = true
		r.SecretCipher = nil
		r.DeviceCipher = nil
		r.VerificationURL = ""
		r.ErrorCode = ""
		r.UpdatedAt = s.now()
		return t.SaveRegistration(ctx, r)
	})
	return s.registrationResult(r), e
}
