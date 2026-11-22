package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

type TriggerWebhookConfig struct {
	Adapter        string `json:"adapter"`
	RepositoryID   int64  `json:"repositoryId,omitempty"`
	InstallationID *int64 `json:"installationId,omitempty"`
}

func ValidGithubID(n int64) bool { return n > 0 && n <= 9007199254740991 }
func GithubTriggerCommand(raw []byte, event, delivery string, c TriggerWebhookConfig) (TriggerCommand, bool, error) {
	var out TriggerCommand
	bad := func() (TriggerCommand, bool, error) {
		return out, false, Fail("invalid_github_issue", 400, "GitHub事件无效")
	}
	if event != "issues" || !ValidID(delivery) || !utf8.Valid(raw) || len(raw) > 256*1024 {
		return bad()
	}
	var v struct {
		Action     string `json:"action"`
		Repository struct {
			ID int64 `json:"id"`
		} `json:"repository"`
		Installation *struct {
			ID int64 `json:"id"`
		} `json:"installation"`
		Issue struct {
			ID     int64   `json:"id"`
			Number int64   `json:"number"`
			Title  string  `json:"title"`
			Body   *string `json:"body"`
		} `json:"issue"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return bad()
	}
	if !ValidGithubID(v.Repository.ID) || !ValidGithubID(v.Issue.ID) || !ValidGithubID(v.Issue.Number) || len(v.Action) < 1 || len(v.Action) > 32 || strings.TrimSpace(v.Issue.Title) == "" || utf8.RuneCountInString(v.Issue.Title) > 1024 {
		return bad()
	}
	for _, r := range v.Action {
		if r != '_' && (r < 'a' || r > 'z') {
			return bad()
		}
	}
	if v.Repository.ID != c.RepositoryID || c.InstallationID != nil && (v.Installation == nil || v.Installation.ID != *c.InstallationID) {
		return out, false, Fail("trigger_source_forbidden", 403, "GitHub来源不匹配")
	}
	if v.Issue.Body != nil && utf8.RuneCountInString(*v.Issue.Body) > 64000 {
		return bad()
	}
	if v.Action != "opened" {
		return out, true, nil
	}
	description := fmt.Sprintf("GitHub Issue #%d: %s\n", v.Issue.Number, v.Issue.Title)
	if v.Issue.Body != nil {
		description += *v.Issue.Body
	}
	if utf8.RuneCountInString(description) > 8000 {
		return out, false, Fail("trigger_description_too_large", 400, "任务描述过长")
	}
	key := fmt.Sprintf("[%d,%d]", v.Repository.ID, v.Issue.Number)
	out = TriggerCommand{Version: 1, EventID: fmt.Sprintf("github-issue-opened-%d-%d", v.Repository.ID, v.Issue.ID), Action: "create_task", Description: description, BusinessKey: &key}
	return out, false, ValidateTriggerCommand(out)
}
