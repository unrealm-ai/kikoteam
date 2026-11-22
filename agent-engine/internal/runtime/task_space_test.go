package runtime

import (
	"github.com/unrealm-ai/kikoteam/agent-engine/internal/integration"
	"github.com/unrealm-ai/kikoteam/agent-engine/internal/repository"
	"strings"
	"testing"
)

func TestTaskSpaceNativeAndSandboxIsolation(t *testing.T) {
	w := repository.Work{WorkspaceID: NewID(), AgentID: NewID(), TaskSpaceID: NewID(), TaskID: NewID(), Assignment: repository.Assignment{SessionID: NewID()}}
	worker := Worker{Root: t.TempDir()}
	first := worker.nativeDirectory(w)
	if !strings.Contains(first, "/taskspaces/"+w.TaskSpaceID+"/tasks/"+w.TaskID+"/") {
		t.Fatal(first)
	}
	v := integration.Sandbox{WorkspaceID: w.WorkspaceID, AgentID: w.AgentID, TaskSpaceID: w.TaskSpaceID, TaskID: w.TaskID}
	if !sandboxMatches(v, w) {
		t.Fatal("own task sandbox rejected")
	}
	w.TaskID = NewID()
	if sandboxMatches(v, w) || first == worker.nativeDirectory(w) {
		t.Fatal("two tasks share environment")
	}
	w.TaskID = v.TaskID
	w.TaskSpaceID = NewID()
	if sandboxMatches(v, w) {
		t.Fatal("foreign space matched")
	}
	w.TaskSpaceID = v.TaskSpaceID
	w.ConversationID = NewID()
	if sandboxMatches(v, w) {
		t.Fatal("ambiguous owner matched")
	}
	w.TaskSpaceID = ""
	v.TaskSpaceID = ""
	v.TaskID = ""
	v.ConversationID = w.ConversationID
	if !sandboxMatches(v, w) || !strings.Contains(worker.nativeDirectory(w), "/conversations/"+w.ConversationID+"/") {
		t.Fatal("conversation layout changed")
	}
	w.ConversationID = ""
	v.ConversationID = ""
	if sandboxMatches(v, w) {
		t.Fatal("empty owner matched")
	}
}

func TestConfigurationOwnerRejectsBothDirections(t *testing.T) {
	w := repository.Work{WorkspaceID: NewID(), AgentID: NewID(), ConversationID: NewID(), TaskID: NewID(), Mode: "task", Assignment: repository.Assignment{SessionID: NewID()}}
	c := integration.HarnessConfig{WorkspaceID: w.WorkspaceID, AgentID: w.AgentID, ConversationID: w.ConversationID, TaskID: w.TaskID, Mode: w.Mode, SessionID: w.SessionID}
	if !configurationOwnerMatches(w, c) {
		t.Fatal("valid conversation config rejected")
	}
	c.TaskSpaceID = NewID()
	if configurationOwnerMatches(w, c) {
		t.Fatal("conversation config accepted space")
	}
	w.TaskSpaceID = c.TaskSpaceID
	w.ConversationID = ""
	if configurationOwnerMatches(w, c) {
		t.Fatal("space config accepted conversation")
	}
	c.ConversationID = ""
	if !configurationOwnerMatches(w, c) {
		t.Fatal("valid space config rejected")
	}
	c.TaskID = NewID()
	if configurationOwnerMatches(w, c) {
		t.Fatal("foreign task config accepted")
	}
}
