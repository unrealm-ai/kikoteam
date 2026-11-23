// Package feishucontent renders untrusted Feishu presentation content, never identity or permissions.
package feishucontent

import (
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// Render keeps rich text ordering but never fetches attachments or follows links.
func Render(kind, raw string) (string, error) { return RenderWithMentions(kind, raw, nil) }

// RenderWithMentions resolves only provider-envelope mention identities; post nodes cannot grant trigger authority.
func RenderWithMentions(kind, raw string, mentions map[string]string) (string, error) {
	if kind == "text" {
		var text struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(raw), &text); err != nil {
			return "", err
		}
		return text.Text, nil
	}
	if kind != "post" {
		return "[不支持的消息类型]", nil
	}
	blocks, title, err := PostBlocks(raw)
	if err != nil {
		return "", err
	}
	var out strings.Builder

	if strings.TrimSpace(title) != "" {
		out.WriteString("# " + title + "\n")
	}
	count := 0
	for i, block := range blocks {
		if block == nil {
			return "", errors.New("invalid_post_paragraph")
		}
		if i > 0 {
			out.WriteByte('\n')
		}
		for _, node := range block {
			count++
			if count > 10000 {
				return "", errors.New("post_structure_limit")
			}
			text, err := renderNode(node, mentions)
			if err != nil {
				return "", err
			}
			out.WriteString(text)
		}
	}
	return out.String(), nil
}
func optional(n map[string]json.RawMessage, k string) string {
	var s string
	if json.Unmarshal(n[k], &s) != nil {
		return ""
	}
	return s
}
func required(n map[string]json.RawMessage, k string) (string, error) {
	var s string
	if string(n[k]) == "null" || json.Unmarshal(n[k], &s) != nil {
		return "", errors.New("invalid_post_node")
	}
	return s, nil
}
func renderNode(n map[string]json.RawMessage, mentions map[string]string) (string, error) {
	tag, err := required(n, "tag")
	if err != nil {
		return "", err
	}
	switch tag {
	case "text":
		text, e := required(n, "text")
		if e != nil {
			return "", e
		}
		var styles []string
		if json.Unmarshal(n["style"], &styles) != nil {
			styles = nil
		}
		for _, s := range []string{"bold", "italic", "lineThrough", "underline"} {
			found := false
			for _, v := range styles {
				if v == s {
					found = true
				}
			}
			if found {
				switch s {
				case "bold":
					text = "**" + text + "**"
				case "italic":
					text = "_" + text + "_"
				case "lineThrough":
					text = "~~" + text + "~~"
				case "underline":
					text = "<u>" + text + "</u>"
				}
			}
		}
		return text, nil
	case "md":
		return required(n, "text")
	case "a":
		href, e := required(n, "href")
		if e != nil {
			return "", e
		}
		text := optional(n, "text")
		u, e := url.Parse(href)
		if e != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
			return text, nil
		}
		return "[" + text + "](" + href + ")", nil
	case "at":
		if resolved := mentions[optional(n, "user_id")]; resolved != "" {
			return resolved, nil
		}
		name := optional(n, "user_name")
		if name == "" {
			name = optional(n, "text")
		}
		if name == "" {
			name = optional(n, "user_id")
			if name == "" {
				name = "提及用户"
			}
		}
		return "@" + name, nil
	case "code_block":
		text, e := required(n, "text")
		return "\n```" + optional(n, "language") + "\n" + text + "\n```\n", e
	case "emotion":
		k := "emoji_type"
		if n[k] == nil {
			k = "type"
		}
		text, e := required(n, k)
		return "[表情类型=" + strconv.Quote(text) + "]", e
	case "hr":
		return "\n---\n", nil
	case "img":
		return "[图片附件未读取]", nil
	default:
		return "[未解析内容类型=" + strconv.Quote(tag) + "]", nil
	}
}

// PostBlocks selects the same locale and ordering for text and native resources.
func PostBlocks(raw string) ([][]map[string]json.RawMessage, string, error) {
	post, err := ParsePost(raw)
	return post.Blocks, post.Title, err
}

type Post struct {
	Blocks [][]map[string]json.RawMessage
	Title  string
	Files  json.RawMessage // Top-level attachment entries, separate from any locale or content version.
}

// ParsePost keeps the existing content/locale projection while retaining root files.
func ParsePost(raw string) (Post, error) {
	var body map[string]json.RawMessage
	if len(raw) > 1<<20 || json.Unmarshal([]byte(raw), &body) != nil || body == nil {
		return Post{}, errors.New("invalid_feishu_content")
	}
	rootFiles := body["files"]
	if body["content"] == nil && body["content_v2"] == nil {
		keys := []string{"zh_cn", "en_us"}
		rest := make([]string, 0, len(body))
		for k := range body {
			if k != "zh_cn" && k != "en_us" {
				rest = append(rest, k)
			}
		}
		sort.Strings(rest)
		keys = append(keys, rest...)
		for _, k := range keys {
			var locale map[string]json.RawMessage
			if json.Unmarshal(body[k], &locale) == nil && locale != nil {
				body = locale
				break
			}
		}
	}
	source := body["content_v2"]
	if source == nil || string(source) == "null" {
		source = body["content"]
	}
	var blocks [][]map[string]json.RawMessage
	if json.Unmarshal(source, &blocks) != nil || blocks == nil {
		return Post{}, errors.New("invalid_post_blocks")
	}
	return Post{Blocks: blocks, Title: optional(body, "title"), Files: rootFiles}, nil
}
