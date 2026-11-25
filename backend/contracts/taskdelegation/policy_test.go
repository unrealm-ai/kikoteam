package taskdelegation

import (
	"encoding/json"
	"slices"
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
		"send_task_detail_link":        {"authorized channel conversation", "exact task_id", "explicitly asks", "once per task", "only the reported task", "No URL is returned", "never construct or guess", "Do not repeat", "does not mean the message was read or delivered"},
		"artifact_select":              {"candidate IDs", "pending delivery"},
		"supplement_todo":              {"unstarted todo", "planning_version"},
		"work_item_get_current":        {"Read-only", "current employee"},
		"trigger_task_get_result":      {"Read-only", "this exact destination group", "ask which one if ambiguous", "exact notified execution"},
		"work_item_amend_requirements": {"only the user's new requirements", "Queued does not mean received or applied", "same basis and content", "no change was applied", "retry once with the returned amendment_basis and unchanged content"},
		"create_todo":                  {"unstarted todo", "No model", "same request_key"},
		"start_todo":                   {"Rechecks current permissions", "planning_version", "Acceptance is not completion"},
		"cancel_todo":                  {"only an unstarted todo", "not stop", "already_started"},
		"create_session":               {"for the current employee only", "read-only", "every explicit constraint and prohibition", "Not for supplementing, querying or stopping", "without polling", "automatic round-result callback", "identical arguments within the same authorized source input", "does not authorize running scripts", "not new user authorization", "Never bypass a permission denial"},
		"list_sessions":                {"only tasks delegated by this source chat", "clarify", "Do not repeatedly list"},
		"get_session":                  {"explicit user progress/result query", "no match was found in the visible scope", "if the read failed", "summary.status", "summary.result describes the selected Turn only", "pagination is not polling", "Do not loop", "completed Turn is not business-task completion"},
		"send_message":                 {"not a new task", "Earlier restrictions remain", "Default steer attempts to guide", "Say briefly what was recorded", "only when the receipt proves it", "does not prove execution started or the change was applied", "Do not poll", "same authorized source input", "does not authorize running scripts", "not new user authorization", "Never bypass a permission denial"},
		"stop_session":                 {"explicitly intends to stop", "Not pause/resume", "not confirmed stopped", "does not stop delegated tasks", "a repeat can stop newer inputs", "only query the same task", "never issue another stop without a new explicit user request", "New running inputs do not establish what happened to the earlier stop"},
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
		if name == "send_task_detail_link" && (len(tool.Schema.Properties) != 2 || len(tool.Schema.Properties["text"]) == 0 || len(tool.Schema.Required) != 1 || tool.Schema.Required[0] != "task_id") {
			t.Errorf("task detail card must take only the exact task_id: %v", tool.Schema)
		}
		if name == "create_session" {
			if _, ok := tool.Schema.Properties["request_key"]; ok {
				t.Error("immediate creation exposes the server-generated request key")
			}
			if len(tool.Schema.Required) != 1 || tool.Schema.Required[0] != "instruction" {
				t.Errorf("immediate creation required fields: %v", tool.Schema.Required)
			}
		}
		if name == "work_item_amend_requirements" {
			want := []string{"amendment_basis", "content"}
			if len(tool.Schema.Properties) != len(want) || !slices.Equal(tool.Schema.Required, want) {
				t.Errorf("amendment must accept only amendment_basis and content: %v", tool.Schema)
			}
		}
		if name == "send_message" {
			wantFields := []string{"task_id", "text", "attachment_paths", "attachment_ids"}
			if len(tool.Schema.Properties) != len(wantFields) {
				t.Errorf("unexpected append tool fields: %v", tool.Schema.Properties)
			}
			for _, field := range wantFields {
				if _, ok := tool.Schema.Properties[field]; !ok {
					t.Errorf("append tool missing property %s", field)
				}
			}
			if len(tool.Schema.Required) != 1 || tool.Schema.Required[0] != "task_id" {
				t.Errorf("unexpected append tool required fields: %v", tool.Schema.Required)
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
		for _, forbidden := range []string{"workspace_id", "employee_id", "conversation_id", "session_id", "source_chat_session_id", "run_id"} {
			if _, ok := tool.Schema.Properties[forbidden]; ok {
				t.Errorf("%s exposes %s", name, forbidden)
			}
		}
	}
}
