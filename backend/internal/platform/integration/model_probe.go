package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

const ModelTextTimeout = 20 * time.Second
const ModelImageTimeout = 40 * time.Second
const imagePrompt = `Identify the central shape and its color. Respond only with JSON {"shape":"...","color":"...","code":"...."}. Use English lowercase shape/color and copy the four black digits exactly.`

// ModelProber only sends synthetic inputs. It never logs provider bodies or credentials.
// HTTPS private deployments are supported; deployment egress policy owns further restrictions.
// AllowLoopbackHTTP is a deployment/test switch, never a field accepted from API requests.
type ModelProber struct {
	AllowLoopbackHTTP             bool
	Client                        *http.Client
	textTimeout, timeImageTimeout time.Duration
}

func (p *ModelProber) Probe(ctx context.Context, in service.ProbeInput) (service.ProbeResult, error) {
	endpoint, err := NormalizeModelEndpoint(in.Protocol, in.Endpoint)
	if err != nil {
		return service.ProbeResult{}, err
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return service.ProbeResult{}, errors.New("invalid model endpoint")
	}
	ip := net.ParseIP(u.Hostname())
	local := u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())
	if u.Scheme != "https" && (!p.AllowLoopbackHTTP || !local) {
		return service.ProbeResult{}, errors.New("model endpoint requires HTTPS")
	}
	if len(in.APIKey) > 8192 || strings.ContainsAny(in.APIKey, "\r\n\x00") {
		return service.ProbeResult{}, errors.New("invalid model credential")
	}
	fixture, err := newModelImageFixture()
	if err != nil {
		return service.ProbeResult{}, errors.New("image fixture generation failed")
	}
	in.Endpoint = endpoint
	result := service.ProbeResult{}
	textDone := make(chan service.ProbeCase, 1)
	go func() {
		textDone <- p.run(ctx, in, "Reply with exactly OK and nothing else. Do not add punctuation.", "", p.textLimit(), nil)
	}()
	result.Image = p.run(ctx, in, imagePrompt, fixture.Data, p.imageLimit(), &fixture)
	result.Text = <-textDone
	return result, nil
}
func (p *ModelProber) textLimit() time.Duration {
	if p.textTimeout > 0 {
		return p.textTimeout
	}
	return ModelTextTimeout
}
func (p *ModelProber) imageLimit() time.Duration {
	if p.timeImageTimeout > 0 {
		return p.timeImageTimeout
	}
	return ModelImageTimeout
}

func (p *ModelProber) run(parent context.Context, in service.ProbeInput, prompt, data string, timeout time.Duration, fixture *modelImageFixture) (result service.ProbeCase) {
	started := time.Now()
	defer func() { result.DurationMS = time.Since(started).Milliseconds() }()
	result.Status = "failed"
	if fixture != nil {
		result.Status = "unknown"
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	body, err := json.Marshal(modelBody(in.Protocol, in.ModelID, prompt, data))
	if err != nil {
		result.Issue = "request_encoding"
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, modelURL(in.Protocol, in.Endpoint), bytes.NewReader(body))
	if err != nil {
		result.Issue = "request_invalid"
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if in.Protocol == "anthropic_messages" {
		if in.APIKey != "" {
			req.Header.Set("x-api-key", in.APIKey)
		}
		req.Header.Set("anthropic-version", "2023-06-01")
	} else if in.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+in.APIKey)
	}
	client := http.Client{}
	if p.Client != nil {
		client = *p.Client
	}
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(req)
	if err != nil {
		result.Issue = "network_error"
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			result.Issue = "timeout"
		} else if errors.Is(ctx.Err(), context.Canceled) {
			result.Issue = "cancelled"
		}
		return
	}
	defer response.Body.Close() //nolint:errcheck // Body read reports failures; connection cleanup cannot alter probe outcome.
	result.HTTPStatus = response.StatusCode
	raw, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil {
		result.Issue = "body_read"
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			result.Issue = "timeout"
		} else if errors.Is(ctx.Err(), context.Canceled) {
			result.Issue = "cancelled"
		}
		return
	}
	if len(raw) > 65536 {
		result.Issue = "body_limit"
		return
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		result.Issue = "http_status"
		if fixture != nil && imageUnsupported(response.StatusCode, raw) {
			result.Status = "unsupported"
			result.Issue = "unsupported_image"
		}
		// Never reflect provider-controlled error bodies, which can echo headers.
		return
	}
	output, valid := modelOutput(raw, in.Protocol)
	result.Output = safeModelOutput(output, in.APIKey)
	if !valid {
		result.Issue = "protocol_shape"
		return
	}
	if fixture == nil {
		if strings.TrimSpace(output) != "OK" {
			result.Issue = "answer_mismatch"
			return
		}
	} else if !imageAnswer(output, *fixture) {
		result.Issue = "answer_mismatch"
		return
	}
	result.Status = "passed"
	result.Issue = "passed"
	return
}

var unsupportedImage = regexp.MustCompile(`(?i)\b(image|vision)\b.{0,100}\b(not supported|unsupported)\b|\bdoes not support\b.{0,60}\b(image|vision)\b`)

func imageUnsupported(status int, raw []byte) bool {
	if status != 400 && status != 422 {
		return false
	}
	var b struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &b) != nil {
		return false
	}
	return b.Error.Code == "unsupported_image_input" || b.Error.Code == "vision_not_supported" || b.Error.Code == "image_not_supported" || unsupportedImage.MatchString(b.Error.Message)
}
func imageAnswer(output string, f modelImageFixture) bool {
	v := strings.TrimSpace(output)
	v = strings.TrimPrefix(v, "```json")
	v = strings.TrimPrefix(v, "```")
	v = strings.TrimSuffix(v, "```")
	var answer struct {
		Shape string `json:"shape"`
		Color string `json:"color"`
		Code  string `json:"code"`
	}
	return json.Unmarshal([]byte(v), &answer) == nil && answer.Shape == "square" && answer.Color == f.Color && answer.Code == f.Code
}

var secretPattern = regexp.MustCompile(`(?i)(bearer\s+)[^\s"<>]+|\bsk-[a-zA-Z0-9_-]{8,}`)

func safeModelOutput(output, secret string) string {
	if secret != "" {
		output = strings.ReplaceAll(output, secret, "[REDACTED]")
	}
	output = secretPattern.ReplaceAllString(output, "[REDACTED]")
	output = strings.Map(func(r rune) rune {
		if r < 32 && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, output)
	runes := []rune(output)
	if len(runes) > 4096 {
		output = string(runes[:4095]) + "…"
	}
	return output
}
