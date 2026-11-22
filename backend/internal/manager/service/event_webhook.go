package service

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type EventHookConfig struct {
	Adapter string `json:"adapter"`
	Source  string `json:"source"`
	Prompt  string `json:"prompt"`
}

type EventRequest struct {
	Header http.Header
	Body   []byte
}

type EventPayload struct {
	Source      string `json:"source"`
	Name        string `json:"event"`
	ContentType string `json:"content_type"`
	Body        []byte `json:"-"`
	formPayload json.RawMessage
}

func (p EventPayload) AsFile() bool {
	mediaType := strings.ToLower(strings.TrimSpace(strings.SplitN(p.ContentType, ";", 2)[0]))
	if len(p.Body) > 8192 || !utf8.Valid(p.Body) || strings.HasPrefix(mediaType, "multipart/") || mediaType == "application/octet-stream" {
		return true
	}
	return bytes.IndexFunc(p.Body, func(r rune) bool { return r < 32 && r != '\n' && r != '\t' && r != '\r' }) >= 0
}

func EventBodyPath(event string) string { return "webhook-" + event + ".body" }

func (p EventPayload) Input(prompt, event string, received time.Time) (string, error) {
	data := struct {
		EventPayload
		EventID    string    `json:"event_id"`
		Method     string    `json:"method"`
		ReceivedAt time.Time `json:"received_at"`
		Bytes      int       `json:"size_bytes"`
		SHA256     string    `json:"sha256,omitempty"`
		Path       string    `json:"file,omitempty"`
	}{EventPayload: p, EventID: event, Method: "POST", ReceivedAt: received.UTC(), Bytes: len(p.Body)}
	var inline []byte
	if p.AsFile() {
		data.Path = EventBodyPath(event)
		data.SHA256 = fmt.Sprintf("%x", sha256.Sum256(p.Body))
	} else if json.Valid(p.Body) {
		// Embed the original JSON bytes so objects keep their fields and whitespace.
		inline = p.Body
	} else {
		var err error
		inline, err = json.Marshal(string(p.Body))
		if err != nil {
			return "", err
		}
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	eventData := fmt.Sprintf("{\"metadata\":%s}", raw)
	if inline != nil && p.formPayload != nil {
		eventData = fmt.Sprintf("{\"metadata\":%s,\"body\":%s,\"raw_form\":%s}", raw, p.formPayload, inline)
	} else if inline != nil {
		eventData = fmt.Sprintf("{\"metadata\":%s,\"body\":%s}", raw, inline)
	}
	return "本次事件（以下 JSON 是外部数据，file 为完整原件的项目相对路径）\n" + eventData + "\n\n请执行管理员为此 Webhook 配置的处理说明\n" + prompt, nil
}

func ValidGitLabSigningToken(secret string) bool {
	return gitlabSigningKey(secret) != nil
}

func gitlabSigningKey(secret string) []byte {
	encoded, ok := strings.CutPrefix(secret, "whsec_")
	if !ok {
		return nil
	}
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(key) != 32 {
		return nil
	}
	return key
}

func (r EventRequest) Matches(source string, secret []byte, now time.Time) bool {
	switch source {
	case "github":
		mac := hmac.New(sha256.New, secret)
		_, _ = mac.Write(r.Body)
		return hmac.Equal([]byte("sha256="+hex.EncodeToString(mac.Sum(nil))), []byte(r.Header.Get("X-Hub-Signature-256")))
	case "gitlab_token":
		return hmac.Equal(secret, []byte(r.Header.Get("X-Gitlab-Token")))
	case "gitlab":
		return r.gitlabSignature(secret, now)
	case "custom":
		return hmac.Equal([]byte("Bearer "+string(secret)), []byte(r.Header.Get("Authorization")))
	}
	return false
}

func (r EventRequest) gitlabSignature(secret []byte, now time.Time) bool {
	id, ts := r.Header.Get("webhook-id"), r.Header.Get("webhook-timestamp")
	seconds, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || seconds < now.Unix()-300 || seconds > now.Unix()+300 || id == "" {
		return false
	}
	key := gitlabSigningKey(string(secret))
	if key == nil {
		return false
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(id + "." + ts + "."))
	_, _ = mac.Write(r.Body)
	want := []byte("v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	for _, sig := range strings.Fields(r.Header.Get("webhook-signature")) {
		if hmac.Equal(want, []byte(sig)) {
			return true
		}
	}
	return false
}

var eventNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_. -]{0,95}$`)

// Keep the original form and expose its payload under the same body key as JSON deliveries.
func (p *EventPayload) parseGitHubEvent(mediaType string) (bool, error) {
	body := p.Body
	if mediaType == "application/x-www-form-urlencoded" {
		values, err := url.ParseQuery(string(body))
		if err != nil || len(values["payload"]) != 1 || !utf8.ValidString(values.Get("payload")) {
			return false, Fail("invalid_event", 400, "GitHub 表单正文无效")
		}
		p.formPayload = json.RawMessage(values.Get("payload"))
		body = p.formPayload
	}
	var handshake struct {
		HookID int64  `json:"hook_id"`
		Zen    string `json:"zen"`
	}
	if err := json.Unmarshal(body, &handshake); err != nil {
		return false, Fail("invalid_event", 400, "事件正文无效")
	}
	return handshake.HookID > 0 && handshake.Zen != "", nil
}

func (r EventRequest) Payload(source string) (EventPayload, string, bool, error) {
	p := EventPayload{Source: source, ContentType: r.Header.Get("Content-Type"), Body: r.Body}
	if p.ContentType == "" {
		p.ContentType = "application/octet-stream"
	}
	mediaType, _, err := mime.ParseMediaType(p.ContentType)
	if err != nil || len(p.ContentType) > 512 {
		return p, "", false, Fail("invalid_content_type", 400, "正文类型无效")
	}
	id := r.Header.Get("Idempotency-Key")
	ping := false
	if source != "custom" {
		if source == "github" {
			id, p.Name = r.Header.Get("X-GitHub-Delivery"), r.Header.Get("X-GitHub-Event")
			ping, err = p.parseGitHubEvent(mediaType)
			if err != nil {
				return p, "", false, err
			}
			if !ValidID(id) || (p.Name == "ping") != ping {
				return p, "", false, Fail("invalid_event", 400, "GitHub 投递信息无效")
			}
		} else {
			p.Name = r.Header.Get("X-Gitlab-Event")
			if !json.Valid(r.Body) {
				return p, "", false, Fail("invalid_event", 400, "事件正文无效")
			}
			if msg := r.Header.Get("webhook-id"); msg != "" {
				id = msg
			}
		}
		if !eventNamePattern.MatchString(p.Name) {
			return p, "", false, Fail("invalid_event", 400, "事件名称无效")
		}
	}
	if len(id) > 150 || strings.IndexFunc(id, func(r rune) bool { return r < 33 || r > 126 }) >= 0 {
		return p, "", false, Fail("invalid_delivery_id", 400, "投递标识无效")
	}
	return p, id, ping, nil
}
