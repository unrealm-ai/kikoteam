package service

import (
	"context"
	"regexp"
)

type ReferralSettings struct {
	Enabled bool  `json:"enabled"`
	Version int64 `json:"version"`
}

type ReferralLink struct {
	Enabled bool   `json:"enabled"`
	Code    string `json:"code,omitempty"`
}

type referralContextKey struct{}

var referralCodePattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Invalid referral codes do not prevent a normal registration.
func WithRegistrationReferral(ctx context.Context, code string) context.Context {
	if !referralCodePattern.MatchString(code) {
		return ctx
	}
	return context.WithValue(ctx, referralContextKey{}, code)
}

func RegistrationReferral(ctx context.Context) string {
	code, _ := ctx.Value(referralContextKey{}).(string)
	return code
}
