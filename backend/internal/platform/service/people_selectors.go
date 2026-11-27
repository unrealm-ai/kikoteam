package service

import (
	"context"
	"strings"
	"unicode/utf8"
)

type PeopleCandidate struct {
	Ref                SelectionRef `json:"ref"`
	DisplayName        string       `json:"display_name"`
	Status             string       `json:"status"`
	Selectable         bool         `json:"selectable"`
	DisabledReason     string       `json:"disabled_reason,omitempty"`
	VerificationStatus string       `json:"verification_status,omitempty"`
	VerificationID     string       `json:"verification_id,omitempty"`
	SearchUserID       string       `json:"search_user_id,omitempty"`
	DepartmentPaths    []string     `json:"department_paths,omitempty"`
	ParentID           string       `json:"parent_id,omitempty"`
}
type PeopleQuery struct {
	Provider, Purpose, Kind, Q, ParentID, DepartmentID, Cursor string
	Limit                                                      int
	SnapshotVersion                                            int64
}
type PeoplePage struct {
	Items           []PeopleCandidate `json:"items"`
	NextCursor      *string           `json:"next_cursor"`
	SnapshotVersion int64             `json:"snapshot_version"`
	Limited         bool              `json:"limited"`
}
type PeopleSource struct {
	Provider                string `json:"provider"`
	SupportsSearch          bool   `json:"supports_search"`
	SupportsBrowse          bool   `json:"supports_browse"`
	SupportsDepartmentTree  bool   `json:"supports_department_tree"`
	SupportsPagination      bool   `json:"supports_pagination"`
	RequiresIdentityMapping bool   `json:"requires_identity_mapping"`
	Available               bool   `json:"available"`
	UnavailableReason       string `json:"unavailable_reason,omitempty"`
}
type PeopleSelectorStore interface {
	SearchWebPeople(context.Context, string, string, string, PeopleQuery) (PeoplePage, error)
	ResolvePerson(context.Context, string, string, string, SelectionRef) (PeopleCandidate, error)
}
type PeopleSelectorService struct {
	Secret   []byte
	Channels *ChannelService
	Wecom    *WecomContactsService
	Store    PeopleSelectorStore
}

func validPeoplePurpose(p string) bool {
	return p == "usage" || p == "capability" || p == "sharing" || p == "confirmation" || p == "maintenance"
}
func (s *PeopleSelectorService) authorize(ctx context.Context, w, actor, a string) error {
	if !agentUUID(w) || !agentUUID(a) {
		return Fail("invalid_selector_scope", 400, "选择范围无效")
	}
	return s.Channels.Store.WithWorkspace(ctx, w, func(t ChannelTx) error {
		if e := AuthorizeEmployee(ctx, t, actor, a); e != nil {
			return e
		}
		return t.AgentExists(ctx, a)
	})
}
func (s *PeopleSelectorService) Sources(ctx context.Context, w, actor, a, purpose string) ([]PeopleSource, error) {
	if !validPeoplePurpose(purpose) {
		return nil, Fail("invalid_selector_purpose", 400, "选择用途无效")
	}
	if e := s.authorize(ctx, w, actor, a); e != nil {
		return nil, e
	}
	out := []PeopleSource{}
	for _, p := range []string{"web", "feishu", "dingtalk", "wecom"} {
		v := PeopleSource{Provider: p, SupportsSearch: true, SupportsBrowse: p != "wecom", SupportsDepartmentTree: (p == "feishu" || p == "dingtalk") && purpose != "maintenance", SupportsPagination: p != "wecom", RequiresIdentityMapping: p == "wecom", Available: true}
		if p != "web" {
			b, e := s.Channels.GetProvider(ctx, w, actor, a, p)
			if e != nil {
				v.Available = false
				v.UnavailableReason = "channel_unavailable"
			} else if !b.Enabled || !b.IdentityConfirmed {
				v.Available = false
				v.UnavailableReason = "channel_identity_unavailable"
			}
		}
		out = append(out, v)
	}
	return out, nil
}
func personRef(b ChannelBinding, kind, id string) SelectionRef {
	return SelectionRef{Kind: kind, Provider: b.Provider, WorkspaceID: b.WorkspaceID, AgentID: b.AgentID, BindingID: b.ID, TenantID: b.TenantID, Namespace: PeopleIdentityNamespace(b, kind), IdentityEpoch: b.IdentityEpoch, Generation: b.Generation, ExternalID: id}
}
func (s *PeopleSelectorService) Search(ctx context.Context, w, actor, a string, q PeopleQuery) (PeoplePage, error) {
	if q.Purpose == "maintenance" && (q.Kind != "user" || q.DepartmentID != "" || q.ParentID != "") {
		return PeoplePage{}, Fail("invalid_people_query", 400, "维护人只能选择个人")
	}
	empty := PeoplePage{Items: []PeopleCandidate{}}
	q.Q = strings.TrimSpace(q.Q)
	if !validPeoplePurpose(q.Purpose) || (q.Kind != "user" && q.Kind != "department") || q.Limit < 1 || q.Limit > 100 || utf8.RuneCountInString(q.Q) > 100 || len(q.Cursor) > 4096 || strings.ContainsAny(q.Q+q.ParentID+q.DepartmentID, "\x00\r\n") {
		return empty, Fail("invalid_people_query", 400, "人员查询参数无效")
	}
	if e := s.authorize(ctx, w, actor, a); e != nil {
		return empty, e
	}
	if q.Provider == "web" {
		if q.Kind != "user" || q.ParentID != "" || q.DepartmentID != "" || q.SnapshotVersion != 0 {
			return empty, Fail("selector_capability_unsupported", 400, "Web不支持部门查询")
		}
		return s.Store.SearchWebPeople(ctx, w, actor, a, q)
	}
	if q.Provider == "wecom" {
		return s.searchWecom(ctx, w, actor, a, q)
	}
	if q.Provider != "feishu" && q.Provider != "dingtalk" {
		return empty, Fail("invalid_selector_provider", 400, "渠道无效")
	}
	return s.searchDirectory(ctx, w, actor, a, q)
}

func (s *PeopleSelectorService) searchDirectory(ctx context.Context, w, actor, a string, q PeopleQuery) (PeoplePage, error) {
	empty := PeoplePage{Items: []PeopleCandidate{}}
	b, e := s.Channels.directoryBinding(ctx, w, actor, a, q.Provider)
	if e != nil {
		return empty, e
	}
	scope := peoplePageScope(w, actor, a, q)
	native, e := s.openPageCursor(scope, q.Cursor)
	if e != nil {
		return empty, e
	}
	page, e := s.Channels.DirectoryPageProvider(ctx, w, actor, a, q.Provider, DirectoryQuery{Kind: q.Kind + "s", Q: q.Q, ParentID: q.ParentID, DepartmentID: q.DepartmentID, Limit: q.Limit, Cursor: native, SnapshotVersion: q.SnapshotVersion})
	if e != nil {
		return empty, e
	}
	cursor, e := s.sealPageCursor(scope, page.NextCursor)
	if e != nil {
		return empty, e
	}
	out := PeoplePage{Items: []PeopleCandidate{}, NextCursor: cursor, SnapshotVersion: page.SnapshotVersion}
	switch items := page.Items.(type) {
	case []DirectoryUser:
		for _, u := range items {
			v := PeopleCandidate{Ref: personRef(b, "user", u.ID), DisplayName: u.DisplayName, Status: u.Status, Selectable: u.Status != "inactive" && (q.Purpose != "maintenance" || u.Status == "active")}
			if !v.Selectable {
				v.DisabledReason = "person_inactive"
			}
			out.Items = append(out.Items, v)
		}
	case []DirectoryDepartment:
		for _, d := range items {
			out.Items = append(out.Items, PeopleCandidate{Ref: personRef(b, "department", d.ID), DisplayName: d.DisplayName, ParentID: d.ParentID, Status: "active", Selectable: true})
		}
	}
	// Prevent late directory responses from returning refs for a replaced binding.
	now, e := s.Channels.GetProvider(ctx, w, actor, a, q.Provider)
	if e != nil {
		return empty, e
	}
	if now.ID != b.ID || now.IdentityEpoch != b.IdentityEpoch || now.Generation != b.Generation {
		return empty, Fail("selector_source_changed", 409, "渠道来源已变化")
	}
	return out, nil
}
func (s *PeopleSelectorService) searchWecom(ctx context.Context, w, actor, a string, q PeopleQuery) (PeoplePage, error) {
	out := PeoplePage{Items: []PeopleCandidate{}}
	if q.Kind != "user" || q.Cursor != "" || q.ParentID != "" || q.DepartmentID != "" || q.SnapshotVersion != 0 {
		return out, Fail("selector_capability_unsupported", 400, "企微仅支持搜索选人及绑定验证")
	}
	b, e := s.Wecom.binding(ctx, w, actor, a)
	if e != nil {
		return out, e
	}
	v, e := s.Wecom.Search(ctx, w, actor, a, q.Q)
	if e != nil {
		return out, e
	}
	out.Limited = v.Limited
	for _, u := range v.Items {
		c := PeopleCandidate{Ref: personRef(b, "user", u.CallbackUserID), DisplayName: u.DisplayName, Status: "unknown", SearchUserID: u.SearchUserID, VerificationStatus: u.VerificationStatus, VerificationID: u.VerificationID, DepartmentPaths: u.DepartmentPaths, Selectable: u.VerificationStatus == "verified" && u.CallbackUserID != ""}
		if !c.Selectable {
			c.DisabledReason = "identity_mapping_required"
		}
		out.Items = append(out.Items, c)
	}
	now, e := s.Wecom.binding(ctx, w, actor, a)
	if e != nil {
		return PeoplePage{}, e
	}
	if now.ID != b.ID || now.IdentityEpoch != b.IdentityEpoch || now.Generation != b.Generation {
		return PeoplePage{}, Fail("selector_source_changed", 409, "渠道来源已变化")
	}
	return out, nil
}
func (s *PeopleSelectorService) Resolve(ctx context.Context, w, actor, a, purpose string, refs []SelectionRef) (PeoplePage, error) {
	if purpose == "maintenance" {
		for _, ref := range refs {
			if ref.Kind != "user" {
				return PeoplePage{}, Fail("invalid_people_refs", 400, "维护人只能选择个人")
			}
		}
	}
	out := PeoplePage{Items: []PeopleCandidate{}}
	if !validPeoplePurpose(purpose) || refs == nil || len(refs) > 200 {
		return out, Fail("invalid_people_refs", 400, "人员引用无效")
	}
	if e := s.authorize(ctx, w, actor, a); e != nil {
		return out, e
	}
	for _, r := range refs {
		if r.WorkspaceID != w || r.AgentID != a || r.ExternalID == "" || len(r.ExternalID) > 512 || (r.Kind != "user" && r.Kind != "department") {
			return out, Fail("invalid_people_refs", 400, "人员引用范围无效")
		}
		v, e := s.Store.ResolvePerson(ctx, w, actor, a, r)
		if e != nil {
			return out, e
		}
		if purpose == "maintenance" && v.Status != "active" && v.VerificationStatus != "verified" {
			v.Selectable = false
			v.DisabledReason = "person_unavailable"
		}
		out.Items = append(out.Items, v)
	}
	return out, nil
}

// PeopleIdentityNamespace distinguishes department IDs from user IDs even in one binding.
func PeopleIdentityNamespace(b ChannelBinding, kind string) string {
	if kind == "department" {
		if b.Provider == "feishu" {
			return "open_department_id"
		}
		return "department_id"
	}
	return b.UserNamespace
}
func (s *PeopleSelectorService) ResolveSaved(ctx context.Context, w, actor, a, purpose string, selectors []UsageSelector) (PeoplePage, error) {
	out := PeoplePage{Items: []PeopleCandidate{}}
	if !validPeoplePurpose(purpose) || selectors == nil || len(selectors) > 200 {
		return out, Fail("invalid_people_refs", 400, "人员引用无效")
	}
	if e := s.authorize(ctx, w, actor, a); e != nil {
		return out, e
	}
	for _, v := range selectors {
		if !validUsageSelector(v) || (v.Kind != "user" && v.Kind != "department") {
			return out, Fail("invalid_people_refs", 400, "人员引用无效")
		}
		r := SelectionRef{Kind: v.Kind, Provider: v.Channel, WorkspaceID: w, AgentID: a, ExternalID: v.ID}
		if v.Channel != "web" {
			b, e := s.Channels.GetProvider(ctx, w, actor, a, v.Channel)
			if e != nil {
				return out, e
			}
			if b.ID != v.BindingID || b.IdentityEpoch != v.IdentityEpoch {
				r.BindingID = v.BindingID
				r.IdentityEpoch = v.IdentityEpoch
				out.Items = append(out.Items, PeopleCandidate{Ref: r, DisplayName: v.ID, Status: "unavailable", DisabledReason: "selector_source_changed"})
				continue
			}
			r = personRef(b, v.Kind, v.ID)
		}
		item, e := s.Store.ResolvePerson(ctx, w, actor, a, r)
		if e != nil {
			return out, e
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}
