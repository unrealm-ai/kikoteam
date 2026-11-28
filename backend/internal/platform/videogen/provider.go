// Package videogen implements the platform's asynchronous, per-second video plugin.
package videogen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"strconv"
	"time"

	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

var ratios = map[string]string{"landscape": "16:9", "portrait": "9:16", "square": "1:1"}
var ids = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)

type upstream struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	URL    string `json:"url"`
}

var rejected = service.Fail("video_provider_rejected", 422, "视频服务拒绝请求，请检查配置或额度")
var unavailable = service.Fail("video_provider_unavailable", 502, "视频服务暂时不可用，请保留原生成记录")
var invalidMedia = service.Fail("video_invalid", 422, "成片格式、画幅、时长或体积不符合交付要求")

func (s *Service) request(ctx context.Context, method, target, key string, payload []byte, limit int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(payload))
	if err != nil {
		return nil, unavailable
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
		req.GetBody = nil
	}
	client := *s.HTTP
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return nil, unavailable
	}
	defer resp.Body.Close() //nolint:errcheck // Read-only response.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if method == http.MethodPost {
			switch resp.StatusCode {
			case 400, 401, 403, 404, 422, 429:
				return nil, rejected
			}
		}
		return nil, unavailable
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, unavailable
	}
	if int64(len(b)) > limit {
		return nil, invalidMedia
	}
	return b, nil
}
func (s *Service) submit(ctx context.Context, c Settings, key string, a Arguments) (string, error) {
	payload, err := json.Marshal(map[string]any{"model": c.Model, "prompt": a.Prompt, "duration": a.Seconds, "ratio": ratios[a.Ratio], "resolution": "768P"})
	if err != nil {
		return "", err
	}
	raw, err := s.request(ctx, http.MethodPost, c.BaseURL+"/v1/videos", key, payload, 65536)
	if err != nil {
		return "", err
	}
	var u upstream
	if json.Unmarshal(raw, &u) != nil || !ids.MatchString(u.ID) || u.ID == "." || u.ID == ".." {
		return "", unavailable
	}
	return u.ID, nil
}
func (s *Service) poll(ctx context.Context, base, key, id string) (upstream, error) {
	var u upstream
	raw, err := s.request(ctx, http.MethodGet, base+"/v1/videos/"+url.PathEscape(id), key, nil, 65536)
	if err != nil {
		return u, err
	}
	if json.Unmarshal(raw, &u) != nil || u.ID != id {
		return u, unavailable
	}
	return u, nil
}
func (s *Service) download(ctx context.Context, u upstream) ([]byte, error) {
	target, err := url.Parse(u.URL)
	if err != nil || target.Scheme != "https" || target.Hostname() == "" || target.User != nil || target.Fragment != "" {
		return nil, unavailable
	}
	// The production transport resolves and pins public IPs. Signed media URLs never receive the API key.
	return s.request(ctx, http.MethodGet, target.String(), "", nil, manager.ProjectUploadLimit)
}
func inspect(ctx context.Context, b []byte, ratio string, seconds int) error {
	if http.DetectContentType(b) != "video/mp4" {
		return invalidMedia
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-protocol_whitelist", "pipe", "-f", "mov", "-select_streams", "v:0", "-show_entries", "stream=width,height,codec_name,duration", "-of", "json", "pipe:0")
	cmd.Stdin = bytes.NewReader(b)
	raw, err := cmd.Output()
	if err != nil {
		var missing *exec.Error
		if errors.As(err, &missing) {
			return service.Fail("video_probe_unavailable", 503, "服务器缺少 ffprobe")
		}
		return invalidMedia
	}
	var info struct {
		Streams []struct {
			Width, Height int
			Codec         string `json:"codec_name"`
			Duration      string
		}
	}
	if json.Unmarshal(raw, &info) != nil || len(info.Streams) != 1 {
		return invalidMedia
	}
	v := info.Streams[0]
	expected := map[string][2]int{"landscape": {1344, 768}, "portrait": {768, 1344}, "square": {768, 768}}
	size := expected[ratio]
	duration, err := strconv.ParseFloat(v.Duration, 64)
	if err != nil || math.IsNaN(duration) || v.Width != size[0] || v.Height != size[1] || duration < float64(seconds)-0.5 || duration > float64(seconds)+1 || v.Codec != "h264" {
		return invalidMedia
	}
	return nil
}
