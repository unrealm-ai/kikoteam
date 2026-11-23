package runner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func testScope() scope {
	return scope{ID: uuid(), WorkspaceID: uuid(), AgentID: uuid(), EngineID: uuid(), ConversationID: uuid(), Lifecycle: "active"}
}
func TestOwnerDirectories(t *testing.T) {
	root := t.TempDir()
	conv := testScope()
	first, e := conv.directory(root)
	if e != nil {
		t.Fatal(e)
	}
	if first != filepath.Join(root, "workspaces", conv.WorkspaceID, "projects", "conv-"+conv.ConversationID) {
		t.Fatal(first)
	}
	// Child task identity belongs to Sessions, never the conversation sandbox owner.
	conv.Sessions = []sessionScope{{SessionID: uuid(), ConversationID: conv.ConversationID, TaskID: uuid()}}
	if err := conv.validateSessions(); err != nil {
		t.Fatal(err)
	}
	ambiguous := conv
	ambiguous.TaskID = uuid()
	if _, err := ambiguous.directory(root); err == nil {
		t.Fatal("ambiguous conversation/task owner accepted")
	}
	task := conv
	task.ConversationID = ""
	task.TaskSpaceID = uuid()
	task.TaskID = uuid()
	one, e := task.directory(root)
	if e != nil {
		t.Fatal(e)
	}
	task.TaskID = uuid()
	two, e := task.directory(root)
	if e != nil || two == one {
		t.Fatal("task projects aliased", two, e)
	}
	for _, dir := range []string{one, two} {
		if e = os.MkdirAll(dir, 0700); e != nil {
			t.Fatal(e)
		}
	}
	if e = os.WriteFile(filepath.Join(one, "result"), []byte("first task"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(two, "result")); !os.IsNotExist(e) {
		t.Fatal("task inherited another default directory", e)
	}
	again, e := task.directory(root)
	if e != nil || again != two {
		t.Fatal("unstable recovery path", again, e)
	}
}
func TestInvalidOwnerAndSessionScopes(t *testing.T) {
	valid := testScope()
	task := valid
	task.ConversationID = ""
	task.TaskSpaceID = uuid()
	task.TaskID = uuid()
	for name, edit := range map[string]func(*scope){"both": func(s *scope) { s.ConversationID = uuid() }, "no task": func(s *scope) { s.TaskID = "" }, "path traversal": func(s *scope) { s.TaskSpaceID = "../other" }, "no owner": func(s *scope) { s.TaskSpaceID = "" }, "workspace traversal": func(s *scope) { s.WorkspaceID = "../other" }} {
		t.Run(name, func(t *testing.T) {
			v := task
			edit(&v)
			if _, err := v.directory(t.TempDir()); err == nil {
				t.Fatal("invalid scope accepted")
			}
		})
	}
	id := uuid()
	task.Sessions = []sessionScope{{SessionID: id, TaskID: task.TaskID, TaskSpaceID: task.TaskSpaceID}}
	if e := task.validateSessions(); e != nil {
		t.Fatal(e)
	}
	for name, edit := range map[string]func(*sessionScope){"foreign task": func(s *sessionScope) { s.TaskID = uuid() }, "foreign space": func(s *sessionScope) { s.TaskSpaceID = uuid() }, "both owners": func(s *sessionScope) { s.ConversationID = uuid() }, "missing task": func(s *sessionScope) { s.TaskID = "" }} {
		t.Run(name, func(t *testing.T) {
			v := task
			v.Sessions = append([]sessionScope{}, task.Sessions...)
			edit(&v.Sessions[0])
			if v.validateSessions() == nil {
				t.Fatal("cross-task session accepted")
			}
		})
	}
	task.Sessions = append(task.Sessions, task.Sessions[0])
	if task.validateSessions() == nil {
		t.Fatal("duplicate sessions accepted")
	}
}
func TestFetchPinsOwnerAcrossConfigurationRefresh(t *testing.T) {
	initial := testScope()
	initial.ConversationID = ""
	initial.TaskSpaceID = uuid()
	initial.TaskID = uuid()
	current := initial
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing config credential")
		}
		if err := json.NewEncoder(w).Encode(current); err != nil {
			t.Error(err)
		}
	}))
	defer backend.Close()
	runner := &server{config: Config{BackendURL: backend.URL, ConfigToken: "test-token", SandboxID: initial.ID, EngineID: initial.EngineID}, backend: backend.Client()}
	got, err := runner.fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	runner.scope = got
	current.TaskID = uuid()
	if _, err = runner.fetch(context.Background()); err == nil {
		t.Fatal("same sandbox id silently changed task")
	}
	current = initial
	current.TaskSpaceID = uuid()
	if _, err = runner.fetch(context.Background()); err == nil {
		t.Fatal("same sandbox id silently changed space")
	}
	current = initial
	current.TaskSpaceID = ""
	current.TaskID = ""
	current.ConversationID = uuid()
	if _, err = runner.fetch(context.Background()); err == nil {
		t.Fatal("same sandbox changed owner kind")
	}
	current = initial
	current.Lifecycle = "archived"
	if _, err = runner.fetch(context.Background()); err != nil {
		t.Fatal("lifecycle update blocked", err)
	}
}

// Checks default working-directory separation, not an OS security boundary.
func TestTaskSpaceBashWorkingDirectoriesAndRestart(t *testing.T) {
	root := t.TempDir()
	owner := testScope()
	owner.ConversationID = ""
	owner.TaskSpaceID = uuid()
	owner.TaskID = uuid()
	run := func(s scope, script string) {
		t.Helper()
		dir, err := s.directory(root)
		if err != nil {
			t.Fatal(err)
		}
		dir = filepath.Join(dir, "project")
		if err = os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("/bin/bash", "-c", script)
		cmd.Dir = dir
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("bash %v %s", err, output)
		}
	}
	run(owner, "printf first > artifact.txt")
	sibling := owner
	sibling.TaskID = uuid()
	run(sibling, "test ! -e artifact.txt; printf second > artifact.txt")
	run(owner, "test \"$(cat artifact.txt)\" = first")
	run(sibling, "test \"$(cat artifact.txt)\" = second")
}
