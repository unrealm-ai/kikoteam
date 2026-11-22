package service

import (
	"context"
	"errors"
	"testing"
)

func TestDisabledEmailRejectsAllCodeFlows(t *testing.T) {
	s := &AuthService{EmailCodeDisabled: true}
	ctx := context.Background()
	_, e1 := s.RequestCode(ctx, "a@example.com", "register", "ip")
	_, _, e2 := s.Register(ctx, "a@example.com", "A", "", "123456", "id")
	_, _, e3 := s.Login(ctx, "a@example.com", "code", "", "123456", "id", "ip")
	e4 := s.ResetPassword(ctx, "a@example.com", "123456", "id", "new-password")
	for _, err := range []error{e1, e2, e3, e4} {
		var target *Error
		if !errors.As(err, &target) || target.Code != "email_code_disabled" {
			t.Fatalf("unexpected error %v", err)
		}
	}
}
