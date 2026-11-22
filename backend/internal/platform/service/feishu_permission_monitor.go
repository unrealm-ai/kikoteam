package service

import (
	"context"
	"time"
)

type FeishuPermissionClaim struct {
	Binding ChannelBinding
	Token   string
}

type feishuPermissionStore interface {
	ClaimFeishuPermissionCheck(context.Context) (*FeishuPermissionClaim, error)
	FinishFeishuPermissionCheck(context.Context, FeishuPermissionClaim, FeishuDiagnostics, time.Duration) error
}

// CheckNextBinding records current permissions independently of how the
// credentials were saved. It only reads the provider and never expands scope.
func (s *FeishuSetupService) CheckNextBinding(ctx context.Context) (bool, error) {
	store, ok := s.Channels.Store.(feishuPermissionStore)
	if !ok {
		return false, nil
	}
	claim, err := store.ClaimFeishuPermissionCheck(ctx)
	if err != nil || claim == nil {
		return false, err
	}
	call, cancel := context.WithTimeout(ctx, 65*time.Second)
	defer cancel()
	b := claim.Binding
	d, err := s.diagnoseBinding(call, b)
	if err != nil {
		d = FeishuDiagnostics{CheckedAt: s.now(), CredentialStatus: "unknown", BotStatus: "unknown", DirectoryStatus: "unknown", ScopeStatus: "unknown", Permissions: FeishuPermissions(), ErrorCode: "feishu_registration_diagnostics_failed"}
	}
	delay := 5 * time.Minute
	if !d.Ready {
		delay = 30 * time.Second
	}
	finish, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stop()
	return true, store.FinishFeishuPermissionCheck(finish, *claim, d, delay)
}

func (s *FeishuSetupService) diagnoseBinding(ctx context.Context, b ChannelBinding) (FeishuDiagnostics, error) {
	secret, err := s.Channels.open(b)
	if err != nil {
		return FeishuDiagnostics{}, err
	}
	d, err := s.Provider.Diagnose(ctx, b.AppID, secret)
	if err != nil {
		return d, err
	}
	if b.TenantID != "" && d.TenantID != "" && b.TenantID != d.TenantID || b.BotID != "" && d.BotID != "" && b.BotID != d.BotID {
		d.Ready = false
		d.BotStatus = "failed"
		d.ScopeStatus = "unknown"
		d.Permissions = FeishuPermissions()
		d.ErrorCode = "feishu_identity_mismatch"
	}
	return d, nil
}

func BindingFeishuPermissionGroups(d *FeishuDiagnostics) []FeishuPermissionGroup {
	return permissionGroups(FeishuProfile(), d)
}
