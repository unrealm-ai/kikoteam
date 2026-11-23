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
