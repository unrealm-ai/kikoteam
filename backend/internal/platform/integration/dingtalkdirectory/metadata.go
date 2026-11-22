package dingtalkdirectory

import (
	"encoding/json"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"reflect"
)

func uniqueIDs(ids []string) []string {
	out := []string{}
	for _, id := range ids {
		if len(out) == 0 || out[len(out)-1] != id {
			out = append(out, id)
		}
	}
	return out
}
func (r *run) userMetadata(w userWire, u *service.DirectoryUser) error {
	u.Aliases = []service.DirectoryAlias{}
	if len(w.Manager) > 0 {
		var id string
		if string(w.Manager) == "null" || json.Unmarshal(w.Manager, &id) != nil || (id != "" && !validID(id)) {
			return failure("directory_manager_invalid", false)
		}
		u.DirectManagerKnown = true
		if id != "" {
			u.DirectManagerID = &id
		}
	}
	if len(w.Union) > 0 {
		var id string
		if string(w.Union) == "null" || json.Unmarshal(w.Union, &id) != nil || (id != "" && !validID(id)) {
			return failure("directory_alias_invalid", false)
		}
		if id != "" {
			u.Aliases = append(u.Aliases, service.DirectoryAlias{ID: id, Namespace: "union_id", ApplicationID: r.appID})
		}
	}
	return nil
}

// Merge complete and lightweight responses within one sync, never a previous
// snapshot. Explicit empty manager clears an old snapshot on publication.
func mergeUserMetadata(old service.DirectoryUser, u *service.DirectoryUser) error {
	if old.DirectManagerKnown && u.DirectManagerKnown && !reflect.DeepEqual(old.DirectManagerID, u.DirectManagerID) {
		return failure("directory_manager_conflict", false)
	}
	if old.DirectManagerKnown && !u.DirectManagerKnown {
		u.DirectManagerKnown = true
		u.DirectManagerID = old.DirectManagerID
	}
	if len(old.Aliases) > 0 && len(u.Aliases) > 0 && !reflect.DeepEqual(old.Aliases, u.Aliases) {
		return failure("directory_alias_conflict", false)
	}
	if len(u.Aliases) == 0 {
		u.Aliases = old.Aliases
	}
	return nil
}
