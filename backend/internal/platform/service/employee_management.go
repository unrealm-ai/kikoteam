package service

import (
	"context"
	"strings"
	"unicode/utf8"
)

// EmployeeManagementCaller is reconstructed by Manager from the live input. It is never a tool argument.
type EmployeeManagementCaller struct {
	Workspace, Actor, Employee, Input string
	Channel                           bool
}

type employeeManagementContext struct{}

func WithEmployeeManagement(ctx context.Context, v EmployeeManagementCaller) context.Context {
	return context.WithValue(ctx, employeeManagementContext{}, v)
}

func EmployeeManagementFrom(ctx context.Context) (EmployeeManagementCaller, bool) {
	v, ok := ctx.Value(employeeManagementContext{}).(EmployeeManagementCaller)
	return v, ok
}

type ManagementQuery struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name,omitempty"`
	Page     int    `json:"page,omitempty"`
	PageSize int    `json:"page_size,omitempty"`
}

func (q *ManagementQuery) Normalize() error {
	q.Name = strings.TrimSpace(q.Name)
	if q.Page == 0 {
		q.Page = 1
	}
	if q.PageSize == 0 {
		q.PageSize = 20
	}
	if q.Page < 1 || q.Page > 100000 || q.PageSize < 1 || q.PageSize > 100 ||
		!utf8.ValidString(q.Name) || utf8.RuneCountInString(q.Name) > 200 || strings.ContainsRune(q.Name, 0) ||
		q.ID != "" && !ValidWorkspaceSubject(q.ID) {
		return Fail("invalid_query", 400, "请提供有效的名称、ID 和分页参数")
	}
	return nil
}

type ManagementPage[T any] struct {
	Items    []T `json:"items"`
	Total    int `json:"total"`
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
}

type EmployeeSummary struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Role    string `json:"role"`
	Avatar  string `json:"avatar"`
	Enabled bool   `json:"enabled"`
}

func ManagementSummary(a Agent) EmployeeSummary {
	return EmployeeSummary{a.ID, a.Name, a.Role, a.Avatar, a.Enabled}
}

type ManagementResource struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Summary   string `json:"summary"`
	Source    string `json:"source"`
	VersionID string `json:"version_id,omitempty"`
	Version   string `json:"version"`
}
