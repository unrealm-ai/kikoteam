package channel

import (
	"encoding/json"
	"slices"
	"testing"

	larkevent "github.com/larksuite/oapi-sdk-go/v3/event"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func normalizeFeishuPostFixture(t *testing.T, chatType, kind, raw string, mentionsBot bool) (Inbound, bool) {
	t.Helper()
	binding := Binding{ID: "binding", Generation: 9, TenantID: "tenant", BotOpenID: "bot", Trigger: Trigger{Mention: true, Keywords: []string{"release"}}}
	binding.Credentials.AppID = "app"
	filter, err := Compile(binding.Trigger)
	if err != nil {
		t.Fatal(err)
	}
	message := &larkim.EventMessage{MessageId: ptr("fixture-message"), ChatId: ptr("chat"), ChatType: ptr(chatType), MessageType: ptr(kind), Content: ptr(raw)}
	if mentionsBot {
		message.Mentions = []*larkim.MentionEvent{{Id: &larkim.UserId{OpenId: ptr("bot")}}}
	}
	event := &larkim.P2MessageReceiveV1{
		EventV2Base: &larkevent.EventV2Base{Header: &larkevent.EventHeader{EventID: "fixture-event", AppID: "app", TenantKey: "tenant"}},
		Event: &larkim.P2MessageReceiveV1Data{
			Sender:  &larkim.EventSender{SenderId: &larkim.UserId{OpenId: ptr("sender")}, SenderType: ptr("user")},
			Message: message,
		},
	}
	got, matched, err := normalize(binding, filter, event)
	if err != nil {
		t.Fatal(err)
	}
	if got.MessageID != "fixture-message" || got.BindingID != binding.ID || got.TenantID != "tenant" || got.BotMentioned != mentionsBot {
		t.Fatal("original and authenticated event scope was not preserved")
	}
	return got, matched
}

type feishuPostMediaItem struct{ kind, key, name string }

func assertFeishuPostMedia(t *testing.T, got Inbound, want ...feishuPostMediaItem) {
	t.Helper()
	var actual []feishuPostMediaItem
	for _, ref := range got.Media {
		var locator mediaLocator
		if err := json.Unmarshal([]byte(ref.Locator), &locator); err != nil || locator.AESKey != "" {
			t.Fatal("unexpected Feishu message-resource locator")
		}
		actual = append(actual, feishuPostMediaItem{kind: ref.Kind, key: locator.Key, name: ref.Name})
	}
	if !slices.Equal(actual, want) {
		t.Fatalf("media=%+v want=%+v", actual, want)
	}
}

func TestFeishuPostCaptionInlineImageAndRootFiles(t *testing.T) {
	const raw = `{
		"title":"发版材料",
		"content":[[{"tag":"text","text":"old"}, {"tag":"img","image_key":"img_old"},{"tag":"file","file_key":"file_old"}]],
		"content_v2":[[{"tag":"text","text":"release notes","style":["bold"]},{"tag":"img","image_key":"img_colored"}],[{"tag":"file","file_key":"file_inline"}]],
		"files":[
			{"file_key":"file_inline","file_name":"原始.pdf","is_folder":false},
			{"file_key":"file_folder","is_folder":true},
			{"file_key":"file_malformed_folder","is_folder":"false"},
			{"file_key":"file_null_folder","is_folder":null},
			{"file_key":"file_number_folder","is_folder":0},
			"not a record",null,{},{"file_key":17},{"file_key":"   "},{"file_key":"bad\u0000key"},
			{"file_key":"file_root1","file_name":"../报告.csv","is_folder":false},
			{"file_key":"file_root1","file_name":"duplicate.csv","is_folder":false},
			{"file_key":"file_root2","file_name":"目录\\预算.xlsx"}
		]
	}`
	got, matched := normalizeFeishuPostFixture(t, "group", "post", raw, false)
	if !matched || got.Text != "# 发版材料\n**release notes**[图片附件未读取]\n[未解析内容类型=\"file\"]" {
		t.Fatalf("caption, inline placeholders or keyword routing changed: %q, match=%v", got.Text, matched)
	}
	assertFeishuPostMedia(t, got,
		feishuPostMediaItem{"image", "img_colored", ""},
		feishuPostMediaItem{"file", "file_inline", "原始.pdf"},
		feishuPostMediaItem{"file", "file_root1", "报告.csv"},
		feishuPostMediaItem{"file", "file_root2", "预算.xlsx"},
	)
	if sealed, err := sealMedia(got, "fixture"); err != nil || len(sealed.Media) != 4 || sealed.Media[1].Locator == got.Media[1].Locator {
		t.Fatal("mixed post did not use the original sealed message-resource path")
	}
}

func TestFeishuPostLocaleSelectedAndRootFilesRetained(t *testing.T) {
	const raw = `{
		"en_us":{"title":"wrong","content_v2":[[{"tag":"text","text":"wrong"},{"tag":"img","image_key":"other_locale"}]]},
		"zh_cn":{"title":"中文","content":[[{"tag":"text","text":"old"},{"tag":"file","file_key":"old"}]], "content_v2":[[{"tag":"text","text":"新版"},{"tag":"img","image_key":"chosen"}]]},
		"files":[{"file_key":"file_root","file_name":"清单.csv","is_folder":false}]
	}`
	got, matched := normalizeFeishuPostFixture(t, "p2p", "post", raw, false)
	if !matched || got.Text != "# 中文\n新版[图片附件未读取]" {
		t.Fatalf("locale or content version changed: %q, match=%v", got.Text, matched)
	}
	assertFeishuPostMedia(t, got,
		feishuPostMediaItem{"image", "chosen", ""},
		feishuPostMediaItem{"file", "file_root", "清单.csv"},
	)
}

func TestFeishuFilesOnlyPostRequiresExistingGroupTrigger(t *testing.T) {
	const raw = `{"title":"","content":[],"content_v2":[],"files":[{"file_key":"file_without_name","is_folder":false}]}`
	for _, tc := range []struct {
		chat                 string
		mention, wantMatched bool
	}{{"p2p", false, true}, {"group", false, false}, {"group", true, true}} {
		got, matched := normalizeFeishuPostFixture(t, tc.chat, "post", raw, tc.mention)
		if got.Text != "" || matched != tc.wantMatched {
			t.Fatalf("group trigger or empty caption changed: %q, match=%v", got.Text, matched)
		}
		assertFeishuPostMedia(t, got, feishuPostMediaItem{"file", "file_without_name", ""})
	}
}

func TestFeishuPostIgnoresBadRootArrayAndKeepsStandaloneMedia(t *testing.T) {
	got, matched := normalizeFeishuPostFixture(t, "p2p", "post",
		`{"title":"","content":[[{"tag":"text","text":"正文"},{"tag":"file","file_key":"file_inline","file_name":"inline.pdf"}]],"files":{"file_key":"invalid_container"}}`, false)
	if !matched || got.Text != "正文[未解析内容类型=\"file\"]" {
		t.Fatalf("malformed root files discarded caption: %q, match=%v", got.Text, matched)
	}
	assertFeishuPostMedia(t, got, feishuPostMediaItem{"file", "file_inline", "inline.pdf"})
	for _, tc := range []struct {
		kind, raw string
		want      feishuPostMediaItem
	}{
		{"image", `{"image_key":"img_standalone"}`, feishuPostMediaItem{"image", "img_standalone", ""}},
		{"file", `{"file_key":"file_standalone","file_name":"独立文件.docx"}`, feishuPostMediaItem{"file", "file_standalone", "独立文件.docx"}},
	} {
		got, matched := normalizeFeishuPostFixture(t, "p2p", tc.kind, tc.raw, false)
		if got.Text != "" || !matched {
			t.Fatalf("standalone %s handling changed: %q, match=%v", tc.kind, got.Text, matched)
		}
		assertFeishuPostMedia(t, got, tc.want)
	}
}
