package integration

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	proxy "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy"
)

const MaxBody = 16 << 20

type Upstream struct{ HTTP *http.Client }

func Client() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func (u Upstream) Request(ctx context.Context, m proxy.Model, key string, body map[string]json.RawMessage, headers http.Header) (*http.Response, error) {
	model, e := json.Marshal(m.UpstreamID)
	if e != nil {
		return nil, e
	}
	body["model"] = model
	stream := string(body["stream"]) == "true"
	if m.Protocol == "chat_completions" && stream {
		opts := map[string]json.RawMessage{}
		if raw := body["stream_options"]; len(raw) > 0 && string(raw) != "null" {
			if e = json.Unmarshal(raw, &opts); e != nil {
				return nil, e
			}
		}
		if opts == nil {
			opts = make(map[string]json.RawMessage)
		}
		opts["include_usage"] = json.RawMessage("true")
		body["stream_options"], e = json.Marshal(opts)
		if e != nil {
			return nil, e
		}
	}
	raw, e := json.Marshal(body)
	if e != nil {
		return nil, e
	}
	suffix := "/chat/completions"
	switch m.Protocol {
	case "openai_responses":
		suffix = "/responses"
	case "anthropic_messages":
		suffix = "/messages"
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, m.BaseURL+suffix, bytes.NewReader(raw))
	if e != nil {
		return nil, e
	}
	req.Header.Set("Content-Type", "application/json")
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	if m.Protocol == "anthropic_messages" {
		req.Header.Set("anthropic-version", "2023-06-01")
		if version := headers.Get("anthropic-version"); version != "" {
			req.Header.Set("anthropic-version", version)
		}
		if beta := headers.Get("anthropic-beta"); beta != "" {
			req.Header.Set("anthropic-beta", beta)
		}
		if key != "" {
			req.Header.Set("x-api-key", key)
		}
	} else if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	client := u.HTTP
	if client == nil {
		client = Client()
	}
	return client.Do(req)
}

// Read relays complete SSE events and meters native usage without buffering the stream.
// A disconnected downstream does not prevent reading the rest of a bounded upstream response.
func Read(resp *http.Response, protocol string, send func([]byte)) (*proxy.Usage, error) {
	defer resp.Body.Close() //nolint:errcheck // Reads report errors.
	stream := strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream")
	meter := meter{protocol: protocol, stream: stream}
	if !stream {
		raw, e := io.ReadAll(io.LimitReader(resp.Body, MaxBody+1))
		if e != nil {
			return nil, e
		}
		if len(raw) > MaxBody {
			return nil, errors.New("model response too large")
		}
		meter.event(raw)
		send(raw)
		return meter.result(), nil
	}
	reader := bufio.NewReaderSize(resp.Body, 64<<10)
	var event, line, data bytes.Buffer
	for {
		part, e := reader.ReadSlice('\n')
		if event.Len()+len(part) > MaxBody {
			return meter.result(), errors.New("model event too large")
		}
		event.Write(part)
		line.Write(part)
		if errors.Is(e, bufio.ErrBufferFull) {
			continue
		}
		if e != nil && !errors.Is(e, io.EOF) {
			return meter.result(), e
		}
		current := bytes.TrimRight(line.Bytes(), "\r\n")
		if len(current) > 0 {
			appendData(&data, current)
		}
		if len(current) == 0 || errors.Is(e, io.EOF) {
			if event.Len() > 0 {
				meter.event(data.Bytes())
				send(event.Bytes())
				event.Reset()
				data.Reset()
			}
		}
		line.Reset()
		if errors.Is(e, io.EOF) {
			return meter.result(), nil
		}
	}
}

func appendData(out *bytes.Buffer, line []byte) {
	payload, ok := bytes.CutPrefix(line, []byte("data:"))
	if !ok {
		return
	}
	if out.Len() > 0 {
		out.WriteByte('\n')
	}
	out.Write(bytes.TrimPrefix(payload, []byte(" ")))
}

func ProbeBody(protocol string) map[string]json.RawMessage {
	body := map[string]json.RawMessage{"stream": json.RawMessage("true")}
	switch protocol {
	case "openai_responses":
		body["input"] = json.RawMessage(`"Reply with OK."`)
		body["max_output_tokens"] = json.RawMessage("32")
		body["store"] = json.RawMessage("false")
	case "anthropic_messages":
		body["messages"] = json.RawMessage(`[{"role":"user","content":"Reply with OK."}]`)
		body["max_tokens"] = json.RawMessage("32")
	default:
		body["messages"] = json.RawMessage(`[{"role":"user","content":"Reply with OK."}]`)
		body["max_completion_tokens"] = json.RawMessage("32")
	}
	return body
}
