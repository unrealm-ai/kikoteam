package service

import "context"

type ChannelDownloadSettings struct {
	Version          int64 `json:"version"`
	ValidatePublicIP bool  `json:"validate_public_ip"`
}

type ChannelDownloadSettingsPatch struct {
	ExpectedVersion  *int64 `json:"expected_version"`
	ValidatePublicIP *bool  `json:"validate_public_ip"`
}

type ChannelDownloadSettingsRepository interface {
	ReadAdmin(context.Context, string) (ChannelDownloadSettings, error)
	SaveAdmin(context.Context, string, int64, bool) (ChannelDownloadSettings, error)
}

type ChannelDownloadSettingsService struct {
	Store ChannelDownloadSettingsRepository
}

func (s *ChannelDownloadSettingsService) Get(ctx context.Context, actor string) (ChannelDownloadSettings, error) {
	return s.Store.ReadAdmin(ctx, actor)
}

func (s *ChannelDownloadSettingsService) Save(ctx context.Context, actor string, p ChannelDownloadSettingsPatch) (ChannelDownloadSettings, error) {
	if p.ExpectedVersion == nil || *p.ExpectedVersion < 0 || p.ValidatePublicIP == nil {
		return ChannelDownloadSettings{}, Fail("invalid_request", 400, "请提供当前版本和公网 IP 校验设置")
	}
	return s.Store.SaveAdmin(ctx, actor, *p.ExpectedVersion, *p.ValidatePublicIP)
}
