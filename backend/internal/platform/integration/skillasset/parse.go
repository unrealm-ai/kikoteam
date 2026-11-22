// Package skillasset implements Platform-owned immutable Skill assets.
package skillasset

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"go.yaml.in/yaml/v3"
	"golang.org/x/text/unicode/norm"
)

const MaxArchive = 20 << 20
const MaxTotal = 100 << 20
const MaxFile = 20 << 20
const MaxFiles = 1000
const MaxMarkdown = 64 << 10

func invalid(message string) error { return service.Fail("invalid_skill", 400, message) }
func validPath(p string) bool {
	if p == "" || len(p) > 1024 || !utf8.ValidString(p) || strings.Contains(p, "\\") || strings.HasPrefix(p, "/") || path.Clean(p) != p {
		return false
	}
	for _, c := range p {
		if unicode.IsControl(c) {
			return false
		}
	}
	for _, v := range strings.Split(p, "/") {
		if v == "." || v == ".." || v == "" || strings.Contains(v, ":") || v == ".git" || strings.EqualFold(v, ".env") || strings.HasPrefix(strings.ToLower(v), ".env.") {
			return false
		}
	}
	return true
}
func addFile(files map[string][]byte, seen map[string]bool, p string, data []byte) error {
	if !validPath(p) || len(data) > MaxFile {
		return invalid("文件路径或大小无效")
	}
	folded := strings.ToLower(norm.NFC.String(p))
	if seen[folded] {
		return invalid("存在重复或大小写冲突的文件路径")
	}
	seen[folded] = true
	files[p] = data
	return nil
}
func readZIP(data []byte) (map[string][]byte, error) {
	if len(data) > MaxArchive {
		return nil, invalid("ZIP 最大为 20 MiB")
	}
	z, e := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if e != nil {
		return nil, invalid("ZIP 格式无效")
	}
	files := map[string][]byte{}
	seen := map[string]bool{}
	total := 0
	if len(z.File) > MaxFiles+100 {
		return nil, invalid("ZIP 条目过多")
	}
	for _, f := range z.File {
		if f.FileInfo().IsDir() {
			if !validPath(strings.TrimSuffix(f.Name, "/")) {
				return nil, invalid("目录路径无效")
			}
			continue
		}
		if !f.Mode().IsRegular() || f.UncompressedSize64 > MaxFile {
			return nil, invalid("不支持链接、特殊文件或超大文件")
		}
		r, e := f.Open()
		if e != nil {
			return nil, invalid("无法读取 ZIP 文件")
		}
		b, e := io.ReadAll(io.LimitReader(r, MaxFile+1))
		ce := r.Close()
		if e != nil || ce != nil {
			return nil, invalid("ZIP 文件损坏")
		}
		total += len(b)
		if total > MaxTotal || len(files) >= MaxFiles {
			return nil, invalid("解压内容超出限制")
		}
		if e = addFile(files, seen, f.Name, b); e != nil {
			return nil, e
		}
	}
	return stripRoot(files)
}
func stripRoot(files map[string][]byte) (map[string][]byte, error) {
	if _, ok := files["SKILL.md"]; ok {
		return files, nil
	}
	root := ""
	for p := range files {
		parts := strings.SplitN(p, "/", 2)
		if len(parts) != 2 {
			return nil, invalid("需要根 SKILL.md 或单一外层目录")
		}
		if root == "" {
			root = parts[0]
		} else if root != parts[0] {
			return nil, invalid("存在多个 Skill 根目录")
		}
	}
	out := map[string][]byte{}
	for p, b := range files {
		out[strings.TrimPrefix(p, root+"/")] = b
	}
	if _, ok := out["SKILL.md"]; !ok {
		return nil, invalid("缺少 SKILL.md")
	}
	return out, nil
}

var technicalName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func metadata(md []byte) (string, string, error) {
	if len(md) > MaxMarkdown || !utf8.Valid(md) || bytes.IndexByte(md, 0) >= 0 {
		return "", "", invalid("SKILL.md 必须为 64 KiB 以内 UTF-8 文本")
	}
	lines := strings.Split(strings.ReplaceAll(string(md), "\r\n", "\n"), "\n")
	if len(lines) < 3 || lines[0] != "---" {
		return "", "", invalid("SKILL.md 需要 YAML frontmatter")
	}
	end := 0
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			end = i
			break
		}
	}
	if end == 0 {
		return "", "", invalid("SKILL.md frontmatter 未结束")
	}
	var doc yaml.Node
	if e := yaml.Unmarshal([]byte(strings.Join(lines[1:end], "\n")), &doc); e != nil {
		return "", "", invalid("SKILL.md 元数据格式无效")
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return "", "", invalid("SKILL.md 元数据必须是对象")
	}
	if e := safeYAML(&doc, 0); e != nil {
		return "", "", e
	}
	var name, description string
	n := doc.Content[0]
	for i := 0; i < len(n.Content); i += 2 {
		v := n.Content[i+1]
		switch n.Content[i].Value {
		case "name":
			if v.Tag != "!!str" {
				return "", "", invalid("name 必须为字符串")
			}
			name = v.Value
		case "description":
			if v.Tag != "!!str" {
				return "", "", invalid("description 必须为字符串")
			}
			description = v.Value
		}
	}
	if !technicalName.MatchString(name) || len(name) > 64 || strings.TrimSpace(description) == "" || utf8.RuneCountInString(description) > 1024 {
		return "", "", invalid("name 需为小写英文数字短横线（最多64字符），description 必填且不超过1024字符")
	}
	return name, description, nil
}
func safeYAML(n *yaml.Node, depth int) error {
	if depth > 16 || n.Kind == yaml.AliasNode || n.Anchor != "" {
		return invalid("不支持 YAML 别名或过深结构")
	}
	if n.Kind == yaml.MappingNode {
		seen := map[string]bool{}
		for i := 0; i < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Kind != yaml.ScalarNode || seen[k.Value] {
				return invalid("YAML 键重复或无效")
			}
			seen[k.Value] = true
		}
	}
	for _, c := range n.Content {
		if e := safeYAML(c, depth+1); e != nil {
			return e
		}
	}
	return nil
}
func hash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func bundle(files map[string][]byte, source service.SkillSource) (service.SkillBundle, error) {
	name, desc, e := metadata(files["SKILL.md"])
	if e != nil {
		return service.SkillBundle{}, e
	}
	keys := make([]string, 0, len(files))
	total := 0
	for p, b := range files {
		if !validPath(p) || len(b) > MaxFile {
			return service.SkillBundle{}, invalid("文件路径或大小无效")
		}
		keys = append(keys, p)
		total += len(b)
	}
	if len(files) > MaxFiles || total > MaxTotal {
		return service.SkillBundle{}, invalid("Skill 超出文件数量或总大小限制")
	}
	sort.Strings(keys)
	m := service.SkillManifest{FormatVersion: 1, Files: []service.SkillFile{}}
	for _, p := range keys {
		m.Files = append(m.Files, service.SkillFile{Path: p, Size: int64(len(files[p])), SHA256: hash(files[p])})
	}
	b, e := json.Marshal(m.Files)
	if e != nil {
		return service.SkillBundle{}, e
	}
	m.Digest = hash(b)
	return service.SkillBundle{TechnicalName: name, Description: desc, Manifest: m, Source: source, Files: files}, nil
}
func (s *Store) Prepare(ctx context.Context, r service.SkillImportRequest) (service.SkillBundle, error) {
	var files map[string][]byte
	var e error
	source := service.SkillSource{Type: r.Source}
	switch r.Source {
	case "zip":
		var b []byte
		b, e = base64.StdEncoding.DecodeString(r.ZIPBase64)
		if e != nil {
			return service.SkillBundle{}, invalid("ZIP Base64 无效")
		}
		files, e = readZIP(b)
	case "markdown":
		files = map[string][]byte{"SKILL.md": []byte(r.Markdown)}
	case "manual":
		var b []byte
		b, e = yaml.Marshal(map[string]string{"name": r.Name, "description": r.Description})
		if e == nil {
			files = map[string][]byte{"SKILL.md": []byte("---\n" + string(b) + "---\n" + r.Body)}
		}
	case "github":
		files, source, e = s.github(ctx, r)
	default:
		return service.SkillBundle{}, invalid("不支持的导入来源")
	}
	if e != nil {
		return service.SkillBundle{}, e
	}
	if e = ctx.Err(); e != nil {
		return service.SkillBundle{}, e
	}
	return bundle(files, source)
}
func verifyFile(f service.SkillFile, b []byte) error {
	if int64(len(b)) != f.Size || hash(b) != f.SHA256 {
		return fmt.Errorf("skill asset integrity mismatch")
	}
	return nil
}
