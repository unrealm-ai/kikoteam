package controller

import (
	"net/http"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func (s *Server) mailSettingsRoutes(m *http.ServeMux) {
	if s.MailSettings == nil {
		return
	}
	m.HandleFunc("GET /api/v1/platform/mail-settings", s.wrap("GET /api/v1/platform/mail-settings", s.mailSettingsGet))
	m.HandleFunc("PATCH /api/v1/platform/mail-settings", s.wrap("PATCH /api/v1/platform/mail-settings", s.mailSettingsSave))
	m.HandleFunc("POST /api/v1/platform/mail-settings/test", s.wrap("POST /api/v1/platform/mail-settings/test", s.mailSettingsTest))
}

func (s *Server) mailSettingsGet(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	actor, err := s.actor(r)
	if err != nil {
		return nil, 0, err
	}
	out, err := s.MailSettings.Get(r.Context(), actor.ID)
	return out, 200, err
}

func (s *Server) mailSettingsSave(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	actor, err := s.actor(r)
	if err != nil {
		return nil, 0, err
	}
	in, err := decodeMailSettings(r)
	if err != nil {
		return nil, 0, err
	}
	out, err := s.MailSettings.Save(r.Context(), actor.ID, in)
	return out, 200, err
}

func (s *Server) mailSettingsTest(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	actor, err := s.actor(r)
	if err != nil {
		return nil, 0, err
	}
	in, err := decodeMailSettings(r)
	if err != nil {
		return nil, 0, err
	}
	err = s.MailSettings.Test(r.Context(), actor.ID, in)
	return map[string]bool{"ok": true}, 200, err
}

func decodeMailSettings(r *http.Request) (service.MailSettingsDraft, error) {
	var in struct {
		Version              *int64  `json:"version"`
		Enabled              *bool   `json:"enabled"`
		VerificationRequired *bool   `json:"registration_verification_required"`
		Address              *string `json:"address"`
		Host                 *string `json:"host"`
		Username             *string `json:"username"`
		From                 *string `json:"from"`
		ImplicitTLS          *bool   `json:"implicit_tls"`
		Password             string  `json:"password"`
	}
	if err := decode(r, &in); err != nil {
		return service.MailSettingsDraft{}, err
	}
	if in.Version == nil || in.Enabled == nil || in.VerificationRequired == nil ||
		in.Address == nil || in.Host == nil || in.Username == nil || in.From == nil || in.ImplicitTLS == nil {
		return service.MailSettingsDraft{}, service.Fail("invalid_mail_settings", 400, "邮件设置缺少必填字段")
	}
	return service.MailSettingsDraft{Version: *in.Version, Password: in.Password, MailSettings: service.MailSettings{
		MailPolicy: service.MailPolicy{Enabled: *in.Enabled, VerificationRequired: *in.VerificationRequired},
		Address:    *in.Address, Host: *in.Host, Username: *in.Username, From: *in.From, ImplicitTLS: *in.ImplicitTLS,
	}}, nil
}
