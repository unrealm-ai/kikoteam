package service

import (
	"context"
	"strings"
)

type AccountPage struct {
	Items    []Account `json:"items"`
	Total    int       `json:"total"`
	Page     int       `json:"page"`
	PageSize int       `json:"page_size"`
}
type AdminPatch struct {
	Version         int64 `json:"version"`
	IsPlatformAdmin bool  `json:"is_platform_admin"`
}
type AdminResult struct {
	AccountID       string `json:"account_id"`
	IsPlatformAdmin bool   `json:"is_platform_admin"`
	Version         int64  `json:"version"`
}
type AdminRepository interface {
	List(context.Context, string, string, string, int, int) (AccountPage, error)
	Set(context.Context, string, string, AdminPatch) (AdminResult, error)
}
type AdminService struct{ Store AdminRepository }

func (s *AdminService) List(ctx context.Context, a, q, f string, p, n int) (AccountPage, error) {
	if e := pageValid(p, n); e != nil {
		return AccountPage{}, e
	}
	if len([]rune(q)) > 100 || (f != "" && f != "true" && f != "false") {
		return AccountPage{}, Fail("invalid_query", 400, "查询条件无效")
	}
	return s.Store.List(ctx, a, strings.TrimSpace(q), f, p, n)
}
func (s *AdminService) Set(ctx context.Context, a, id string, r AdminPatch) (AdminResult, error) {
	if r.Version < 1 {
		return AdminResult{}, Fail("version_required", 400, "需要当前版本")
	}
	return s.Store.Set(ctx, a, id, r)
}
