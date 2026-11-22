package integration

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
)

// NormalizeModelEndpoint is shared by probing and persisted runtime configuration.
// A bare origin means /v1. Explicit non-root prefixes are preserved.
func NormalizeModelEndpoint(protocol, endpoint string) (string, error) {
	if protocol != "chat_completions" && protocol != "openai_responses" && protocol != "anthropic_messages" {
		return "", errors.New("invalid model protocol")
	}
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || len(endpoint) > 2048 || u == nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || strings.ContainsAny(endpoint, "\r\n\\") {
		return "", errors.New("invalid model endpoint")
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return "", errors.New("invalid model endpoint scheme")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	if u.Path == "" {
		u.Path = "/v1"
	}
	return u.String(), nil
}

func modelURL(protocol, base string) string {
	if protocol == "anthropic_messages" {
		return strings.TrimSuffix(base, "/v1") + "/v1/messages"
	}
	if protocol == "openai_responses" {
		return base + "/responses"
	}
	return base + "/chat/completions"
}

func modelBody(protocol, model, prompt, imageData string) map[string]any {
	limit := 64
	if imageData != "" {
		limit = 128
	}
	body := map[string]any{"model": model, "stream": false}
	switch protocol {
	case "openai_responses":
		parts := []any{map[string]any{"type": "input_text", "text": prompt}}
		if imageData != "" {
			parts = append(parts, map[string]any{"type": "input_image", "image_url": "data:image/png;base64," + imageData})
		}
		body["input"] = []any{map[string]any{"role": "user", "content": parts}}
		body["max_output_tokens"] = limit
		body["store"] = false
		body["reasoning"] = map[string]any{"effort": "none"}
	case "anthropic_messages":
		parts := []any{map[string]any{"type": "text", "text": prompt}}
		if imageData != "" {
			parts = append(parts, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": imageData}})
		}
		body["messages"] = []any{map[string]any{"role": "user", "content": parts}}
		body["max_tokens"] = limit
	default:
		var content any = prompt
		if imageData != "" {
			content = []any{map[string]any{"type": "text", "text": prompt}, map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64," + imageData}}}
		}
		body["messages"] = []any{map[string]any{"role": "user", "content": content}}
		body["max_completion_tokens"] = limit
		body["reasoning_effort"] = "none"
		body["enable_thinking"] = false
	}
	return body
}

type modelResponse struct {
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Status string `json:"status"`
	Output []struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
	StopReason string `json:"stop_reason"`
	Content    []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

func modelOutput(raw []byte, protocol string) (string, bool) {
	var body modelResponse
	if json.Unmarshal(raw, &body) != nil {
		return "", false
	}
	switch protocol {
	case "openai_responses":
		var parts []string
		for _, item := range body.Output {
			if item.Type == "message" && item.Role == "assistant" {
				for _, p := range item.Content {
					if p.Type == "output_text" {
						parts = append(parts, p.Text)
					}
				}
			}
		}
		return strings.Join(parts, ""), body.Status == "completed"
	case "anthropic_messages":
		var parts []string
		for _, p := range body.Content {
			if p.Type == "text" {
				parts = append(parts, p.Text)
			}
		}
		return strings.Join(parts, ""), body.StopReason == "end_turn" || body.StopReason == "stop_sequence"
	default:
		if len(body.Choices) != 1 {
			return "", false
		}
		c := body.Choices[0]
		var output string
		if json.Unmarshal(c.Message.Content, &output) != nil {
			return "", false
		}
		return output, c.Message.Role == "assistant" && c.FinishReason == "stop"
	}
}
