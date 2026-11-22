package service

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

func (s *ChannelService) directoryStore() (ChannelDirectoryStore, error) {
	store, ok := s.Store.(ChannelDirectoryStore)
	if !ok {
		return nil, Fail("directory_unavailable", 503, "通讯录暂不可用")
	}
	return store, nil
}
func (s *ChannelService) DirectoryStatusProvider(ctx context.Context, w, actor, agent, provider string) (DirectoryStatus, error) {
	b, err := s.directoryBinding(ctx, w, actor, agent, provider)
	if err != nil {
		return DirectoryStatus{}, err
	}
	store, err := s.directoryStore()
	if err != nil {
		return DirectoryStatus{}, err
	}
	return store.DirectoryStatus(ctx, b)
}
func (s *ChannelService) RequestDirectorySyncProvider(ctx context.Context, w, actor, agent, provider string) (DirectoryStatus, error) {
	b, err := s.directoryBinding(ctx, w, actor, agent, provider)
	if err != nil {
		return DirectoryStatus{}, err
	}
	if !b.Enabled {
		return DirectoryStatus{}, Fail("channel_disabled", 409, "请先启用渠道")
	}
	if b.Provider == "feishu" && !b.IdentityConfirmed {
		return DirectoryStatus{}, Fail("directory_identity_unverified", 409, "请先完成渠道租户身份验证")
	}
	store, err := s.directoryStore()
	if err != nil {
		return DirectoryStatus{}, err
	}
	v, err := store.RequestDirectorySync(ctx, b)
	if err == nil {
		select {
		case s.DirectoryWake <- struct{}{}:
		default:
		}
	}
	return v, err
}
func (s *ChannelService) DirectoryPageProvider(ctx context.Context, w, actor, agent, provider string, q DirectoryQuery) (DirectoryPage, error) {
	b, err := s.directoryBinding(ctx, w, actor, agent, provider)
	if err != nil {
		return DirectoryPage{}, err
	}
	q.Q = strings.TrimSpace(q.Q)
	if (q.Kind != "users" && q.Kind != "departments") || q.Limit < 1 || q.Limit > 100 || q.SnapshotVersion < 0 || utf8.RuneCountInString(q.Q) > 100 || len(q.ParentID) > 256 || len(q.DepartmentID) > 256 || len(q.Cursor) > 4096 || strings.ContainsAny(q.Q+q.ParentID+q.DepartmentID, "\x00\r\n") {
		return DirectoryPage{}, Fail("invalid_directory_query", 400, "通讯录查询无效")
	}
	store, err := s.directoryStore()
	if err != nil {
		return DirectoryPage{}, err
	}
	return store.DirectoryPage(ctx, b, q)
}

// SyncNextDirectory uses a durable source-fenced lease, so every Backend may
// poll without publishing concurrent partial snapshots. Network I/O is outside
// database transactions. A crashed lease becomes eligible after two minutes.
func (s *ChannelService) SyncNextDirectory(ctx context.Context, fetcher DirectoryFetcher) (bool, error) {
	store, err := s.directoryStore()
	if err != nil {
		return false, err
	}
	claim, err := store.ClaimDirectorySync(ctx)
	if err != nil || claim == nil {
		return false, err
	}
	secret, err := s.open(claim.Binding)
	if err != nil {
		return true, store.FailDirectory(ctx, *claim, "directory_credentials_invalid", true)
	}
	call, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	var snapshot DirectorySnapshot
	if multi, ok := fetcher.(interface {
		FetchProvider(context.Context, string, string, string, string) (DirectorySnapshot, error)
	}); ok {
		snapshot, err = multi.FetchProvider(call, claim.Binding.Provider, claim.Binding.AppID, secret, claim.Binding.TenantID)
	} else if claim.Binding.Provider == "feishu" {
		snapshot, err = fetcher.Fetch(call, claim.Binding.AppID, secret, claim.Binding.TenantID)
	} else {
		err = &DirectoryFetchError{Code: "directory_provider_unavailable", Invalidate: true}
	}
	if err == nil {
		err = validateDirectorySnapshot(snapshot)
	}
	if err != nil {
		code, invalidate := "directory_sync_failed", false
		var failure *DirectoryFetchError
		if errors.As(err, &failure) {
			code, invalidate = failure.Code, failure.Invalidate
		}
		if call.Err() != nil {
			code = "directory_sync_timeout"
		}
		if e := store.FailDirectory(ctx, *claim, code, invalidate); e != nil {
			return true, e
		}
		return true, err
	}
	return true, store.PublishDirectory(ctx, *claim, snapshot)
}
func validateDirectorySnapshot(v DirectorySnapshot) error {
	if len(v.Users) > 100000 || len(v.Departments) > 100000 {
		return &DirectoryFetchError{Code: "directory_size_limit"}
	}
	valid := func(v string) bool { return v != "" && len(v) <= 256 && !strings.ContainsAny(v, "\x00\r\n") }
	seen := map[string]bool{}
	for _, d := range v.Departments {
		if !valid(d.ID) || !valid(d.DisplayName) || len(d.ParentID) > 256 || strings.ContainsAny(d.ParentID, "\x00\r\n") || seen[d.ID] || d.ParentID == d.ID {
			return &DirectoryFetchError{Code: "directory_invalid_response"}
		}
		if err := validateDirectoryLeaders(d, valid); err != nil {
			return err
		}
		seen[d.ID] = true
	}
	seen = map[string]bool{}
	for _, u := range v.Users {
		if !valid(u.ID) || !valid(u.DisplayName) || seen[u.ID] || (u.Status != "active" && u.Status != "inactive" && u.Status != "unknown") || len(u.DepartmentIDs) > 1000 {
			return &DirectoryFetchError{Code: "directory_invalid_response"}
		}
		if err := validateDirectoryUserMetadata(u, valid); err != nil {
			return err
		}
		seen[u.ID] = true
		depts := map[string]bool{}
		for _, id := range u.DepartmentIDs {
			if !valid(id) || depts[id] {
				return &DirectoryFetchError{Code: "directory_invalid_response"}
			}
			depts[id] = true
		}
	}
	return nil
}

func (s *ChannelService) directoryBinding(ctx context.Context, w, actor, agent, provider string) (ChannelBinding, error) {
	b, err := s.GetProvider(ctx, w, actor, agent, provider)
	if err != nil {
		return b, err
	}
	if provider != "feishu" && provider != "dingtalk" {
		return b, Fail("directory_provider_unsupported", 400, "此渠道不支持组织通讯录")
	}
	return b, nil
}
func (s *ChannelService) DirectoryStatus(ctx context.Context, w, actor, agent string) (DirectoryStatus, error) {
	return s.DirectoryStatusProvider(ctx, w, actor, agent, "feishu")
}
func (s *ChannelService) RequestDirectorySync(ctx context.Context, w, actor, agent string) (DirectoryStatus, error) {
	return s.RequestDirectorySyncProvider(ctx, w, actor, agent, "feishu")
}
func (s *ChannelService) DirectoryPage(ctx context.Context, w, actor, agent string, q DirectoryQuery) (DirectoryPage, error) {
	return s.DirectoryPageProvider(ctx, w, actor, agent, "feishu", q)
}

func validateDirectoryLeaders(d DirectoryDepartment, valid func(string) bool) error {
	if (!d.LeadersKnown && len(d.LeaderIDs) > 0) || len(d.LeaderIDs) > 1000 {
		return &DirectoryFetchError{Code: "directory_invalid_response"}
	}
	leaders := map[string]bool{}
	for _, id := range d.LeaderIDs {
		if !valid(id) || leaders[id] {
			return &DirectoryFetchError{Code: "directory_invalid_response"}
		}
		leaders[id] = true
	}

	return nil
}
func validateDirectoryUserMetadata(u DirectoryUser, valid func(string) bool) error {
	if (!u.DirectManagerKnown && u.DirectManagerID != nil) || (u.DirectManagerID != nil && !valid(*u.DirectManagerID)) || len(u.Aliases) > 100 {
		return &DirectoryFetchError{Code: "directory_invalid_response"}
	}
	aliases := map[DirectoryAlias]bool{}
	for _, alias := range u.Aliases {
		if !valid(alias.ID) || !valid(alias.Namespace) || !valid(alias.ApplicationID) || aliases[alias] {
			return &DirectoryFetchError{Code: "directory_invalid_response"}
		}
		aliases[alias] = true
	}

	return nil
}
