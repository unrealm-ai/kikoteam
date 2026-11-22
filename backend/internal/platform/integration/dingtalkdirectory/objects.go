package dingtalkdirectory

import (
	"encoding/json"
	"reflect"
	"sort"
	"strconv"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

// Department identifiers are canonical safe integers.
type departmentID string

func (d *departmentID) UnmarshalJSON(b []byte) error {
	s := string(b)
	if len(b) > 0 && b[0] == '"' {
		if json.Unmarshal(b, &s) != nil {
			return failure("directory_department_id_invalid", false)
		}
	}
	n, e := strconv.ParseUint(s, 10, 53)
	if e != nil || n > 9007199254740991 || strconv.FormatUint(n, 10) != s {
		return failure("directory_department_id_invalid", false)
	}
	*d = departmentID(s)
	return nil
}

type userWire struct {
	ID          string          `json:"userid"`
	Manager     json.RawMessage `json:"manager_userid"`
	Union       json.RawMessage `json:"unionid"`
	Name        string          `json:"name"`
	Active      *bool           `json:"active"`
	Departments *[]departmentID `json:"dept_id_list"`
}

func contains(v []string, id string) bool {
	for _, s := range v {
		if s == id {
			return true
		}
	}
	return false
}
func (r *run) putUser(w userWire, listed string) error {
	if !validID(w.ID) || w.Name == "" {
		return failure("directory_user_invalid", false)
	}
	u := service.DirectoryUser{ID: w.ID, DisplayName: w.Name, Status: "unknown", DepartmentIDs: []string{}, MembershipsKnown: w.Departments != nil}
	if err := r.userMetadata(w, &u); err != nil {
		return err
	}
	if w.Active != nil && *w.Active {
		u.Status = "active"
	} // false is activation, not proof of offboarding.
	if w.Departments != nil {
		for _, id := range *w.Departments {
			if !contains(u.DepartmentIDs, string(id)) {
				u.DepartmentIDs = append(u.DepartmentIDs, string(id))
			}
		}
		sort.Strings(u.DepartmentIDs)
		if listed != "" && !contains(u.DepartmentIDs, listed) {
			return failure("directory_membership_conflict", false)
		}
	} else if listed != "" {
		u.DepartmentIDs = []string{listed}
	}
	if old, ok := r.users[u.ID]; ok {
		if err := mergeUserMetadata(old, &u); err != nil {
			return err
		}
		if old.DisplayName != u.DisplayName || old.Status != u.Status {
			return failure("directory_user_conflict", false)
		}
		if old.MembershipsKnown && u.MembershipsKnown && !reflect.DeepEqual(old.DepartmentIDs, u.DepartmentIDs) {
			return failure("directory_membership_conflict", false)
		}
		if old.MembershipsKnown {
			for _, id := range u.DepartmentIDs {
				if !contains(old.DepartmentIDs, id) {
					return failure("directory_membership_conflict", false)
				}
			}
			u.DepartmentIDs = old.DepartmentIDs
			u.MembershipsKnown = true
		} else if u.MembershipsKnown {
			for _, id := range old.DepartmentIDs {
				if !contains(u.DepartmentIDs, id) {
					return failure("directory_membership_conflict", false)
				}
			}
		} else {
			for _, id := range old.DepartmentIDs {
				if !contains(u.DepartmentIDs, id) {
					u.DepartmentIDs = append(u.DepartmentIDs, id)
				}
			}
			sort.Strings(u.DepartmentIDs)
		}
	}
	r.users[u.ID] = u
	if len(r.users) > 100000 {
		return failure("directory_object_budget", false)
	}
	return nil
}
func (r *run) user(id string) error {
	var v struct {
		User userWire `json:"result"`
	}
	if e := r.api("/topapi/v2/user/get", map[string]any{"userid": id, "language": "zh_CN"}, &v); e != nil {
		return e
	}
	if v.User.ID != id {
		return failure("directory_user_identity_mismatch", false)
	}
	return r.putUser(v.User, "")
}

type departmentWire struct {
	ID      departmentID    `json:"dept_id"`
	Leaders json.RawMessage `json:"dept_manager_userid_list"`
	Name    string          `json:"name"`
	Parent  json.RawMessage `json:"parent_id"`
}

// Only department detail for the canonical root can omit its parent.
func (w departmentWire) parentID(rootDetail bool) (string, error) {
	if len(w.Parent) == 0 && rootDetail {
		return "", nil
	}
	if len(w.Parent) == 0 || string(w.Parent) == "null" {
		return "", failure("directory_department_invalid", false)
	}
	var parent departmentID
	if err := json.Unmarshal(w.Parent, &parent); err != nil {
		return "", failure("directory_invalid_response", false)
	}
	if w.ID == "1" {
		if parent == "0" || parent == "1" {
			return "", nil
		}
		return "", failure("directory_department_invalid", false)
	}
	return string(parent), nil
}

func (w departmentWire) value(rootDetail bool) (service.DirectoryDepartment, error) {
	if w.ID == "" || w.Name == "" {
		return service.DirectoryDepartment{}, failure("directory_department_invalid", false)
	}
	parent, err := w.parentID(rootDetail)
	if err != nil {
		return service.DirectoryDepartment{}, err
	}
	d := service.DirectoryDepartment{ID: string(w.ID), DisplayName: w.Name, ParentID: parent, LeaderIDs: []string{}, LeadersKnown: len(w.Leaders) > 0}
	if d.LeadersKnown {
		if string(w.Leaders) == "null" || json.Unmarshal(w.Leaders, &d.LeaderIDs) != nil || len(d.LeaderIDs) > 1000 {
			return d, failure("directory_department_leaders_invalid", false)
		}
		for _, id := range d.LeaderIDs {
			if !validID(id) {
				return d, failure("directory_department_leaders_invalid", false)
			}
		}
		sort.Strings(d.LeaderIDs)
		d.LeaderIDs = uniqueIDs(d.LeaderIDs)
	}
	return d, nil
}
func (r *run) department(id string, path map[string]bool) error {
	if path[id] || len(path) >= 100 {
		return failure("directory_department_cycle", false)
	}
	if _, ok := r.departments[id]; ok {
		return nil
	}
	path[id] = true
	defer delete(path, id)
	number, e := strconv.ParseInt(id, 10, 64)
	if e != nil {
		return failure("directory_department_id_invalid", false)
	}
	var detail struct {
		Department departmentWire `json:"result"`
	}
	if e = r.api("/topapi/v2/department/get", map[string]any{"dept_id": number, "language": "zh_CN"}, &detail); e != nil {
		return e
	}
	d, e := detail.Department.value(id == "1" && detail.Department.ID == "1")
	if e != nil {
		return e
	}
	if d.ID != id {
		return failure("directory_department_identity_mismatch", false)
	}
	r.departments[id] = d
	if len(r.departments) > 100000 {
		return failure("directory_object_budget", false)
	}
	if e = r.members(id, number); e != nil {
		return e
	}
	var children struct {
		Items *[]departmentWire `json:"result"`
	}
	if e = r.api("/topapi/v2/department/listsub", map[string]any{"dept_id": number, "language": "zh_CN"}, &children); e != nil {
		return e
	}
	if children.Items == nil || len(*children.Items) > 10000 {
		return failure("directory_children_invalid", false)
	}
	seen := map[string]bool{}
	for _, w := range *children.Items {
		child, e := w.value(false)
		if e != nil {
			return e
		}
		if child.ParentID != id || seen[child.ID] {
			return failure("directory_department_conflict", false)
		}
		seen[child.ID] = true
		if e = r.department(child.ID, path); e != nil {
			return e
		}
		detail := r.departments[child.ID]
		if detail.ID != child.ID || detail.ParentID != child.ParentID || detail.DisplayName != child.DisplayName || (detail.LeadersKnown && child.LeadersKnown && !reflect.DeepEqual(detail.LeaderIDs, child.LeaderIDs)) {
			return failure("directory_department_conflict", false)
		}
		if child.LeadersKnown && !detail.LeadersKnown {
			detail.LeaderIDs = child.LeaderIDs
			detail.LeadersKnown = true
			r.departments[child.ID] = detail
		}
	}
	return nil
}
func (r *run) members(id string, number int64) error {
	cursor := "0"
	seen := map[string]bool{}
	for {
		if seen[cursor] {
			return failure("directory_cursor_loop", false)
		}
		seen[cursor] = true
		n, e := strconv.ParseInt(cursor, 10, 64)
		if e != nil {
			return failure("directory_cursor_invalid", false)
		}
		var v struct {
			Result *struct {
				List *[]userWire   `json:"list"`
				More *bool         `json:"has_more"`
				Next *departmentID `json:"next_cursor"`
			} `json:"result"`
		}
		if e = r.api("/topapi/v2/user/list", map[string]any{"dept_id": number, "cursor": n, "size": 100, "order_field": "custom", "language": "zh_CN"}, &v); e != nil {
			return e
		}
		if v.Result == nil || v.Result.More == nil || v.Result.List == nil || len(*v.Result.List) > 100 {
			return failure("directory_members_invalid", false)
		}
		ids := map[string]bool{}
		for _, w := range *v.Result.List {
			if ids[w.ID] {
				return failure("directory_user_duplicate", false)
			}
			ids[w.ID] = true
			if e = r.putUser(w, id); e != nil {
				return e
			}
		}
		if !*v.Result.More {
			return nil
		}
		if v.Result.Next == nil {
			return failure("directory_cursor_invalid", false)
		}
		cursor = string(*v.Result.Next)
	}
}
