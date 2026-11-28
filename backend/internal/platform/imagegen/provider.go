package imagegen

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

var imageSizes = map[string]string{"square": "1024x1024", "landscape": "1536x1024", "portrait": "1024x1536"}

type original struct {
	Digest        string
	Width, Height int
	Bytes         int64
}

func validatePNG(data []byte, expectedSize string) (original, error) {
	bad := service.Fail("invalid_provider_image", 502, "供应商图片格式或尺寸不符")
	if len(data) == 0 || len(data) > manager.ProjectUploadLimit {
		return original{}, bad
	}
	cfg, e := png.DecodeConfig(bytes.NewReader(data))
	if e != nil || fmt.Sprintf("%dx%d", cfg.Width, cfg.Height) != expectedSize {
		return original{}, bad
	}
	if _, e = png.Decode(bytes.NewReader(data)); e != nil {
		return original{}, bad
	}
	digest := sha256.Sum256(data)
	return original{hex.EncodeToString(digest[:]), cfg.Width, cfg.Height, int64(len(data))}, nil
}

// The admin preview is bounded independently of the archived original size.
func makePreview(data []byte) ([]byte, error) {
	src, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	const side = 192
	thumb := image.NewNRGBA(image.Rect(0, 0, side, side))
	for y := range side {
		for x := range side {
			thumb.Set(x, y, src.At(x*src.Bounds().Dx()/side, y*src.Bounds().Dy()/side))
		}
	}
	var b bytes.Buffer
	err = png.Encode(&b, thumb)
	return b.Bytes(), err
}

func readBounded(r io.Reader, maxBytes int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil || int64(len(b)) > maxBytes {
		return nil, service.Fail("provider_response_invalid", 502, "供应商响应不完整或过大")
	}
	return b, nil
}
func (s *Service) download(ctx context.Context, base, location string) ([]byte, error) {
	from, e := url.Parse(base)
	if e != nil {
		return nil, e
	}
	u, e := url.Parse(location)
	if e != nil || u.Scheme != "https" || !strings.EqualFold(u.Host, from.Host) || u.User != nil || u.Fragment != "" {
		return nil, service.Fail("untrusted_image_url", 502, "供应商返回了非受信任的下载地址")
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if e != nil {
		return nil, e
	}
	resp, e := s.HTTP.Do(req)
	if e != nil {
		return nil, service.Fail("image_download_failed", 502, "读取供应商图片失败")
	}
	defer resp.Body.Close() //nolint:errcheck // Read-only response.
	if resp.StatusCode != 200 {
		return nil, service.Fail("image_download_failed", 502, "读取供应商图片失败")
	}
	return readBounded(resp.Body, manager.ProjectUploadLimit)
}
func generationPayload(c Settings, prompt, ratio string) map[string]any {
	payload := map[string]any{"model": c.Model, "prompt": prompt, "size": c.sizes[ratio], "output_format": "png"}
	base, err := url.Parse(c.BaseURL)
	if err == nil && base.Scheme == "https" && strings.EqualFold(base.Hostname(), "ark.cn-beijing.volces.com") && base.Path == "/api/v3" {
		// Ark defaults to a signed object-store URL on another origin.
		payload["response_format"] = "b64_json"
	} else {
		payload["n"] = 1
		if c.Provider == base64Provider {
			payload["response_format"] = "b64_json"
		}
	}
	return payload
}

func (s *Service) generate(ctx context.Context, c Settings, key, prompt, ratio, id string) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.Timeout)*time.Second)
	defer cancel()
	body, e := json.Marshal(generationPayload(c, prompt, ratio))
	if e != nil {
		return nil, "", e
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/images/generations", bytes.NewReader(body))
	if e != nil {
		return nil, "", e
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	// Disable implicit transport retries for a billable POST.
	req.GetBody = nil
	req.Header.Set("Idempotency-Key", id)
	resp, e := s.HTTP.Do(req)
	if e != nil {
		return nil, "", service.Fail("generation_pending_review", 502, "供应商执行结果待核对")
	}
	defer resp.Body.Close() //nolint:errcheck // Read-only response.
	upstream := resp.Header.Get("X-Request-ID")
	if len(upstream) > 256 {
		upstream = ""
	}
	switch resp.StatusCode {
	case 400, 401, 403, 404, 422, 429:
		return nil, upstream, service.Fail("image_provider_rejected", 422, "供应商拒绝出图，请检查模型、凭据或额度")
	case 200:
	default:
		return nil, upstream, service.Fail("generation_pending_review", 502, "供应商执行结果待核对")
	}
	raw, e := readBounded(resp.Body, manager.ProjectUploadLimit*4/3+65536)
	if e != nil {
		return nil, upstream, e
	}
	var decoded struct {
		Data []struct {
			PNG []byte `json:"b64_json"`
			URL string `json:"url"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &decoded) != nil || len(decoded.Data) != 1 {
		return nil, upstream, service.Fail("provider_response_invalid", 502, "供应商未返回一张图片")
	}
	img := decoded.Data[0].PNG
	if len(img) == 0 && decoded.Data[0].URL != "" {
		img, e = s.download(ctx, c.BaseURL, decoded.Data[0].URL)
		if e != nil {
			return nil, upstream, e
		}
	}
	_, e = validatePNG(img, c.sizes[ratio])
	return img, upstream, e
}
