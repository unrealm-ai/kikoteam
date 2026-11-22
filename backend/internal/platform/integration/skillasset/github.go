package skillasset

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func githubHTTPClient() *http.Client {
	tr := &http.Transport{Proxy: nil, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 20 * time.Second, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(address)
		if e != nil {
			return nil, e
		}
		if host != "api.github.com" && host != "codeload.github.com" {
			return nil, fmt.Errorf("unapproved GitHub host")
		}
		ips, e := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if e != nil {
			return nil, e
		}
		for _, ip := range ips {
			if !publicIP(ip) {
				return nil, fmt.Errorf("GitHub resolved to a private address")
			}
		}
		for _, ip := range ips {
			c, e := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if e == nil {
				return c, nil
			}
		}
		return nil, fmt.Errorf("GitHub connection failed")
	}}
	return &http.Client{Transport: tr, Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("GitHub redirects are not permitted") }}
}
func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsUnspecified()
}

var repoPart = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

func githubSource(r service.SkillImportRequest) (service.SkillSource, error) {
	u, e := url.Parse(r.RepositoryURL)
	if e != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return service.SkillSource{}, invalid("只支持公开 GitHub HTTPS 仓库")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 || !repoPart.MatchString(parts[0]) || !repoPart.MatchString(parts[1]) {
		return service.SkillSource{}, invalid("GitHub 仓库地址无效")
	}
	repo := strings.TrimSuffix(parts[1], ".git")
	if repo == "" || repo == "." || repo == ".." {
		return service.SkillSource{}, invalid("GitHub 仓库无效")
	}
	ref := r.Ref
	if ref == "" {
		ref = "HEAD"
	}
	sub := r.Subpath
	if len(parts) > 2 {
		if parts[2] != "tree" || r.Ref == "" {
			return service.SkillSource{}, invalid("GitHub 目录链接请同时明确填写分支或 commit")
		}
		tail := strings.Join(parts[3:], "/")
		if tail != ref && !strings.HasPrefix(tail, ref+"/") {
			return service.SkillSource{}, invalid("目录链接与指定 ref 不一致")
		}
		derived := strings.TrimPrefix(strings.TrimPrefix(tail, ref), "/")
		if sub != "" && sub != derived {
			return service.SkillSource{}, invalid("目录链接与子目录不一致")
		}
		sub = derived
	}
	if len(ref) > 256 || strings.ContainsAny(ref, "\r\n\x00") || sub != "" && !validPath(sub) {
		return service.SkillSource{}, invalid("GitHub ref 或子目录无效")
	}
	return service.SkillSource{Type: "github", RepositoryURL: "https://github.com/" + parts[0] + "/" + repo, Ref: ref, Subpath: sub}, nil
}
func (s *Store) download(ctx context.Context, u string, limit int64) ([]byte, error) {
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Kiko-Skill-Importer")
	resp, e := s.githubClient.Do(req)
	if e != nil {
		return nil, service.Fail("github_unavailable", 502, "GitHub 获取失败或超时")
	}
	defer resp.Body.Close() //nolint:errcheck // Response reading errors checked below.
	if resp.StatusCode != 200 {
		return nil, service.Fail("github_unavailable", 502, "GitHub 资源不可访问、限流或不存在")
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if e != nil || int64(len(b)) > limit {
		return nil, invalid("GitHub 内容超过限制或下载不完整")
	}
	return b, nil
}
func (s *Store) github(ctx context.Context, r service.SkillImportRequest) (map[string][]byte, service.SkillSource, error) {
	source, e := githubSource(r)
	if e != nil {
		return nil, source, e
	}
	repo := strings.TrimPrefix(source.RepositoryURL, "https://github.com/")
	b, e := s.download(ctx, "https://api.github.com/repos/"+repo+"/commits/"+url.PathEscape(source.Ref), 1<<20)
	if e != nil {
		return nil, source, e
	}
	var result struct {
		SHA string `json:"sha"`
	}
	if json.Unmarshal(b, &result) != nil || !commitPattern.MatchString(result.SHA) {
		return nil, source, invalid("GitHub 未返回有效 commit")
	}
	source.Commit = result.SHA
	b, e = s.download(ctx, "https://codeload.github.com/"+repo+"/tar.gz/"+source.Commit, MaxArchive)
	if e != nil {
		return nil, source, e
	}
	files, e := readGitTar(b, source.Subpath)
	return files, source, e
}
func readGitTar(b []byte, sub string) (map[string][]byte, error) {
	gz, e := gzip.NewReader(bytes.NewReader(b))
	if e != nil {
		return nil, invalid("GitHub 归档格式无效")
	}
	defer gz.Close() //nolint:errcheck // Decompression checked through bounded tar reads.
	limited := &io.LimitedReader{R: gz, N: MaxTotal + 1}
	tr := tar.NewReader(limited)
	files := map[string][]byte{}
	seen := map[string]bool{}
	total := int64(0)
	count := 0
	for {
		h, e := tr.Next()
		if errors.Is(e, io.EOF) {
			break
		}
		if e != nil {
			return nil, invalid("GitHub 归档损坏")
		}
		count++
		if count > 10000 || h.Size < 0 || h.Size > MaxFile {
			return nil, invalid("GitHub 归档文件超出限制")
		}
		total += h.Size
		if total > MaxTotal {
			return nil, invalid("GitHub 解压内容超出限制")
		}
		clean := strings.TrimSuffix(h.Name, "/")
		if strings.HasPrefix(clean, "/") || strings.Contains(clean, "\\") || path.Clean(clean) != clean || strings.Contains(clean, "../") {
			return nil, invalid("GitHub 路径无效")
		}
		parts := strings.SplitN(clean, "/", 2)
		if len(parts) != 2 {
			continue
		}
		p := parts[1]
		if sub != "" {
			if !strings.HasPrefix(p, sub+"/") {
				continue
			}
			p = strings.TrimPrefix(p, sub+"/")
		}
		if h.Typeflag == tar.TypeDir {
			continue
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != '\x00' {
			return nil, invalid("Skill 不支持符号链接或特殊文件")
		}
		data, e := io.ReadAll(io.LimitReader(tr, MaxFile+1))
		if e != nil {
			return nil, invalid("GitHub 文件读取失败")
		}
		if len(files) >= MaxFiles {
			return nil, invalid("Skill 文件数量超出限制")
		}
		if e = addFile(files, seen, p, data); e != nil {
			return nil, e
		}
	}
	if _, e := io.Copy(io.Discard, limited); e != nil || limited.N == 0 {
		return nil, invalid("GitHub 解压内容超出限制或校验失败")
	}
	if _, ok := files["SKILL.md"]; !ok {
		return nil, invalid("指定 GitHub 目录缺少 SKILL.md")
	}
	return files, nil
}
