package service

import (
	"context"
	"time"
)

// DirectoryAlias is descriptive metadata, not an alternate authorization ID.
type DirectoryAlias struct {
	ID            string `json:"id"`
	Namespace     string `json:"namespace"`
	ApplicationID string `json:"application_id"`
}
type DirectoryUser struct {
	DirectManagerID    *string          `json:"direct_manager_id"`
	DirectManagerKnown bool             `json:"direct_manager_known"`
	Aliases            []DirectoryAlias `json:"aliases"`

	ID               string   `json:"id"`
	DisplayName      string   `json:"display_name"`
	Status           string   `json:"status"`
	DepartmentIDs    []string `json:"department_ids"`
	MembershipsKnown bool     `json:"memberships_known"`
}
type DirectoryDepartment struct {
	LeaderIDs    []string `json:"leader_ids"`
	LeadersKnown bool     `json:"leaders_known"`

	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	ParentID    string `json:"parent_id"`
}
type DirectorySnapshot struct {
	Users       []DirectoryUser       `json:"users"`
	Departments []DirectoryDepartment `json:"departments"`
}
type DirectoryFetchError struct {
	Detail     string
	Code       string
	Invalidate bool
}

func (e *DirectoryFetchError) Error() string { return e.Code }

type DirectoryFetcher interface {
	Fetch(context.Context, string, string, string) (DirectorySnapshot, error)
}
type DirectoryStatus struct {
	Status          string     `json:"status"`
	SnapshotVersion int64      `json:"snapshot_version"`
	Complete        bool       `json:"complete"`
	SyncedAt        *time.Time `json:"synced_at"`
	ExpiresAt       *time.Time `json:"expires_at"`
	ErrorCode       string     `json:"error_code"`
	UserCount       int        `json:"user_count"`
	DepartmentCount int        `json:"department_count"`
}
type DirectoryQuery struct {
	Kind            string
	Q               string
	ParentID        string
	DepartmentID    string
	Limit           int
	Cursor          string
	SnapshotVersion int64
}
type DirectoryPage struct {
	Items           any     `json:"items"`
	NextCursor      *string `json:"next_cursor"`
	SnapshotVersion int64   `json:"snapshot_version"`
}
type DirectoryClaim struct {
	Binding ChannelBinding
	Token   string
}
type ChannelDirectoryStore interface {
	DirectoryStatus(context.Context, ChannelBinding) (DirectoryStatus, error)
	RequestDirectorySync(context.Context, ChannelBinding) (DirectoryStatus, error)
	DirectoryPage(context.Context, ChannelBinding, DirectoryQuery) (DirectoryPage, error)
	ClaimDirectorySync(context.Context) (*DirectoryClaim, error)
	PublishDirectory(context.Context, DirectoryClaim, DirectorySnapshot) error
	FailDirectory(context.Context, DirectoryClaim, string, bool) error
}
