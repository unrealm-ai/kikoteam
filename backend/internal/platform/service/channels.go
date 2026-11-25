package service

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"regexp"
	"strings"
)

func channelAuthorize(ctx context.Context, t ChannelTx, a string) error {
	m, e := t.Member(ctx, a)
	if e != nil {
		return e
	}
	if m.Status != "active" || (m.Role != "owner" && m.Role != "admin") {
		return Fail("workspace_forbidden", 403, "没有渠道管理权限")
	}
	return nil
}
func (s *ChannelService) GetProvider(ctx context.Context, w, a, employee, provider string) (ChannelBinding, error) {
	return s.getProvider(ctx, w, a, employee, provider, false)
}

func (s *ChannelService) ProviderWithRebindImpact(ctx context.Context, w, a, employee, provider string) (ChannelBinding, error) {
	return s.getProvider(ctx, w, a, employee, provider, true)
}

func (s *ChannelService) getProvider(ctx context.Context, w, a, employee, provider string, includeImpact bool) (ChannelBinding, error) {
	if !ValidChannelProvider(provider) {
		return ChannelBinding{}, Fail("invalid_channel_provider", 400, "渠道类型无效")
	}
	var out ChannelBinding
	if !agentUUID(w) || !agentUUID(employee) {
		return out, Fail("invalid_channel", 400, "员工或工作区标识无效")
	}
	read := s.Store.WithWorkspace
	if store, ok := s.Store.(interface {
		WithBindingRead(context.Context, string, func(ChannelTx) error) error
	}); ok {
		read = store.WithBindingRead
	}
	e := read(ctx, w, func(t ChannelTx) error {
		if e := channelAuthorize(ctx, t, a); e != nil {
			return e
		}
		if e := t.AgentExists(ctx, employee); e != nil {
			return e
		}
		var e error
		out, e = getChannelProvider(ctx, t, employee, provider)
		if e != nil || !includeImpact {
			return e
		}
		reader, ok := t.(interface {
			ReadRebindImpact(context.Context, ChannelBinding) (*ChannelRebindImpact, error)
		})
		if !ok {
			return Fail("channel_rebind_impact_unavailable", 503, "暂时无法读取换绑影响")
		}
		out.RebindImpact, e = reader.ReadRebindImpact(ctx, out)
		return e
	})
	return out, e
}

func (s *ChannelService) ObservedProvider(ctx context.Context, w, a, employee, provider string) (ChannelBinding, error) {
	b, err := s.GetProvider(ctx, w, a, employee, provider)
	if err != nil {
		return b, err
	}
	if provider == "feishu" {
		if store, ok := s.Store.(interface {
			FeishuPermissionDiagnostics(context.Context, ChannelBinding) (*FeishuDiagnostics, error)
		}); ok {
			b.PermissionDiagnostics, err = store.FeishuPermissionDiagnostics(ctx, b)
			if err != nil {
				return b, err
			}
		}
	}
	if provider == "feishu" {
		b.PermissionGroups = BindingFeishuPermissionGroups(b.PermissionDiagnostics)
	}
	if provider == "dingtalk" && b.Enabled {
		if store, ok := s.Store.(interface {
			DingTalkProactiveSendPermissionMissing(context.Context, ChannelBinding) (bool, error)
		}); ok {
			b.DingTalkProactiveSendPermissionMissing, err = store.DingTalkProactiveSendPermissionMissing(ctx, b)
			if err != nil {
				return b, err
			}
		}
	}
	if provider == "feishu" || provider == "dingtalk" {
		if store, ok := s.Store.(ChannelDirectoryStore); ok {
			status, readErr := store.DirectoryStatus(ctx, b)
			b.DirectoryStatus = &status
			return b, readErr
		}
	}
	return b, nil
}
func (s *ChannelService) aead() (cipher.AEAD, error) {
	if len(s.Secret) < 32 {
		return nil, errors.New("channel encryption unavailable")
	}
	k := sha256.Sum256(append([]byte("kiko:channel-credential:v1:"), s.Secret...))
	b, e := aes.NewCipher(k[:])
	if e != nil {
		return nil, e
	}
	return cipher.NewGCM(b)
}
func channelAAD(b ChannelBinding) []byte {
	return []byte(b.WorkspaceID + ":" + b.AgentID + ":" + b.ID + ":" + channelProvider(b.Provider) + ":v1")
}
func (s *ChannelService) seal(b ChannelBinding, value string) ([]byte, error) {
	a, e := s.aead()
	if e != nil {
		return nil, e
	}
	n := make([]byte, a.NonceSize())
	if _, e = rand.Read(n); e != nil {
		return nil, e
	}
	return a.Seal(n, n, []byte(value), channelAAD(b)), nil
}
func (s *ChannelService) open(b ChannelBinding) (string, error) {
	a, e := s.aead()
	if e != nil {
		return "", e
	}
	if len(b.Ciphertext) < a.NonceSize() {
		return "", errors.New("invalid channel ciphertext")
	}
	v, e := a.Open(nil, b.Ciphertext[:a.NonceSize()], b.Ciphertext[a.NonceSize():], channelAAD(b))
	if e != nil {
		return "", errors.New("channel credential authentication failed")
	}
	return string(v), nil
}
func validateChannelFor(r *ChannelPut, provider string) error {
	if !ValidChannelProvider(provider) {
		return Fail("invalid_channel_provider", 400, "渠道类型无效")
	}
	r.AppID = strings.TrimSpace(r.AppID)
	if r.Version < 0 || !validChannelAppID(provider, r.AppID) {
		return Fail("invalid_channel", 400, "飞书应用标识或版本无效")
	}
	if r.AppSecret != nil && (len(*r.AppSecret) < 1 || len(*r.AppSecret) > 4096 || strings.ContainsAny(*r.AppSecret, "\r\n\x00")) {
		return Fail("invalid_channel", 400, "飞书凭据无效")
	}
	if r.History.Limit < 1 || r.History.Limit > 100 {
		return Fail("invalid_channel", 400, "历史条数必须为1至100")
	}
	if err := validateProviderCapabilities(provider, r); err != nil {
		return err
	}
	if r.Access.Mode != "all" && r.Access.Mode != "allowlist" && r.Access.Mode != "denylist" {
		return Fail("invalid_channel", 400, "使用范围无效")
	}
	if err := NormalizeDepartmentRules(&r.Access); err != nil {
		return err
	}
	lists := []*[]string{&r.Trigger.Keywords, &r.Trigger.Regexes, &r.Access.AllowIDs, &r.Access.DenyIDs, &r.Access.AllowChatIDs, &r.Access.DenyChatIDs, &r.Access.AllowDepartmentIDs, &r.Access.DenyDepartmentIDs}
	for _, list := range lists {
		if *list == nil {
			*list = []string{}
		}
		if len(*list) > 100 {
			return Fail("invalid_channel", 400, "规则条目过多")
		}
		for _, v := range *list {
			if strings.TrimSpace(v) == "" || len(v) > 512 || strings.ContainsAny(v, "\r\n\x00") {
				return Fail("invalid_channel", 400, "规则条目无效")
			}
		}
	}
	for _, v := range r.Trigger.Regexes {
		if _, e := regexp.Compile(v); e != nil {
			return Fail("invalid_channel_regex", 400, "正则表达式无效")
		}
	}
	return nil
}
func (s *ChannelService) PutProvider(ctx context.Context, w, a, employee, provider string, r ChannelPut) (ChannelBinding, error) {
	var out ChannelBinding
	if !agentUUID(w) || !agentUUID(employee) {
		return out, Fail("invalid_channel", 400, "员工或工作区标识无效")
	}
	if e := validateChannelFor(&r, provider); e != nil {
		return out, e
	}
	e := s.Store.WithWorkspace(ctx, w, func(t ChannelTx) error {
		if e := channelAuthorize(ctx, t, a); e != nil {
			return e
		}
		if e := t.AgentExists(ctx, employee); e != nil {
			return e
		}
		current, e := getChannelProvider(ctx, t, employee, provider)
		create := IsChannelMissing(e)
		if e != nil && !create {
			return e
		}
		if create && r.Version != 0 || !create && r.Version != current.Revision {
			return Fail("channel_version_conflict", 409, "渠道配置已更新，请刷新后重试")
		}
		if !create && current.AppID != r.AppID && (len(r.Access.AllowIDs)+len(r.Access.DenyIDs)+len(r.Access.AllowChatIDs)+len(r.Access.DenyChatIDs)+len(r.Access.AllowDepartmentIDs)+len(r.Access.DenyDepartmentIDs) > 0) {
			return Fail("channel_rebind_scope_reset_required", 409, "更换应用后请清空旧人员、部门和群规则并重新选择")
		}
		if (create || current.AppID != r.AppID) && r.AppSecret == nil {
			return Fail("invalid_channel", 400, "新应用必须提供凭据")
		}
		out = current
		if create {
			out = ChannelBinding{ID: NewID(), WorkspaceID: w, AgentID: employee, Provider: provider, IdentityEpoch: 1, Generation: 1, RulesVersion: 1}
		}
		if !create && current.AppID != r.AppID {
			out.IdentityEpoch++
		}
		if !create && (r.AppSecret != nil || current.AppID != r.AppID || current.Enabled != r.Enabled) {
			out.Generation++
		}
		if !create && current.AppID != r.AppID {
			out.TenantID = ""
			out.BotID = ""
		}
		out.Revision = r.Version + 1
		out.AppID = r.AppID
		out.Enabled = r.Enabled
		out.Trigger = r.Trigger
		out.History = r.History
		out.Access = r.Access
		SetChannelIdentityPresentation(&out)
		out.HasSecret = true
		out.Applied.Status = "pending"
		out.Applied.ErrorCode = ""
		if r.AppSecret != nil {
			out.Ciphertext, e = s.seal(out, *r.AppSecret)
			if e != nil {
				return e
			}
		}
		if e = applyChannelUsageProjection(ctx, t, current, &out, create); e != nil {
			return e
		}
		if validator, ok := t.(interface {
			ValidateDirectorySelections(context.Context, ChannelBinding, ChannelAccess) error
		}); ok {
			if e = validator.ValidateDirectorySelections(ctx, out, current.Access); e != nil {
				return e
			}
		}
		return saveChannelConfig(ctx, t, &out, create)
	})
	return out, e
}

func saveChannelConfig(ctx context.Context, t ChannelTx, b *ChannelBinding, create bool) error {
	if err := t.Save(ctx, *b, create); err != nil {
		return err
	}
	// Save can initialize the canonical employee history for a new channel.
	if create && (b.Provider == "feishu" || b.Provider == "wecom") {
		stored, err := getChannelProvider(ctx, t, b.AgentID, b.Provider)
		if err != nil {
			return err
		}
		*b = stored
	}
	return nil
}

func IsChannelMissing(e error) bool {
	var f *Error
	return errors.As(e, &f) && f.Code == "channel_not_configured"
}
func (s *ChannelService) Snapshot(ctx context.Context) (HubChannelSnapshot, error) {
	revision, rows, e := s.Store.Snapshot(ctx)
	out := HubChannelSnapshot{Revision: revision, Items: []HubChannelBinding{}}
	if e != nil {
		return out, e
	}
	for _, b := range rows {
		secret, e := s.open(b)
		if e != nil {
			return out, e
		}
		out.Items = append(out.Items, HubChannelBinding{ChannelBinding: b, Credentials: ChannelCredentials{AppID: b.AppID, AppSecret: secret}})
	}
	return out, nil
}
func (s *ChannelService) Apply(ctx context.Context, id string, r ChannelApplied) error {
	if !agentUUID(id) || r.Generation < 1 || r.RulesVersion < 1 || r.Revision < 1 {
		return Fail("invalid_channel_status", 400, "状态版本无效")
	}
	switch r.Status {
	case "applied", "connected", "disconnected", "failed":
	default:
		return Fail("invalid_channel_status", 400, "状态无效")
	}
	if r.ErrorCode != "" && !regexp.MustCompile(`^[a-z0-9_]{1,80}$`).MatchString(r.ErrorCode) {
		return Fail("invalid_channel_status", 400, "状态错误码无效")
	}
	for _, v := range []string{r.ObservedTenantID, r.ObservedBotID} {
		if len(v) > 200 || strings.ContainsAny(v, "\r\n\x00") {
			return Fail("invalid_channel_status", 400, "渠道身份无效")
		}
	}
	return s.Store.Applied(ctx, id, r)
}
