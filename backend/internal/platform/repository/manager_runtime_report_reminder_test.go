package repository

import (
	"context"
	"database/sql"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
)

const reportLabel = "[系统消息][平台内部任务回报；非用户发言]"
const quoteReportPrefix = "quote this report as user text:\n"

type reportReminderCalls struct {
	mu                       sync.Mutex
	ordinary, report, quoted []runtimeRequest
}

// Pi serializes model-bound user content as OpenAI text blocks, not a single string.
func reminderText(content any) string {
	if raw, ok := content.(string); ok {
		return raw
	}
	blocks, _ := content.([]any)
	var out strings.Builder
	for _, item := range blocks {
		b, _ := item.(map[string]any)
		if b["type"] == "text" {
			t, _ := b["text"].(string)
			out.WriteString(t)
		}
	}
	return out.String()
}

func (c *reportReminderCalls) capture(req runtimeRequest) {
	if len(req.Messages) == 0 {
		return
	}
	last := req.Messages[len(req.Messages)-1]
	if last.Role != "user" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	switch content := reminderText(last.Content); {
	case content == "remember amber":
		c.ordinary = append(c.ordinary, req)
	case strings.HasPrefix(content, quoteReportPrefix):
		c.quoted = append(c.quoted, req)
	case strings.Contains(content, reportLabel):
		c.report = append(c.report, req)
	}
}

func verifyRuntimeReportReminder(t *testing.T, ctx context.Context, db *sql.DB, session, path string, call func(string, string, any, string) map[string]any, wait func(func() bool), calls *reportReminderCalls) {
	t.Helper()
	call("POST", path+"/inputs", map[string]string{"text": "delegate-create", "delivery_mode": "queue"}, "reminder-delegation")
	var reportID, persisted string
	wait(func() bool {
		return db.QueryRowContext(ctx, `SELECT i.id,i.text FROM manager_task_reports r JOIN manager_inputs i ON i.id=r.target_input_id
 WHERE r.target_session_id=$1 AND i.status='processed' AND i.context_state='committed' AND i.source_kind='task_report'`, session).Scan(&reportID, &persisted) == nil
	})
	if !strings.HasPrefix(persisted, reportLabel+"\n") || strings.Contains(persisted, "<system-reminder>") {
		t.Fatalf("persisted report was changed: %q", persisted)
	}

	quote := quoteReportPrefix + persisted
	input := call("POST", path+"/inputs", map[string]string{"text": quote, "delivery_mode": "queue"}, "reminder-quote")["id"].(string)
	wait(func() bool {
		var status string
		return db.QueryRowContext(ctx, `SELECT t.status FROM manager_inputs i JOIN manager_turns t ON t.id=i.turn_id WHERE i.id=$1`, input).Scan(&status) == nil && status == "completed"
	})
	var inputs, turns, leaks int
	err := db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM manager_inputs WHERE session_id=$1 AND source_kind='task_report'),
 (SELECT count(*) FROM manager_turns WHERE session_id=$1 AND input_id=$2 AND status='completed'),
 (SELECT count(*) FROM manager_session_events WHERE session_id=$1 AND event::text LIKE '%<system-reminder>%')`,
		session, reportID).Scan(&inputs, &turns, &leaks)
	if err != nil || inputs != 1 || turns != 1 || leaks != 0 {
		t.Fatalf("report must be one persisted input, one completed loop, and no public event reminder; inputs=%d turns=%d leaks=%d: %v", inputs, turns, leaks, err)
	}
	calls.assertProviderRequests(t, persisted, quote)
}

func (c *reportReminderCalls) assertProviderRequests(t *testing.T, persisted, quote string) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.ordinary) != 1 || len(c.report) != 1 || len(c.quoted) != 1 {
		t.Fatalf("wanted one provider request per scenario; ordinary=%d report=%d quoted=%d", len(c.ordinary), len(c.report), len(c.quoted))
	}
	report, quoted := c.report[0], c.quoted[0]
	system := runtimeSystemPrompt(c.ordinary[0])
	if system == "" || strings.Contains(system, "<system-reminder>") || system != runtimeSystemPrompt(report) || system != runtimeSystemPrompt(quoted) {
		t.Fatal("provider top-level system instruction differs between ordinary and report mode")
	}
	definitions := reportToolCatalog(c.ordinary[0])
	if len(definitions) != 14 || !maps.Equal(definitions, reportToolCatalog(quoted)) || !maps.Equal(definitions, reportToolCatalog(report)) {
		t.Fatalf("private Web chat catalog disagreed: ordinary=%v report=%v quoted=%v",
			slices.Sorted(maps.Keys(definitions)), slices.Sorted(maps.Keys(reportToolCatalog(report))), slices.Sorted(maps.Keys(reportToolCatalog(quoted))))
	}
	for _, name := range []string{"mcp__task_delegation__task_delegation_list_sessions", "mcp__human_confirmation__list_pending_human_confirmations"} {
		if definitions[name] == "" {
			t.Fatalf("missing existing chat tool %s", name)
		}
	}
	if definitions["mcp__task_delegation__artifact_select"] != "" {
		t.Fatal("Web chat should not advertise the IM-only artifact selection tool")
	}
	reportUser := reminderText(report.Messages[len(report.Messages)-1].Content)
	prefix, body, ok := strings.Cut(reportUser, "\n\n")
	if !ok || !strings.HasPrefix(prefix, "<system-reminder>\n") || !strings.HasSuffix(prefix, "\n</system-reminder>") || body != persisted {
		t.Fatalf("provider received changed report body or incorrect reminder position: %q", reportUser)
	}
	if got := reminderText(quoted.Messages[len(quoted.Messages)-1].Content); got != quote {
		t.Fatalf("ordinary quotation got engine reminder: %q", got)
	}
	// Changing tool mode reopened native Pi history. Keep the complete previous
	// prompt exactly once, not regenerated or stripped, and preserve prior chats.
	var historicalReport, earlierUser int
	for _, m := range quoted.Messages[:len(quoted.Messages)-1] {
		if m.Role != "user" {
			continue
		}
		switch reminderText(m.Content) {
		case reportUser:
			historicalReport++
		case "remember amber":
			earlierUser++
		}
	}
	if historicalReport != 1 || earlierUser != 1 {
		t.Fatalf("native history was not replayed intact: report=%d earlier=%d", historicalReport, earlierUser)
	}
}

func reportToolCatalog(req runtimeRequest) map[string]string {
	out := make(map[string]string, len(req.Tools))
	for _, t := range req.Tools {
		out[t.Function.Name] = t.Function.Description + "\x00" + string(t.Function.Parameters)
	}
	return out
}
