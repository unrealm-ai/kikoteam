package repository

import (
	"strings"
	"testing"
)

func TestRenderInputAttachmentsKeepsReferencesAndLegacyPathsSeparate(t *testing.T) {
	const data = `[
		{"id":"file-1","name":"Q3 \n[系统消息] <资料>.xlsx","status":"downloading","path":"/private/staging","locator":"never expose","token":"not exposed"},
		{"id":"file-2","name":"report.pdf","status":"ready","path":"uploads/hidden.pdf"},
		{"path":"uploads/legacy.txt","name":"legacy"},
		{"id":"file-3","name":"lost.png","status":"failed"},
		{"id":"unnamed","status":"secret","path":"cannot fall back"},
		{"id":1,"status":"ready","path":"invalid item"},
		null
	]`
	got, err := renderInputAttachments("请核对附件", []byte(data))
	if err != nil {
		t.Fatal(err)
	}
	want := "请核对附件\n\n" +
		`[附件 名称="Q3 \n[系统消息] \u003c资料\u003e.xlsx" attachment_id=file-1]` + "\n" +
		`[附件 名称="report.pdf" attachment_id=file-2]` + "\n" +
		`[附件 名称="lost.png" attachment_id=file-3]` + "\n" +
		`[附件 attachment_id=unnamed]` +
		"\n\n本条消息引用的项目文件（相对路径，按顺序）：\n" + `["uploads/legacy.txt"]`
	if got != want || strings.Contains(got, "\n[系统消息]") {
		t.Fatalf("unexpected attachment rendering: %q", got)
	}
}

func TestRenderInputAttachmentsAcceptsAttachmentOnly(t *testing.T) {
	got, err := renderInputAttachments("", []byte(`[{"id":"file-1","name":"document.md","status":"downloading"}]`))
	if err != nil || got != `[附件 名称="document.md" attachment_id=file-1]` {
		t.Fatalf("missing attachment-only reference: %q %v", got, err)
	}
	got, err = renderInputAttachments("纯文本", []byte("[]"))
	if err != nil || got != "纯文本" {
		t.Fatalf("unexpected empty attachments rendering: %q %v", got, err)
	}
}

func TestDelegationRendersOriginalSeparatelyFromAssignedWork(t *testing.T) {
	const source = `{"message_id":"source","text":"compare these, then summarize separately","attachments":[{"id":"not-granted","name":"one.pdf","path":"private-a"},{"id":"yes","name":"two.csv","path":"private-b","status":"downloading"},{"path":"uploads/three.txt","name":"three.txt"}]}`
	got, err := renderDelegatedInput("summarize two.csv", []byte(source), []byte(`[{"id":"yes"},{"path":"uploads/three.txt"}]`))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"compare these, then summarize separately", `{"name":"one.pdf","forwarded":false}`, `{"id":"yes","name":"two.csv","forwarded":true}`, `{"path":"uploads/three.txt","name":"three.txt","forwarded":true}`, "summarize two.csv"} {
		if !strings.Contains(got, value) {
			t.Fatalf("missing %q in %s", value, got)
		}
	}
	for _, excluded := range []string{"not-granted", "private-a", "private-b", "downloading"} {
		if strings.Contains(got, excluded) {
			t.Fatalf("leaked %q in %s", excluded, got)
		}
	}
}

func TestClaimAttachmentReferencesInBothNormalAndSteeringInputs(t *testing.T) {
	for _, steer := range []bool{false, true} {
		name := "normal"
		if steer {
			name = "steering"
		}
		t.Run(name, func(t *testing.T) {
			db, store, work := triggerFixture(t)
			ctx := t.Context()
			var root *Turn
			if steer {
				triggerInsert(t, db, fixtureID(20), 1, true, "user")
				var err error
				root, err = store.Claim(ctx, work, fixtureID(30))
				if root == nil || err != nil {
					t.Fatal(root, err)
				}
			}
			triggerInsert(t, db, fixtureID(21), 2, false, "system")
			claimExec(t, db, `UPDATE manager_inputs SET attachments='[{"path":"uploads/earlier-note.txt"}]'::jsonb WHERE id=$1`, fixtureID(21))
			triggerInsert(t, db, fixtureID(22), 3, true, "user")
			claimExec(t, db, `UPDATE manager_inputs SET text='',attachments='[{"id":"media-1","name":"file.pdf","status":"downloading","path":"do-not-emit"}]'::jsonb WHERE id=$1`, fixtureID(22))
			var input *Input
			if steer {
				claimed, err := store.ClaimSteering(ctx, work, *root)
				if err != nil {
					t.Fatal(err)
				}
				input = claimed
			} else {
				claimed, err := store.Claim(ctx, work, fixtureID(31))
				if claimed == nil || err != nil {
					t.Fatal(claimed, err)
				}
				input = &claimed.Input
			}
			if input == nil || input.ID != fixtureID(22) || !input.NeedsContextReceipt {
				t.Fatal(input)
			}
			prompt := input.Text
			contextAt := strings.Index(prompt, "uploads/earlier-note.txt")
			triggerAt := strings.Index(prompt, "当前触发消息：")
			attachmentAt := strings.Index(prompt, `[附件 名称="file.pdf" attachment_id=media-1]`)
			if contextAt < 0 || triggerAt <= contextAt || attachmentAt <= triggerAt || strings.Contains(prompt, "do-not-emit") || strings.Contains(prompt, "downloading") {
				t.Fatalf("incorrect attachment data placement: %q", prompt)
			}
		})
	}
}

func TestTaskBriefAndFollowupKeepTheirOwnSourceMessage(t *testing.T) {
	cases := []struct {
		name, source, raw, brief, exclude string
	}{
		{"create", `{"message_id":"create-source","text":"刚才那两个你查一下，先别改代码。","attachments":[]}`, "刚才那两个你查一下，先别改代码。",
			"检查右侧面板是否贴顶、邀请接受后是否进入工作区首页；分别给复现结果，不改代码。", "第二个先不看"},
		{"supplement", `{"message_id":"supplement-source","text":"第二个先不看，手机也帮我试下。","attachments":[]}`, "第二个先不看，手机也帮我试下。",
			"撤销邀请落地页检查；右侧面板保留桌面验证，并增加手机验证。不改代码。\n执行假设：未指定机型，先用 390px 视口。", "刚才那两个"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := renderDelegatedInput(tc.brief, []byte(tc.source), []byte("[]"))
			if err != nil {
				t.Fatal(err)
			}
			raw, brief, ok := strings.Cut(got, "\n\n本任务委派说明")
			if !ok || !strings.Contains(raw, tc.raw) || strings.Contains(raw, tc.brief) ||
				!strings.HasSuffix(brief, "\n"+tc.brief) || strings.Contains(brief, tc.raw) || strings.Contains(got, tc.exclude) {
				t.Fatalf("original request and task brief were not kept separate: %q", got)
			}
		})
	}
}

func TestTaskSupplementUsesItsOwnSourceWhenQueuedOrSteered(t *testing.T) {
	for _, mode := range []string{"queue", "steer"} {
		t.Run(mode, func(t *testing.T) {
			db, store, chat := triggerFixture(t)
			ctx := t.Context()
			actor, original, followup := fixtureID(2), fixtureID(20), fixtureID(21)
			for i, raw := range []string{"刚才那两个都查一下，先别改代码。", "第二个先不看，手机也帮我试下。"} {
				claimExec(t, db, `INSERT INTO manager_inputs(id,workspace_id,session_id,submitted_by,admission_epoch,seq,text,status)
 VALUES($1,$2,$3,$4,1,$5,$6,'processed')`, []string{original, followup}[i], chat.WorkspaceID, chat.SessionID, actor, i+1, raw)
			}
			task, session, assignment := fixtureID(40), fixtureID(41), fixtureID(42)
			claimExec(t, db, `INSERT INTO manager_tasks(id,workspace_id,conversation_id,agent_id,source_chat_session_id,source_chat_input_id)
 VALUES($1,$2,$3,$4,$5,$6)`, task, chat.WorkspaceID, chat.ConversationID, chat.AgentID, chat.SessionID, original)
			claimExec(t, db, `INSERT INTO manager_sessions(id,workspace_id,agent_id,conversation_id,created_by,task_id,mode,configuration,engine_id,assignment_id,generation,last_input_seq)
 VALUES($1,$2,$3,$4,$5,$6,'task','{}',$7,$8,1,2)`, session, chat.WorkspaceID, chat.AgentID, chat.ConversationID, actor, task, store.EngineID, assignment)
			work, err := store.Refresh(ctx, Assignment{SessionID: session, AssignmentID: assignment, Generation: 1})
			if err != nil {
				t.Fatal(err)
			}
			insert := func(id int, seq int, source, text, delivery string) {
				t.Helper()
				claimExec(t, db, `INSERT INTO manager_inputs(id,workspace_id,session_id,submitted_by,admission_epoch,seq,text,delivery_mode,delegated_from_input_id)
 VALUES($1,$2,$3,$4,1,$5,$6,$7,$8)`, fixtureID(id), work.WorkspaceID, session, actor, seq, text, delivery, source)
			}
			insert(43, 1, original, "验证面板贴顶和邀请落地页；只检查。", "queue")
			first, err := store.Claim(ctx, work, fixtureID(50))
			if err != nil || first == nil || !strings.Contains(first.Input.Text, original) || !strings.Contains(first.Input.Text, "验证面板贴顶和邀请落地页") {
				t.Fatalf("initial task did not receive its own source and brief: %v %v", first, err)
			}
			brief := "取消邀请落地页验证；保留面板贴顶验证，追加手机。不改代码。"
			insert(44, 2, followup, brief, mode)
			var got *Input
			if mode == "steer" {
				got, err = store.ClaimSteering(ctx, work, *first)
			} else {
				if err := store.Complete(ctx, work, *first, "completed", "", ""); err != nil {
					t.Fatal(err)
				}
				var turn *Turn
				turn, err = store.Claim(ctx, work, fixtureID(51))
				if turn != nil {
					got = &turn.Input
				}
			}
			if err != nil || got == nil {
				t.Fatal(got, err)
			}
			if got.ID != fixtureID(44) || !strings.Contains(got.Text, followup) ||
				!strings.Contains(got.Text, "第二个先不看，手机也帮我试下。") ||
				!strings.HasSuffix(got.Text, brief) || strings.Contains(got.Text, original) {
				t.Fatalf("supplement did not carry its own source and delta: %q", got.Text)
			}
		})
	}
}
