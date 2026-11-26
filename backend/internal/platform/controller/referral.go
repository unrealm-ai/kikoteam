package controller

import (
	"context"
	"net/http"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type ReferralRepository interface {
	Link(context.Context, string, bool) (service.ReferralLink, error)
	Settings(context.Context, string) (service.ReferralSettings, error)
	SaveSettings(context.Context, string, int64, bool) (service.ReferralSettings, error)
}

func (s *Server) referralRoutes(m *http.ServeMux) {
	if s.Referrals == nil {
		return
	}
	for pattern, op := range map[string]operation{
		"GET /api/v1/referrals/me":                 s.referralLink,
		"POST /api/v1/referrals/me":                s.referralLink,
		"GET /api/v1/platform/referral-settings":   s.getReferralSettings,
		"PATCH /api/v1/platform/referral-settings": s.patchReferralSettings,
	} {
		m.HandleFunc(pattern, s.wrap(pattern, op))
	}
}

func (s *Server) referralLink(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	actor, err := s.actor(r)
	if err != nil {
		return nil, 0, err
	}
	out, err := s.Referrals.Link(r.Context(), actor.ID, r.Method == http.MethodPost)
	return out, http.StatusOK, err
}

func (s *Server) getReferralSettings(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	actor, err := s.actor(r)
	if err != nil {
		return nil, 0, err
	}
	out, err := s.Referrals.Settings(r.Context(), actor.ID)
	return out, http.StatusOK, err
}

func (s *Server) patchReferralSettings(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	actor, err := s.actor(r)
	if err != nil {
		return nil, 0, err
	}
	var b struct {
		ExpectedVersion *int64 `json:"expected_version"`
		Enabled         *bool  `json:"enabled"`
	}
	if err = decode(r, &b); err != nil {
		return nil, 0, err
	}
	if b.ExpectedVersion == nil || *b.ExpectedVersion < 0 || b.Enabled == nil {
		return nil, 0, service.Fail("invalid_request", 400, "请提供当前版本与邀请奖励开关")
	}
	out, err := s.Referrals.SaveSettings(r.Context(), actor.ID, *b.ExpectedVersion, *b.Enabled)
	return out, http.StatusOK, err
}
