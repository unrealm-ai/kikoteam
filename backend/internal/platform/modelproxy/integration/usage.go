package integration

import (
	"encoding/json"

	proxy "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy"
)

type tokenUsage struct {
	Input         *int64 `json:"input_tokens"`
	Output        *int64 `json:"output_tokens"`
	Prompt        *int64 `json:"prompt_tokens"`
	Completion    *int64 `json:"completion_tokens"`
	CacheRead     *int64 `json:"cache_read_input_tokens"`
	CacheWrite    *int64 `json:"cache_creation_input_tokens"`
	PromptDetails struct {
		Cached int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	InputDetails struct {
		Cached int64 `json:"cached_tokens"`
	} `json:"input_tokens_details"`
	// Some Chat Completions providers report this cache split independently.
	Hit  *int64 `json:"prompt_cache_hit_tokens"`
	Miss *int64 `json:"prompt_cache_miss_tokens"`
}

type meter struct {
	protocol string
	stream   bool
	usage    tokenUsage
	complete bool
}

func (m *meter) event(data []byte) {
	var body struct {
		Type    string      `json:"type"`
		Usage   *tokenUsage `json:"usage"`
		Choices []struct {
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Message *struct {
			Usage *tokenUsage `json:"usage"`
		} `json:"message"`
		Response *struct {
			Usage *tokenUsage `json:"usage"`
		} `json:"response"`
	}
	if json.Unmarshal(data, &body) != nil {
		return
	}
	if !m.stream || m.protocol == "chat_completions" {
		if body.Usage != nil && (!m.stream || len(body.Choices) == 0 || body.Choices[0].FinishReason != "") {
			m.usage = *body.Usage
			m.complete = true
		}
		return
	}
	switch m.protocol {
	case "openai_responses":
		if (body.Type == "response.completed" || body.Type == "response.incomplete" || body.Type == "response.failed") && body.Response != nil && body.Response.Usage != nil {
			m.usage = *body.Response.Usage
			m.complete = true
		}
	case "anthropic_messages":
		if body.Type == "message_start" && body.Message != nil && body.Message.Usage != nil {
			m.usage = *body.Message.Usage
		}
		if body.Type == "message_delta" && body.Usage != nil && body.Usage.Output != nil {
			m.usage.Output = body.Usage.Output
			if body.Usage.Input != nil {
				m.usage.Input = body.Usage.Input
			}
			if body.Usage.CacheRead != nil {
				m.usage.CacheRead = body.Usage.CacheRead
			}
			if body.Usage.CacheWrite != nil {
				m.usage.CacheWrite = body.Usage.CacheWrite
			}
			m.complete = true
		}
	}
}

func (m meter) result() *proxy.Usage {
	if !m.complete {
		return nil
	}
	u := m.usage
	input, output, cached, written := u.Input, u.Output, u.InputDetails.Cached, int64(0)
	switch m.protocol {
	case "chat_completions":
		input, output, cached = u.Prompt, u.Completion, u.PromptDetails.Cached
		if u.Hit != nil {
			if cached != 0 && cached != *u.Hit {
				return nil
			}
			cached = *u.Hit
		}
	case "anthropic_messages":
		cached = 0
		if u.CacheRead != nil {
			cached = *u.CacheRead
		}
		if u.CacheWrite != nil {
			written = *u.CacheWrite
		}
	}
	if input == nil || output == nil || *input < 0 || *output < 0 || cached < 0 || written < 0 {
		return nil
	}
	ordinary := *input
	if m.protocol != "anthropic_messages" {
		if cached > ordinary {
			return nil
		}
		ordinary -= cached
		if u.Miss != nil && *u.Miss != ordinary {
			return nil
		}
	}
	return &proxy.Usage{Input: ordinary, Output: *output, CacheRead: cached, CacheWrite: written}
}
