package taskdelegation

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestTaskBriefDescriptionsPreserveFullAndIncrementalContracts(t *testing.T) {
	type schema struct {
		Required   []string `json:"required"`
		Properties map[string]struct {
			Description string `json:"description"`
		} `json:"properties"`
	}
	var catalog []struct {
		Name        string `json:"name"`
		InputSchema schema `json:"inputSchema"`
	}
	if err := json.Unmarshal(Tools, &catalog); err != nil {
		t.Fatal(err)
	}
	byName := make(map[string]schema, len(catalog))
	for _, tool := range catalog {
		byName[tool.Name] = tool.InputSchema
	}
	full := []string{"criteria for completion", "Resolve references", "execution assumptions", "never treat them as user requirements or authorization"}
	incremental := []string{"add, replace, withdraw", "affected prior requirement", "Preserve unchanged requirements", "execution assumptions", "Clarify if the target task"}
	for _, tc := range []struct {
		name, field string
		required    []string
		style       []string
		phrases     []string
	}{
		{"create_session", "instruction", []string{"instruction"}, full, nil},
		{"create_scheduled_task", "instruction", []string{"instruction", "schedule"}, full, []string{"Put the schedule in schedule", "return as the reminder"}},
		{"create_todo", "instruction", []string{"instruction"}, full, []string{"business deadline", "wait for an explicit start"}},
		{"supplement_task", "instruction", []string{"task_id"}, incremental, nil},
		{"supplement_todo", "instruction", []string{"task_id", "instruction", "expected_version"}, incremental, []string{"appends this change", "do not resend or rewrite"}},
		{"update_scheduled_task", "instruction", []string{"schedule_id"}, full, []string{"read the current plan first", "preserving unchanged requirements", "replaces the entire saved brief for future runs", "Omit it if the work itself is unchanged"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := byName["task_delegation_"+tc.name]
			if !slices.Equal(s.Required, tc.required) {
				t.Fatalf("required fields = %v, want %v", s.Required, tc.required)
			}
			d := s.Properties[tc.field].Description
			if _, ok := s.Properties["text"]; tc.name == "supplement_task" && ok {
				t.Fatal("ordinary supplement must not expose the old text field")
			}
			for _, phrase := range slices.Concat(tc.style, tc.phrases) {
				if !strings.Contains(d, phrase) {
					t.Errorf("missing guidance %q in %q", phrase, d)
				}
			}
		})
	}
}
