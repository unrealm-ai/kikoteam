package service

import (
	"context"
	"strings"
	"time"
	_ "time/tzdata" // Ship an IANA database for minimal deployment images.
)

const DefaultPlatformTimezone = "Asia/Shanghai"

type TimezoneSettings struct {
	Version  int64  `json:"version"`
	Timezone string `json:"timezone"`
}

type TimezoneSettingsPatch struct {
	ExpectedVersion *int64 `json:"expected_version"`
	Timezone        string `json:"timezone"`
}

type TimezoneSettingsRepository interface {
	ReadAdmin(context.Context, string) (TimezoneSettings, error)
	SaveAdmin(context.Context, string, int64, string) (TimezoneSettings, error)
}

type TimezoneSettingsService struct{ Store TimezoneSettingsRepository }

func (s *TimezoneSettingsService) Get(ctx context.Context, actor string) (TimezoneSettings, error) {
	return s.Store.ReadAdmin(ctx, actor)
}

func (s *TimezoneSettingsService) Save(ctx context.Context, actor string, patch TimezoneSettingsPatch) (TimezoneSettings, error) {
	zone := strings.TrimSpace(patch.Timezone)
	if patch.ExpectedVersion == nil || *patch.ExpectedVersion < 0 || zone == "" || len(zone) > 128 || zone == "Local" {
		return TimezoneSettings{}, Fail("invalid_platform_timezone", 400, "请选择有效的 IANA 时区，例如 Asia/Shanghai")
	}
	if _, err := time.LoadLocation(zone); err != nil {
		return TimezoneSettings{}, Fail("invalid_platform_timezone", 400, "请选择有效的 IANA 时区，例如 Asia/Shanghai")
	}
	return s.Store.SaveAdmin(ctx, actor, *patch.ExpectedVersion, zone)
}
