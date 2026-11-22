package feishudirectory

import (
	"encoding/json"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"net/url"
	"reflect"
	"sort"
)

type userWire struct {
	ID          string    `json:"open_id"`
	Name        string    `json:"name"`
	Departments *[]string `json:"department_ids"`
	Status      *struct {
		Frozen    *bool `json:"is_frozen"`
		Resigned  *bool `json:"is_resigned"`
		Exited    *bool `json:"is_exited"`
		Unjoin    *bool `json:"is_unjoin"`
		Activated *bool `json:"is_activated"`
	} `json:"status"`
}

func yes(v *bool) bool { return v != nil && *v }
func no(v *bool) bool  { return v != nil && !*v }
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
	if w.Departments != nil {
		var err error
		u.DepartmentIDs, err = ids(*w.Departments)
		if err != nil {
			return err
		}
		if listed != "" && !contains(u.DepartmentIDs, listed) {
			return failure("directory_membership_conflict", false)
		}
	} else if listed != "" {
		u.DepartmentIDs = []string{listed}
	}
	if s := w.Status; s != nil {
		if yes(s.Frozen) || yes(s.Resigned) || yes(s.Exited) || yes(s.Unjoin) {
			u.Status = "inactive"
		} else if no(s.Frozen) && no(s.Resigned) && no(s.Exited) && yes(s.Activated) {
			u.Status = "active"
		}
	}
	if prior, ok := r.users[u.ID]; ok {
		if prior.DisplayName != u.DisplayName || (prior.Status != "unknown" && u.Status != "unknown" && prior.Status != u.Status) {
			return failure("directory_user_conflict", false)
		}
		if u.Status == "unknown" {
			u.Status = prior.Status
		}
		if prior.MembershipsKnown && u.MembershipsKnown && !reflect.DeepEqual(prior.DepartmentIDs, u.DepartmentIDs) {
			return failure("directory_membership_conflict", false)
		}
		if prior.MembershipsKnown {
			for _, id := range u.DepartmentIDs {
				if !contains(prior.DepartmentIDs, id) {
					return failure("directory_membership_conflict", false)
				}
			}
			u.DepartmentIDs = prior.DepartmentIDs
			u.MembershipsKnown = true
		} else if u.MembershipsKnown {
			for _, id := range prior.DepartmentIDs {
				if !contains(u.DepartmentIDs, id) {
					return failure("directory_membership_conflict", false)
				}
			}
		} else {
			for _, id := range prior.DepartmentIDs {
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
	var data struct {
		User userWire `json:"user"`
	}
	if err := r.get("/open-apis/contact/v3/users/"+url.PathEscape(id), params(), &data); err != nil {
		return err
	}
	if data.User.ID != id {
		return failure("directory_user_identity_mismatch", false)
	}
	return r.putUser(data.User, "")
}

type departmentWire struct {
	ID     string  `json:"open_department_id"`
	Name   string  `json:"name"`
	Parent *string `json:"parent_department_id"`
}

func departmentValue(w departmentWire) (service.DirectoryDepartment, error) {
	if !validID(w.ID) || w.Name == "" || w.Parent == nil || !validID(*w.Parent) {
		return service.DirectoryDepartment{}, failure("directory_department_invalid", false)
	}
	if w.ID == *w.Parent && w.ID != "0" {
		return service.DirectoryDepartment{}, failure("directory_department_cycle", false)
	}
	parent := *w.Parent
	if w.ID == "0" && parent == "0" {
		parent = ""
	}
	return service.DirectoryDepartment{ID: w.ID, DisplayName: w.Name, ParentID: parent}, nil
}
func (r *run) department(id string, path map[string]bool) error {
	if path[id] || len(path) >= 100 {
		return failure("directory_department_cycle", false)
	}
	if _, done := r.departments[id]; done {
		return nil
	}
	var data struct {
		Department departmentWire `json:"department"`
	}
	if err := r.get("/open-apis/contact/v3/departments/"+url.PathEscape(id), params(), &data); err != nil {
		return err
	}
	if data.Department.ID != id {
		return failure("directory_department_identity_mismatch", false)
	}
	d, err := departmentValue(data.Department)
	if err != nil {
		return err
	}
	r.departments[id] = d
	if len(r.departments) > 100000 {
		return failure("directory_object_budget", false)
	}
	q := params()
	q.Set("department_id", id)
	seenUsers := map[string]bool{}
	if err = r.pages("/open-apis/contact/v3/users/find_by_department", q, func(raw json.RawMessage) error {
		var p struct {
			Items *[]userWire `json:"items"`
		}
		if json.Unmarshal(raw, &p) != nil || p.Items == nil || len(*p.Items) > 50 {
			return failure("directory_members_invalid", false)
		}
		for _, u := range *p.Items {
			if seenUsers[u.ID] {
				return failure("directory_duplicate_member", false)
			}
			seenUsers[u.ID] = true
			if err := r.putUser(u, id); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	children := []departmentWire{}
	seen := map[string]bool{}
	q = params()
	q.Set("fetch_child", "false")
	if err = r.pages("/open-apis/contact/v3/departments/"+url.PathEscape(id)+"/children", q, func(raw json.RawMessage) error {
		var p struct {
			Items json.RawMessage `json:"items"`
			More  bool            `json:"has_more"`
		}
		if json.Unmarshal(raw, &p) != nil {
			return failure("directory_children_invalid", false)
		}
		var rows []departmentWire
		if len(p.Items) == 0 && !p.More {
			rows = []departmentWire{}
		} else if string(p.Items) == "null" || json.Unmarshal(p.Items, &rows) != nil || len(rows) > 50 {
			return failure("directory_children_invalid", false)
		}
		for _, v := range rows {
			d, err := departmentValue(v)
			if err != nil {
				return err
			}
			if d.ParentID != id || seen[d.ID] {
				return failure("directory_department_conflict", false)
			}
			seen[d.ID] = true
			children = append(children, v)
		}
		return nil
	}); err != nil {
		return err
	}
	path[id] = true
	defer delete(path, id)
	for _, child := range children {
		if err := r.department(child.ID, path); err != nil {
			return err
		}
		expected, err := departmentValue(child)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(r.departments[child.ID], expected) {
			return failure("directory_department_conflict", false)
		}
	}
	return nil
}
