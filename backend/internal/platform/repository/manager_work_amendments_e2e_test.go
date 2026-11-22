package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

const (
	baseReportCriteria = "报表包含日期和订单数"
	refundColumn       = "报表新增退款率列，按退款订单数除以订单总数计算"
	refundReport       = "日期,订单数,退款率\n2026-11-21,100,5%"
)

type workAmendmentCase struct {
	name                   string
	workerReady, chatReady chan struct{}
	submit, amend          chan struct{}
	stale                  chan struct{}
}

type workAmendmentRuntime struct {
	employee string
	cases    []*workAmendmentCase
}

func newWorkAmendmentRuntime(employee string) *workAmendmentRuntime {
	f := &workAmendmentRuntime{employee: employee}
	for _, name := range []string{"AMEND-QUEUE", "AMEND-RACE", "AMEND-SUBMIT-FIRST"} {
		f.cases = append(f.cases, &workAmendmentCase{name: name,
			workerReady: make(chan struct{}, 1), chatReady: make(chan struct{}, 1),
			submit: make(chan struct{}), amend: make(chan struct{}), stale: make(chan struct{}, 1)})
	}
	return f
}

type amendmentPrompt struct {
	Criteria     string                    `json:"criteria"`
	Summary      string                    `json:"summary"`
	Requirements string                    `json:"requirements_version"`
	Employee     struct{ ID, Name string } `json:"current_employee"`
}

type amendmentModelItem struct {
	ID           string                    `json:"id"`
	Version      string                    `json:"version"`
	Requirements string                    `json:"requirements_version"`
	Criteria     string                    `json:"criteria"`
	Result       string                    `json:"result"`
	Employee     struct{ ID, Name string } `json:"current_employee"`
}

func (f *workAmendmentRuntime) reply(t *testing.T, w http.ResponseWriter, r *http.Request, req runtimeRequest) bool {
	t.Helper()
	var user, last string
	for _, m := range req.Messages {
		switch m.Role {
		case "user":
			user, last = amendmentMessageText(m.Content), ""
		case "tool":
			last = amendmentMessageText(m.Content)
		}
	}
	c := f.scenario(user)
	if c == nil {
		return false
	}
	tool := func(name string, args any) {
		t.Helper()
		for _, candidate := range req.Tools {
			if candidate.Function.Name == name {
				humanToolStream(t, w, []string{name}, []any{args})
				return
			}
		}
		t.Errorf("missing amendment fixture tool %s", name)
		w.WriteHeader(http.StatusBadRequest)
	}
	switch {
	case strings.Contains(user, "START-AMENDMENT "):
		if last == "" {
			tool("mcp__task_delegation__task_delegation_create_session", map[string]string{"instruction": "MAKE-REPORT " + c.name})
		} else {
			humanTextStream(t, w, "已安排生成报表")
		}
	case strings.Contains(user, "MAKE-REPORT "):
		tool("work_item_handoff", map[string]string{"target_employee_id": f.employee, "title": "退款报表 " + c.name, "goal": "交付报表", "criteria": baseReportCriteria, "summary": c.name})
	case strings.Contains(user, "CHANGE-AMENDMENT "):
		f.replyChatAmendment(t, w, r, c, tool, user, last)
	case strings.Contains(user, "你收到一个事项处理任务") || strings.Contains(user, "当前事项的要求已更新"):
		f.replyAssignee(t, w, r, c, tool, user, last)
	case strings.Contains(user, "已收到交接事项的新成果"):
		f.replyReviewer(t, w, tool, user, last)
	default:
		return false
	}
	return true
}

func amendmentMessageText(content any) string {
	blocks, ok := content.([]any)
	if !ok {
		return fmt.Sprint(content)
	}
	var text []string
	for _, block := range blocks {
		if part, ok := block.(map[string]any); ok {
			if value, ok := part["text"].(string); ok {
				text = append(text, value)
			}
		}
	}
	return strings.Join(text, "\n")
}

func (f *workAmendmentRuntime) scenario(user string) *workAmendmentCase {
	for _, c := range f.cases {
		if strings.Contains(user, c.name) {
			return c
		}
	}
	return nil
}

func reachAmendmentGate(ctx context.Context, ready chan struct{}, release <-chan struct{}) bool {
	select {
	case ready <- struct{}{}:
	default:
	}
	select {
	case <-release:
		return true
	case <-ctx.Done():
		return false
	}
}

func (f *workAmendmentRuntime) replyChatAmendment(t *testing.T, w http.ResponseWriter, r *http.Request, c *workAmendmentCase, tool func(string, any), user, last string) {
	t.Helper()
	if last == "" {
		_, args, _ := strings.Cut(user, "CHANGE-AMENDMENT ")
		tool("mcp__task_delegation__work_item_get_current", map[string]string{"task_id": strings.Fields(args)[1]})
		return
	}
	var state struct {
		Item  *amendmentModelItem
		Basis string `json:"amendment_basis"`
	}
	if json.Unmarshal([]byte(last), &state) == nil && state.Item != nil {
		if state.Item.Employee.ID != f.employee || state.Item.Employee.Name != "Handoff B" || state.Item.Criteria != baseReportCriteria || state.Item.Requirements != "1" {
			t.Errorf("chat read unexpected work projection: %s", last)
		}
		if reachAmendmentGate(r.Context(), c.chatReady, c.amend) {
			tool("mcp__task_delegation__work_item_amend_requirements", map[string]string{"work_item_id": state.Item.ID, "expected_requirements_version": "1", "amendment_basis": state.Basis, "content": refundColumn, "request_key": "refund-prerequisite"})
		}
		return
	}
	switch {
	case strings.Contains(last, `"delivery":"queued"`):
		humanTextStream(t, w, "退款率要求已入队")
	case strings.Contains(last, "work_amendment_closed"):
		humanTextStream(t, w, "成果已提交，退款率要求未受理")
	default:
		t.Errorf("unexpected chat amendment receipt: %s", last)
		humanTextStream(t, w, "事项修改失败")
	}
}

func (f *workAmendmentRuntime) replyAssignee(t *testing.T, w http.ResponseWriter, r *http.Request, c *workAmendmentCase, tool func(string, any), user, last string) {
	t.Helper()
	if last == "" {
		tool(workGetTool, map[string]string{})
		return
	}
	if strings.Contains(last, "事项版本或当前环节已变化") {
		select {
		case c.stale <- struct{}{}:
		default:
		}
		humanTextStream(t, w, "本轮旧版本未提交")
		return
	}
	var item amendmentModelItem
	if err := json.Unmarshal([]byte(last), &item); err != nil || item.Version == "" {
		t.Errorf("unexpected assignee result: %s (%v)", last, err)
		humanTextStream(t, w, "接手任务失败")
		return
	}
	result, evidence := "日期,订单数\n2026-11-21,100", "日期和订单数已核对"
	if strings.Contains(user, "当前事项的要求已更新") {
		prompt := decodeAmendmentPrompt(t, user)
		if prompt.Summary != refundColumn || prompt.Employee.ID != f.employee || prompt.Employee.Name != "Handoff B" || prompt.Requirements != "2" ||
			item.Requirements != "2" || !strings.Contains(prompt.Criteria, baseReportCriteria) || !strings.Contains(prompt.Criteria, refundColumn) || prompt.Criteria != item.Criteria {
			t.Errorf("B did not receive the manager's latest employee and criteria: %s / %s", user, last)
		}
		result, evidence = refundReport, "退款订单数 5，订单总数 100，退款率 5%"
	} else if !reachAmendmentGate(r.Context(), c.workerReady, c.submit) {
		return
	}
	tool("work_item_submit_result", map[string]string{"expected_version": item.Version, "result": result, "completion_evidence": evidence})
}

func (f *workAmendmentRuntime) replyReviewer(t *testing.T, w http.ResponseWriter, tool func(string, any), user, last string) {
	t.Helper()
	if last == "" {
		tool(workGetTool, map[string]string{})
		return
	}
	if strings.Contains(last, `"status":"completed"`) {
		humanTextStream(t, w, "报表已验收")
		return
	}
	var item amendmentModelItem
	err := json.Unmarshal([]byte(last), &item)
	prompt := decodeAmendmentPrompt(t, user)
	if err != nil || item.Version == "" || prompt.Requirements != item.Requirements || prompt.Criteria != item.Criteria || prompt.Employee.ID != f.employee {
		t.Errorf("A saw inconsistent review criteria or submitter: %s / %s (%v)", user, last, err)
	}
	evidence := "核对日期与订单数"
	if item.Requirements == "2" {
		if !strings.Contains(prompt.Criteria, refundColumn) || !strings.Contains(prompt.Criteria, baseReportCriteria) || item.Result != refundReport {
			t.Errorf("A did not review the amended report and both criteria: %s / %s", user, last)
		}
		evidence = "核对日期、订单数和退款率 5%"
	}
	tool(workAcceptTool, map[string]string{"expected_version": item.Version, "completion_evidence": evidence})
}

func decodeAmendmentPrompt(t *testing.T, user string) amendmentPrompt {
	t.Helper()
	var prompt amendmentPrompt
	_, payload, _ := strings.Cut(user, "\n")
	if err := json.Unmarshal([]byte(payload), &prompt); err != nil {
		t.Errorf("could not read Manager's task input: %s (%v)", user, err)
	}
	return prompt
}

func testRuntimeWorkAmendments(t *testing.T, ctx context.Context, db *sql.DB, sessionPath string, call workRuntimeCall, wait func(func() bool), f *workAmendmentRuntime) {
	t.Helper()
	for _, c := range f.cases {
		t.Run(c.name, func(t *testing.T) {
			testRuntimeWorkAmendment(t, ctx, db, sessionPath, call, wait, c)
		})
	}
}

func testRuntimeWorkAmendment(t *testing.T, ctx context.Context, db *sql.DB, path string, call workRuntimeCall, wait func(func() bool), c *workAmendmentCase) {
	t.Helper()
	t.Cleanup(func() {
		for _, gate := range []chan struct{}{c.submit, c.amend} {
			select {
			case <-gate:
			default:
				close(gate)
			}
		}
	})
	call("POST", path+"/inputs", map[string]string{"text": "START-AMENDMENT " + c.name, "delivery_mode": "queue"}, "start-"+c.name)
	awaitAmendmentGate(t, c.workerReady, "B did not read the original version")
	var item, task, assignee string
	err := db.QueryRowContext(ctx, `SELECT w.id,s.task_id,a.session_id FROM manager_work_items w
 JOIN manager_work_assignments a ON a.id=w.current_assignment_id JOIN manager_sessions s ON s.id=w.root_session_id
 WHERE w.title=$1`, "退款报表 "+c.name).Scan(&item, &task, &assignee)
	if err != nil {
		t.Fatal(err)
	}
	chatInput := call("POST", path+"/inputs", map[string]string{"text": "CHANGE-AMENDMENT " + c.name + " " + task + " " + refundColumn, "delivery_mode": "queue"}, "change-"+c.name)["id"].(string)
	awaitAmendmentGate(t, c.chatReady, "chat Agent did not find the delegated work item")
	switch c.name {
	case "AMEND-QUEUE":
		close(c.amend)
		wait(func() bool {
			var pending bool
			return db.QueryRowContext(ctx, `SELECT EXISTS(SELECT FROM manager_work_amendments m JOIN manager_inputs i ON i.id=m.input_id
 WHERE m.work_item_id=$1 AND m.source_input_id=$2 AND m.requirements_version=2 AND i.session_id=$3 AND i.status='queued' AND i.delivery_mode='queue')`, item, chatInput, assignee).Scan(&pending) == nil && pending
		})
		close(c.submit)
	case "AMEND-SUBMIT-FIRST":
		close(c.submit)
		wait(func() bool {
			var accepted bool
			return db.QueryRowContext(ctx, `SELECT EXISTS(SELECT FROM manager_work_boundaries WHERE work_item_id=$1 AND kind='submit')`, item).Scan(&accepted) == nil && accepted
		})
		close(c.amend)
	default:
		close(c.submit)
		close(c.amend)
	}
	wait(func() bool {
		var completed bool
		return db.QueryRowContext(ctx, `SELECT status='completed' FROM manager_work_items WHERE id=$1`, item).Scan(&completed) == nil && completed &&
			call("GET", path+"/inputs/"+chatInput, nil, "")["status"] == "processed"
	})
	assertAmendedWorkResult(t, ctx, db, item, c)
}

func awaitAmendmentGate(t *testing.T, ready <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-ready:
	case <-time.After(70 * time.Second):
		t.Fatal(message)
	}
}

func assertAmendedWorkResult(t *testing.T, ctx context.Context, db *sql.DB, item string, c *workAmendmentCase) {
	t.Helper()
	var version, submitted, amendments, submissions, received int
	var evidence string
	err := db.QueryRowContext(ctx, `SELECT w.requirements_version,b.requirements_version,w.acceptance_evidence,
 (SELECT count(*) FROM manager_work_amendments WHERE work_item_id=w.id),
 (SELECT count(*) FROM manager_work_boundaries WHERE work_item_id=w.id AND kind='submit'),
 (SELECT count(*) FROM manager_work_amendments m JOIN manager_inputs i ON i.id=m.input_id
  WHERE m.work_item_id=w.id AND i.status='processed' AND i.context_state='committed')
 FROM manager_work_items w JOIN manager_work_boundaries b ON b.id=w.current_submission_id WHERE w.id=$1`, item).
		Scan(&version, &submitted, &evidence, &amendments, &submissions, &received)
	if err != nil || submitted != version || submissions != 1 || amendments != version-1 || received != amendments {
		t.Fatalf("inconsistent amendment outcome: version=%d submitted=%d amendments=%d submissions=%d received=%d error=%v", version, submitted, amendments, submissions, received, err)
	}
	if c.name == "AMEND-QUEUE" && version != 2 || c.name == "AMEND-SUBMIT-FIRST" && version != 1 {
		t.Fatalf("unexpected winning requirement version: %d", version)
	}
	t.Logf("accepted requirements version %d, queued amendments received %d", version, received)
	if version == 2 {
		awaitAmendmentGate(t, c.stale, "B's old submit was not rejected before it reached a boundary")
		if !strings.Contains(evidence, "退款率 5%") {
			t.Fatalf("A did not record v2 acceptance: %s", evidence)
		}
	}
}
