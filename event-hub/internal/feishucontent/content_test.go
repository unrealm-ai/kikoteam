package feishucontent

import (
	"strings"
	"testing"
)

func TestPostRenderingParityWithoutAssets(t *testing.T) {
	raw := `{"zh_cn":{"title":"标题","content":[[{"tag":"text","text":"old"}]],"content_v2":[[{"tag":"at","user_id":"bot","user_name":"员工"},{"tag":"text","text":"粗体","style":["bold","italic","underline","lineThrough"]},{"tag":"a","text":"链接","href":"https://example.test/a"}],[{"tag":"md","text":"md"},{"tag":"code_block","text":"print(1)","language":"python"},{"tag":"emotion","emoji_type":"SMILE"},{"tag":"hr"},{"tag":"img","image_key":"secret-key"},{"tag":"unknown"}]]}}`
	got, err := Render("post", raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# 标题\n@员工<u>~~_**粗体**_~~</u>", "[链接](https://example.test/a)", "\nmd\n```python\nprint(1)\n```\n", "[表情类型=\"SMILE\"]", "[图片附件未读取]", "[未解析内容类型=\"unknown\"]"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, "secret-key") || strings.Contains(got, "old") {
		t.Fatal(got)
	}
}
func TestPostRejectsMalformedAndUnsafeLinks(t *testing.T) {
	for _, raw := range []string{`null`, `{}`, `{"content":null}`, `{"content":[null]}`, `{"content":[[{"tag":"text","text":5}]]}`, `{"content":[[{"tag":"md"}]]}`} {
		if _, err := Render("post", raw); err == nil {
			t.Fatal(raw)
		}
	}
	for _, link := range []string{"javascript:alert(1)", "https://user:pass@example.test/", "file:///etc/passwd"} {
		got, err := Render("post", `{"en_us":{"content":[[{"tag":"a","text":"label","href":"`+link+`"}]]}}`)
		if err != nil || got != "label" {
			t.Fatal(got, err)
		}
	}
	nodes := strings.Repeat(`{"tag":"hr"},`, 10000) + `{"tag":"hr"}`
	if _, err := Render("post", `{"content":[[`+nodes+`]]}`); err == nil {
		t.Fatal("unbounded nodes")
	}
}

func TestTextRetainsExistingEmptySemantics(t *testing.T) {
	for _, raw := range []string{`null`, `{}`, `{"text":null}`} {
		got, err := Render("text", raw)
		if err != nil || got != "" {
			t.Fatal(raw, got, err)
		}
	}
}

func TestPostOnlyIDMentionAndUnresolvedIdentity(t *testing.T) {
	raw := `{"content":[[{"tag":"at","user_id":"ou_bot"},{"tag":"text","text":" hi "},{"tag":"at","user_id":"unknown"}]]}`
	got, err := RenderWithMentions("post", raw, map[string]string{"ou_bot": "@_user_1"})
	if err != nil || got != "@_user_1 hi @unknown" {
		t.Fatal(got, err)
	}
}
