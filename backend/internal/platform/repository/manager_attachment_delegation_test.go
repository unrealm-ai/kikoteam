package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func steerDelegatingSource(t *testing.T, db *sql.DB, m *manager.Service, d *manager.Delegation, scope manager.DelegationScope, text string) manager.DelegationScope {
	t.Helper()
	ctx := t.Context()
	input, _, err := m.SubmitInputMode(ctx, scope.WorkspaceID, scope.ActorID, scope.Identity.SessionID, manager.NewID(), text, "steer")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE manager_inputs SET status='started',context_state='committed',turn_id=(SELECT turn_id FROM manager_inputs WHERE id=$2) WHERE id=$1`, input.ID, scope.InputID); err != nil {
		t.Fatal(err)
	}
	next, err := d.Store.DelegationSource(ctx, scope.Identity)
	if err != nil || next.InputID != input.ID {
		t.Fatalf("supplement source: %s %v", next.InputID, err)
	}
	return next
}

func TestDelegatedInputRetainsSourceAndRequiredFilesOnRetry(t *testing.T) {
	db, m, d, scope := plannedFixture(t)
	ctx := t.Context()
	const wrongPath = "outputs/unselected.txt"
	var task string
	for _, op := range []string{"create_session", "send_message"} {
		primary, secondary := manager.NewID(), manager.NewID()
		if op == "send_message" {
			scope = steerDelegatingSource(t, db, m, d, scope, "only compare the two attached documents")
		}
		if _, err := db.ExecContext(ctx, `UPDATE manager_inputs SET attachments=jsonb_build_array(
 jsonb_build_object('id',$2::text,'name','one.pdf','status','ready'),
 jsonb_build_object('id',$3::text,'name','two.csv','status','downloading')) WHERE id=$1`, scope.InputID, primary, secondary); err != nil {
			t.Fatal(err)
		}
		call := func(args manager.DelegationArgs) (any, error) {
			args.TaskID = task
			return d.Call(ctx, scope, op, args)
		}
		reject := func(args manager.DelegationArgs, code, detail string) {
			t.Helper()
			_, err := call(args)
			var problem *manager.Error
			if !errors.As(err, &problem) || problem.Code != code || !strings.Contains(problem.Message, detail) {
				t.Fatalf("%s: want %s with %q, got %v", op, code, detail, err)
			}
		}
		args := manager.DelegationArgs{Instruction: "compare inputs", Text: "apply these changes"}
		reject(args, "attachment_selection_required", "source_files_unused")
		args.AttachmentIDs = []string{secondary, primary}
		args.AttachmentPaths = []string{wrongPath}
		reject(args, "permission_denied", wrongPath)
		args.AttachmentIDs = nil
		reject(args, "attachment_retry_required", secondary)
		args.AttachmentPaths = nil
		args.SourceFilesUnused = true
		reject(args, "attachment_retry_required", secondary)
		args.SourceFilesUnused = false
		args.AttachmentIDs = []string{secondary}
		reject(args, "attachment_retry_required", primary)
		var leaked int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM manager_inputs WHERE delegated_from_input_id=$1`, scope.InputID).Scan(&leaked); err != nil || leaked != 0 {
			t.Fatalf("rejected %s left inputs: %d %v", op, leaked, err)
		}
		if op == "create_session" {
			if err := db.QueryRowContext(ctx, `SELECT count(*) FROM manager_tasks WHERE source_chat_input_id=$1`, scope.InputID).Scan(&leaked); err != nil || leaked != 0 {
				t.Fatalf("rejected create left tasks: %d %v", leaked, err)
			}
		}
		args.AttachmentIDs = []string{secondary, primary}
		type result struct {
			out any
			err error
		}
		results := make(chan result, 2)
		for range 2 {
			go func() {
				out, err := call(args)
				results <- result{out, err}
			}()
		}
		var out any
		for range 2 {
			r := <-results
			if r.err != nil {
				t.Fatalf("concurrent corrected retry: %v", r.err)
			}
			out = r.out
		}
		if op == "create_session" {
			task = *out.(map[string]any)["task_id"].(*string)
		}
		var count int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM manager_inputs WHERE delegated_from_input_id=$1 AND attachments->0->>'id'=$2 AND attachments->1->>'id'=$3`, scope.InputID, secondary, primary).Scan(&count); err != nil || count != 1 {
			t.Fatalf("%s source/grants/replay: %d %v", op, count, err)
		}
	}
	// A new independent operation can deliberately leave both files out.
	if _, err := d.Call(ctx, scope, "create_session", manager.DelegationArgs{Instruction: "unrelated text-only work", SourceFilesUnused: true}); err != nil {
		t.Fatal(err)
	}
	var grants int
	if err := db.QueryRowContext(ctx, `SELECT jsonb_array_length(attachments) FROM manager_inputs WHERE text='unrelated text-only work'`).Scan(&grants); err != nil || grants != 0 {
		t.Fatalf("unrelated task gained files: %d %v", grants, err)
	}
}

func TestMissingAuthorizedProjectFileCannotDisappearFromRetriedTask(t *testing.T) {
	db, m, d, scope := plannedFixture(t)
	ctx := t.Context()
	m.ProjectRoot = t.TempDir()
	if _, err := db.ExecContext(ctx, `UPDATE manager_conversations SET web_user_id=created_by WHERE id=$1`, scope.ConversationID); err != nil {
		t.Fatal(err)
	}
	// The path was referenced in an earlier user input. This source is plain text.
	if _, err := db.ExecContext(ctx, `UPDATE manager_inputs SET attachments='[{"path":"report.csv","name":"report.csv"}]' WHERE id=$1`, scope.InputID); err != nil {
		t.Fatal(err)
	}
	scope = steerDelegatingSource(t, db, m, d, scope, "read the previously attached report")
	args := manager.DelegationArgs{Instruction: "read the previously referenced report", AttachmentPaths: []string{"report.csv"}}
	_, err := d.Call(ctx, scope, "create_session", args)
	var problem *manager.Error
	if !errors.As(err, &problem) || problem.Code != "resource_not_found" || !strings.Contains(problem.Message, "report.csv") {
		t.Fatalf("want the missing project file: %v", err)
	}
	_, err = d.Call(ctx, scope, "create_session", manager.DelegationArgs{Instruction: "use the description instead", SourceFilesUnused: true})
	if !errors.As(err, &problem) || problem.Code != "attachment_retry_required" || !strings.Contains(problem.Message, "attachment_paths: report.csv") {
		t.Fatalf("lost historical path: %v", err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM manager_tasks WHERE source_chat_input_id=$1`, scope.InputID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partially admitted task: %d %v", count, err)
	}
	c, err := m.WebProject(ctx, scope.WorkspaceID, scope.ActorID, scope.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = m.UploadProject(ctx, c, scope.ActorID, "restore-report", "", "report.csv", strings.NewReader("restored")); err != nil {
		t.Fatal(err)
	}
	if _, err = d.Call(ctx, scope, "create_session", args); err != nil {
		t.Fatal(err)
	}
}

func TestDelegationTransfersOnlyAuthorizedSourceAttachmentGrants(t *testing.T) {
	db, m, d, scope := plannedFixture(t)
	ctx := context.Background()
	a := manager.Attachment{ID: manager.NewID(), Name: "a.txt", Status: "ready"}
	b := manager.Attachment{ID: manager.NewID(), Name: "b.txt", Status: "ready"}
	pending := manager.Attachment{ID: manager.NewID(), Name: "pending.txt", Status: "downloading", Path: "should-not-forward"}
	raw, err := json.Marshal([]manager.Attachment{a, b, pending})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE manager_inputs SET attachments=$2 WHERE id=$1`, scope.InputID, raw); err != nil {
		t.Fatal(err)
	}

	other, err := m.CreateConversation(ctx, scope.WorkspaceID, scope.ActorID, manager.CreateConversation{AgentID: scope.AgentID})
	if err != nil {
		t.Fatal(err)
	}
	req := manager.CreateSession{ConversationID: other.ID, Mode: "chat", Input: &struct {
		Text string `json:"text"`
	}{Text: "other chat"}}
	foreign, _, err := m.CreateSession(ctx, scope.WorkspaceID, scope.ActorID, "other-chat-key", req)
	if err != nil {
		t.Fatal(err)
	}
	foreignID := manager.NewID()
	raw, err = json.Marshal([]manager.Attachment{{ID: foreignID, Name: "foreign.txt", Status: "ready"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE manager_inputs SET attachments=$2 WHERE id=$1`, *foreign.InitialInputID, raw); err != nil {
		t.Fatal(err)
	}

	otherActor := manager.NewID()
	insertWorkspaceAccount(t, db, otherActor, "attachment-other@example.test")
	privateID := manager.NewID()
	raw, err = json.Marshal([]manager.Attachment{{ID: privateID, Name: "another actor", Status: "ready"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO manager_inputs(id,workspace_id,session_id,submitted_by,admission_epoch,seq,text,attachments)
    SELECT $2,workspace_id,session_id,$3,admission_epoch,seq+100,'other actor input',$4
    FROM manager_inputs WHERE id=$1`, scope.InputID, manager.NewID(), otherActor, raw); err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{foreignID, privateID} {
		_, err = d.Call(ctx, scope, "create_session", manager.DelegationArgs{Instruction: "do not transfer", AttachmentIDs: []string{id}})
		var failure *manager.Error
		if !errors.As(err, &failure) || failure.Code != "permission_denied" {
			t.Fatalf("want permission denial: %v", err)
		}
	}

	inspect := func(input string, want ...manager.Attachment) {
		t.Helper()
		var saved []byte
		var got []manager.Attachment
		if e := db.QueryRowContext(ctx, `SELECT attachments FROM manager_inputs WHERE id=$1`, input).Scan(&saved); e != nil {
			t.Fatal(e)
		}
		if e := json.Unmarshal(saved, &got); e != nil || !slices.Equal(got, want) {
			t.Fatalf("want %#v got %#v: %v", want, got, e)
		}
	}
	args := manager.DelegationArgs{Instruction: "read files", AttachmentIDs: []string{b.ID, pending.ID, a.ID}}
	v, err := d.Call(ctx, scope, "create_session", args)
	if err != nil {
		t.Fatal(err)
	}
	immediate := v.(map[string]any)
	pending.Path = ""
	inspect(*immediate["input_id"].(*string), b, pending, a)
	v, err = d.Call(ctx, scope, "send_message", manager.DelegationArgs{SessionID: immediate["session_id"].(string), AttachmentIDs: []string{a.ID}})
	if err != nil {
		t.Fatal(err)
	}
	inspect(v.(manager.Input).ID, a)

	v, err = d.Call(ctx, scope, "create_todo", manager.DelegationArgs{RequestKey: "create-refs", Instruction: "read later", AttachmentIDs: []string{a.ID}})
	if err != nil {
		t.Fatal(err)
	}
	todo := v.(manager.PlannedTask)
	v, err = d.Call(ctx, scope, "supplement_todo", manager.DelegationArgs{SessionID: todo.SessionID, ExpectedVersion: todo.Version, RequestKey: "supplement-refs", Instruction: "read both", AttachmentIDs: []string{a.ID, b.ID}})
	if err != nil {
		t.Fatal(err)
	}
	todo = v.(manager.PlannedTask)
	if !slices.Equal(todo.Attachments, []manager.Attachment{a, b}) {
		t.Fatalf("unexpected planned refs: %#v", todo.Attachments)
	}
	v, err = d.Call(ctx, scope, "start_todo", manager.DelegationArgs{SessionID: todo.SessionID, ExpectedVersion: todo.Version, RequestKey: "start-refs"})
	if err != nil {
		t.Fatal(err)
	}
	inspect(*v.(manager.PlannedTask).InitialInputID, a, b)
}
