package taskdelegation

import (
	"encoding/json"
	"strings"
	"testing"

	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func TestChatResultDoesNotExposeAttachmentTransferStatus(t *testing.T) {
	items := []manager.Attachment{
		{ID: "known", Name: "readme.txt", Path: "must-not-leak", Status: "ready", Size: 42},
		{ID: "unnamed", Status: "downloading"},
		{ID: "other", Status: "failed"},
		{Path: "uploads/legacy.txt", Name: "legacy.txt"},
	}
	input := manager.Input{Attachments: items, Status: "queued"}
	for _, result := range []any{input, manager.PlannedTask{Attachments: items, State: "planned"}, map[string]any{"input": &input}} {
		got, err := json.Marshal(chatResult(result))
		if err != nil {
			t.Fatal(err)
		}
		s := string(got)
		for _, forbidden := range []string{"ready", "downloading", "failed", "must-not-leak", `"size_bytes":42`} {
			if strings.Contains(s, forbidden) {
				t.Fatalf("unexpected %q in %s", forbidden, s)
			}
		}
		for _, expected := range []string{`"id":"known"`, `"name":"readme.txt"`, `"id":"unnamed"`, `"path":"uploads/legacy.txt"`} {
			if !strings.Contains(s, expected) {
				t.Fatalf("missing %s in %s", expected, s)
			}
		}
	}
	if items[0].Status != "ready" || items[1].Status != "downloading" {
		t.Fatal("chat projection modified source attachments")
	}
}
