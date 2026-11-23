package runner

import (
	"errors"
	"path/filepath"
)

// Owner selects a persistent working directory, not an operating-system security
// boundary. All commands still execute with the Runner account's permissions.
func (s scope) validOwner() bool {
	if s.TaskSpaceID != "" {
		return s.ConversationID == "" && validID(s.TaskSpaceID) && validID(s.TaskID)
	}
	return validID(s.ConversationID) && s.TaskID == ""
}
func (s scope) sameOwner(other scope) bool {
	return s.ID == other.ID && s.WorkspaceID == other.WorkspaceID && s.AgentID == other.AgentID && s.EngineID == other.EngineID && s.ConversationID == other.ConversationID && s.TaskSpaceID == other.TaskSpaceID && s.TaskID == other.TaskID
}
func (s scope) directory(root string) (string, error) {
	if !filepath.IsAbs(root) || !validID(s.WorkspaceID) || !validID(s.AgentID) || !s.validOwner() {
		return "", errors.New("invalid sandbox directory owner")
	}
	base := filepath.Join(root, "workspaces", s.WorkspaceID, "projects")
	if s.TaskSpaceID != "" {
		return filepath.Join(base, "task-"+s.TaskID), nil
	}
	return filepath.Join(base, "conv-"+s.ConversationID), nil
}
func (s scope) validateSessions() error {
	seen := map[string]bool{}
	for _, session := range s.Sessions {
		if !validID(session.SessionID) || seen[session.SessionID] {
			return errors.New("invalid or duplicated sandbox session")
		}
		seen[session.SessionID] = true
		if s.TaskSpaceID != "" && (session.TaskID != s.TaskID || session.TaskSpaceID != s.TaskSpaceID || session.ConversationID != "") {
			return errors.New("sandbox task/session owner mismatch")
		}
		if s.TaskSpaceID == "" && (session.TaskSpaceID != "" || session.ConversationID != "" && session.ConversationID != s.ConversationID) {
			return errors.New("sandbox conversation/session owner mismatch")
		}
	}
	return nil
}
