package repository

import (
	"context"
	"database/sql"
	"io/fs"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func verifyOfficeChat(t *testing.T, ctx context.Context, db *sql.DB, runtime *service.RuntimeService, w, actor, model, base, root string, call func(string, string, any, string) map[string]any) {
	t.Helper()
	c, e := service.BuiltinCatalog()
	if e != nil {
		t.Fatal(e)
	}
	if e = SyncCatalog(ctx, db, c, runtime.Skills.Assets); e != nil {
		t.Fatal(e)
	}
	var tid, v string
	if e = db.QueryRow("SELECT id,published_version_id FROM platform_agent_templates WHERE builtin_key='business-analyst'").Scan(&tid, &v); e != nil {
		t.Fatal(e)
	}
	s := &service.TemplateService{Store: &TemplateStore{DB: db}, Assets: runtime.Skills.Assets}
	employee, e := s.CreateFromTemplate(ctx, w, actor, service.TemplateCreation{TemplateID: tid, TemplateVersionID: v, Name: "经营数据分析师", ModelProfileID: model})
	if e != nil {
		t.Fatal(e)
	}
	conversation := call("POST", base+"/conversations/web-direct", map[string]string{"agent_id": employee.ID}, "")["id"].(string)
	prompt := "请安排一次后台对账。订单明细为 A01 100 元、A02 200 元、A03 300 元。实际回款为 A01 100 元、A02 180 元、A03 300 元。按订单核对后，请交付可编辑的 Excel 明细和两页中文 PPT。PPT 简要列出应收合计、实收合计、差额以及差异订单。请直接生成两份实际文件，在原对话告诉我结果。"
	chat := call("POST", base+"/sessions", map[string]any{"conversation_id": conversation, "mode": "chat", "input": map[string]string{"text": prompt}}, service.NewID())["id"].(string)
	for deadline := time.Now().Add(7 * time.Minute); time.Now().Before(deadline); time.Sleep(time.Second) {
		var reported bool
		if e = db.QueryRowContext(ctx, `SELECT EXISTS(SELECT FROM manager_task_reports r JOIN manager_inputs i ON i.id=r.target_input_id JOIN manager_turns t ON t.input_id=i.id WHERE r.target_session_id=$1 AND t.status='completed')`, chat).Scan(&reported); e != nil {
			t.Fatal(e)
		}
		if !reported {
			continue
		}
		var sheets, slides []string
		if e = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() {
				switch strings.ToLower(filepath.Ext(path)) {
				case ".xlsx":
					sheets = append(sheets, path)
				case ".pptx":
					slides = append(slides, path)
				}
			}
			return nil
		}); e != nil {
			t.Fatal(e)
		}
		for _, sheet := range sheets {
			for _, slide := range slides {
				check := exec.CommandContext(ctx, "python3", "-c", `import sys
from openpyxl import load_workbook
from pptx import Presentation
book=load_workbook(sys.argv[1])
cells=[str(c.value) for sh in book for row in sh for c in row if c.value is not None]
assert all(v in cells for v in ["A01","A02","A03","180"]), "order details missing"
deck=Presentation(sys.argv[2])
assert len(deck.slides)==2, "expected two slides"
text=" ".join((s.text if s.has_text_frame else " ".join(c.text for row in s.table.rows for c in row.cells) if s.has_table else "") for slide in deck.slides for s in slide.shapes)
assert all(v in text for v in ["600","580","20","A02"]), "presentation figures mismatch"
assert any("\u4e00" <= char <= "\u9fff" for char in text), "missing Chinese text"
`, sheet, slide)
				if check.Run() == nil {
					t.Logf("main chat=%s verified workbook=%s presentation=%s", chat, filepath.Base(sheet), filepath.Base(slide))
					return
				}
			}
		}
		t.Fatalf("report delivered without two verified Office artifacts: workbooks=%d presentations=%d", len(sheets), len(slides))
	}
	t.Fatal("Office report did not return", chat)
}
