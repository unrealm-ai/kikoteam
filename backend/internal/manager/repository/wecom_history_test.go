package repository

import (
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"testing"
)

func TestWecomHistoryRequiresQueryLocalReferences(t *testing.T) {
	base := service.ChannelHistoryItem{RecordKey: "page:0:record:1", SenderID: "history-id", SenderName: "历史成员", SenderType: "unknown", Text: "相同文本", CreatedAtMS: 99000}
	if err := validateHistorySnapshotItems([]service.ChannelHistoryItem{base}, "wecom", "trigger", "previous", 100000, 99500); err != nil {
		t.Fatal("query-local recent window must not reuse Feishu previous event bound", err)
	}
	for _, mode := range []string{"fake_provider_id", "empty_record_key", "trusted_sender", "identity_namespace", "duplicate_record_key", "too_old"} {
		t.Run(mode, func(t *testing.T) {
			v := base
			items := []service.ChannelHistoryItem{v}
			switch mode {
			case "fake_provider_id":
				v.MessageID = "invented"
			case "empty_record_key":
				v.RecordKey = ""
			case "trusted_sender":
				v.SenderType = "user"
			case "identity_namespace":
				v.SenderIDType = "user_id"
			case "duplicate_record_key":
				items = append(items, v)
			case "too_old":
				v.CreatedAtMS = 1
			}
			items[0] = v
			cutoff := int64(100000)
			if mode == "too_old" {
				cutoff = 8 * 86400000
			}
			if err := validateHistorySnapshotItems(items, "wecom", "trigger", "previous", cutoff, 99500); err == nil {
				t.Fatal("invalid history accepted")
			}
		})
	}
	other := base
	other.RecordKey = "page:0:record:2"
	if err := validateHistorySnapshotItems([]service.ChannelHistoryItem{base, other}, "wecom", "trigger", "previous", 100000, 99500); err != nil {
		t.Fatal("duplicate text should be preserved", err)
	}
	if err := validateHistorySnapshotItems([]service.ChannelHistoryItem{base}, "feishu", "trigger", "previous", 100000, 0); err == nil {
		t.Fatal("query-local reference crossed provider boundary")
	}
	if got := channelHistoryInputText(base); got != "[说话人=历史成员]相同文本" {
		t.Fatal(got)
	}
}
