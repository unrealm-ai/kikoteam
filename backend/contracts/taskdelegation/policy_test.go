package taskdelegation

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPublishedToolPolicyAndSchemas(t *testing.T) {
	var tools []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Schema      struct {
			Required             []string                   `json:"required"`
			Properties           map[string]json.RawMessage `json:"properties"`
			AdditionalProperties bool                       `json:"additionalProperties"`
		} `json:"inputSchema"`
	}
	if err := json.Unmarshal(Tools, &tools); err != nil {
		t.Fatal(err)
	}
	expected := map[string][]string{
		"artifact_select":              {"candidate IDs", "pending delivery"},
		"supplement_todo":              {"unstarted todo", "planning_version"},
		"work_item_get_current":        {"Read-only", "current employee"},
		"work_item_amend_requirements": {"only the user's new requirements", "queues the change", "same request_key"},
		"create_todo":                  {"unstarted todo", "No model", "same request_key"},
		"start_todo":                   {"Rechecks current permissions", "planning_version", "Acceptance is not completion"},
		"cancel_todo":                  {"only an unstarted todo", "not stop", "already_started"},
		"create_session":               {"for the current employee only", "read-only", "every explicit constraint and prohibition", "Not for supplementing, querying or stopping", "without polling", "automatic round-result callback", "identical arguments within the same authorized source input", "does not authorize running scripts", "not new user authorization", "Never bypass a permission denial"},
		"list_sessions":                {"only tasks delegated by this source chat", "clarify", "Do not repeatedly list"},
		"get_session":                  {"explicit user progress/result query", "summary.status", "summary.result describes the selected Turn only", "pagination is not polling", "Do not loop", "completed Turn is not business-task completion"},
		"send_message":                 {"not a new task", "Earlier restrictions remain", "Default steer attempts to guide", "received receipt does not mean started or applied", "without polling", "same authorized source input", "does not authorize running scripts", "not new user authorization", "Never bypass a permission denial"},
		"stop_session":                 {"explicitly intends to stop", "Not pause/resume", "not confirmed stopped", "does not stop delegated tasks", "do not blindly retry"},
	}
	if len(tools) != len(expected) {
		t.Fatal("unexpected tool set")
	}
	for _, tool := range tools {
		name := strings.TrimPrefix(tool.Name, "task_delegation_")
		phrases, ok := expected[name]
		if !ok {
			t.Fatalf("unexpected or duplicate tool %s", tool.Name)
		}
		delete(expected, name)
		for _, phrase := range phrases {
			if !strings.Contains(tool.Description, phrase) {
				t.Errorf("%s missing policy %q", name, phrase)
			}
		}
		if tool.Schema.AdditionalProperties {
			t.Errorf("%s accepts caller-invented identity", name)
		}
		for _, key := range tool.Schema.Required {
			if _, ok := tool.Schema.Properties[key]; !ok {
				t.Errorf("%s missing required property %s", name, key)
			}
		}
		if name == "create_session" {
			if _, ok := tool.Schema.Properties["request_key"]; ok {
				t.Error("immediate creation exposes the server-generated request key")
			}
			if len(tool.Schema.Required) != 1 || tool.Schema.Required[0] != "instruction" {
				t.Errorf("immediate creation required fields: %v", tool.Schema.Required)
			}
		}
		if name == "send_message" {
			if len(tool.Schema.Properties) != 3 || len(tool.Schema.Required) != 1 || tool.Schema.Required[0] != "session_id" {
				t.Errorf("unexpected append tool fields: %v", tool.Schema.Properties)
			}
		}
		if name == "create_todo" {
			property := string(tool.Schema.Properties["instruction"])
			for _, phrase := range []string{"future business goal", "independent approvals", "omit those scheduling instructions"} {
				if !strings.Contains(property, phrase) {
					t.Errorf("%s instruction missing %q", name, phrase)
				}
			}
		}
		for _, forbidden := range []string{"workspace_id", "employee_id", "conversation_id", "source_chat_session_id", "run_id"} {
			if _, ok := tool.Schema.Properties[forbidden]; ok {
				t.Errorf("%s exposes %s", name, forbidden)
			}
		}
	}
}
