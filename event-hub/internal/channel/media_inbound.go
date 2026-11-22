package channel

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/unrealm-ai/kikoteam/event-hub/internal/feishucontent"
)

const mediaLimit = 32 << 20

type mediaLocator struct {
	Key    string `json:"key"`
	AESKey string `json:"aes_key,omitempty"`
}
type mediaNode struct {
	Type         string `json:"type"`
	DownloadCode string `json:"downloadCode"`
	URL          string `json:"url"`
	AESKey       string `json:"aeskey"`
	FileName     string `json:"fileName"`
	Name         string `json:"name"`
}

func mediaRef(kind, name, key, aesKey string) []MediaReference {
	if key == "" || len(key) > 8192 || len(aesKey) > 128 {
		return nil
	}
	name = path.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	if name == "." || name == ".." || name == "/" || strings.TrimSpace(name) == "" {
		name = kind
	}
	for len(name) > 220 {
		_, size := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-size]
	}
	raw, e := json.Marshal(mediaLocator{key, aesKey})
	if e != nil {
		return nil
	}
	return []MediaReference{{Kind: kind, Name: name, Locator: string(raw)}}
}
func feishuMedia(kind, raw string) []MediaReference {
	if kind == "post" {
		var out []MediaReference
		blocks, _, err := feishucontent.PostBlocks(raw)
		if err != nil {
			return nil
		}
		for _, block := range blocks {
			for _, n := range block {
				switch jsonString(n["tag"]) {
				case "img":
					out = append(out, mediaRef("image", "", jsonString(n["image_key"]), "")...)
				case "file":
					out = append(out, mediaRef("file", jsonString(n["file_name"]), jsonString(n["file_key"]), "")...)
				}
			}
		}
		return out
	}
	var n struct {
		Image string `json:"image_key"`
		File  string `json:"file_key"`
		Name  string `json:"file_name"`
	}
	if json.Unmarshal([]byte(raw), &n) != nil {
		return nil
	}
	switch kind {
	case "image":
		return mediaRef(kind, n.Name, n.Image, "")
	case "file":
		return mediaRef(kind, n.Name, n.File, "")
	}
	return nil
}
func dingMedia(kind string, raw json.RawMessage) []MediaReference {
	if kind == "richText" {
		var body struct {
			Items []mediaNode `json:"richText"`
		}
		if json.Unmarshal(raw, &body) != nil {
			return nil
		}
		var out []MediaReference
		for _, item := range body.Items {
			switch item.Type {
			case "picture", "image":
				out = append(out, mediaRef("image", item.FileName, item.DownloadCode, "")...)
			case "file":
				out = append(out, mediaRef("file", item.FileName, item.DownloadCode, "")...)
			}
		}
		return out
	}
	var n mediaNode
	if json.Unmarshal(raw, &n) != nil {
		return nil
	}
	switch kind {
	case "picture", "image":
		return mediaRef("image", n.FileName, n.DownloadCode, "")
	case "file":
		return mediaRef("file", n.FileName, n.DownloadCode, "")
	}
	return nil
}

type wecomMediaNode struct {
	Type  string    `json:"msgtype"`
	Image mediaNode `json:"image"`
	File  mediaNode `json:"file"`
}

func wecomMedia(kind string, raw json.RawMessage) []MediaReference {
	var body struct {
		wecomMediaNode
		Mixed struct {
			Items []wecomMediaNode `json:"msg_item"`
		} `json:"mixed"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return nil
	}
	items := []wecomMediaNode{body.wecomMediaNode}
	if kind == "mixed" {
		items = body.Mixed.Items
	}
	var out []MediaReference
	for _, item := range items {
		n := item.Image
		if item.Type == "file" {
			n = item.File
		} else if item.Type != "image" {
			continue
		}
		name := n.FileName
		if name == "" {
			name = n.Name
		}
		out = append(out, mediaRef(item.Type, name, n.URL, n.AESKey)...)
	}
	return out
}

// Persist only authenticated ciphertext. A download capability is usable solely for its original binding, generation, message and ordinal.
func mediaCipher(token string) (cipher.AEAD, error) {
	key := sha256.Sum256([]byte("channel-media-v1:" + token))
	block, e := aes.NewCipher(key[:])
	if e != nil {
		return nil, e
	}
	return cipher.NewGCMWithRandomNonce(block)
}
func mediaAAD(binding string, generation int64, msg string, index int) []byte {
	return []byte(fmt.Sprintf("%q/%d/%q/%d", binding, generation, msg, index))
}
func sealMedia(v Inbound, token string) (Inbound, error) {
	if len(v.Media) > 32 {
		return v, errors.New("too many channel attachments")
	}
	a, e := mediaCipher(token)
	if e != nil {
		return v, e
	}
	refs := append([]MediaReference(nil), v.Media...)
	for i := range refs {
		refs[i].Locator = base64.RawStdEncoding.EncodeToString(a.Seal(nil, nil, []byte(refs[i].Locator), mediaAAD(v.BindingID, v.Generation, v.MessageID, i)))
	}
	v.Media = refs
	return v, nil
}
func mediaURL(raw, provider string) bool {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.User != nil || u.Hostname() == "" || u.Port() != "" && u.Port() != "443" {
		return false
	}
	domains := []string{"dingtalk.com", "alicdn.com", "aliyuncs.com"}
	if provider == "wecom" {
		domains = []string{"qq.com", "myqcloud.com", "weixin.qq.com", "wxwork.cn"}
	}
	for _, h := range domains {
		if u.Hostname() == h || strings.HasSuffix(u.Hostname(), "."+h) {
			return true
		}
	}
	return false
}
func readMedia(r io.Reader, limit int64) ([]byte, error) {
	b, e := io.ReadAll(io.LimitReader(r, limit+1))
	if int64(len(b)) > limit {
		return nil, errors.New("attachment too large")
	}
	return b, e
}
func fetchMedia(ctx context.Context, b Binding, j MediaJob, l mediaLocator) ([]byte, error) {
	if b.Provider == "feishu" {
		api := lark.NewClient(b.Credentials.AppID, b.Credentials.AppSecret, lark.WithLogger(quietLogger{}), lark.WithReqTimeout(45*time.Second), lark.WithHttpClient(boundedLarkClient{}))
		r, e := api.Im.MessageResource.Get(ctx, larkim.NewGetMessageResourceReqBuilder().MessageId(j.MessageID).FileKey(l.Key).Type(j.Kind).Build())
		if e != nil || r == nil || !r.Success() || r.File == nil {
			return nil, errors.New("resource unavailable")
		}
		return readMedia(r.File, mediaLimit)
	}
	address := l.Key
	if b.Provider == "dingtalk" {
		h := newDingHTTP()
		token, e := h.token(ctx, b)
		if e != nil {
			return nil, e
		}
		var result struct {
			URL string `json:"downloadUrl"`
		}
		e = h.request(ctx, "POST", "/v1.0/robot/messageFiles/download", token, map[string]string{"robotCode": b.BotOpenID, "downloadCode": l.Key}, &result)
		if e != nil {
			return nil, e
		}
		address = result.URL
	}
	if !mediaURL(address, b.Provider) {
		return nil, errors.New("invalid resource location")
	}
	client := http.Client{Timeout: 45 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 3 || !mediaURL(req.URL.String(), b.Provider) {
			return errors.New("invalid resource redirect")
		}
		return nil
	}}
	req, e := http.NewRequestWithContext(ctx, "GET", address, nil)
	if e != nil {
		return nil, e
	}
	resp, e := client.Do(req)
	if e != nil {
		return nil, e
	}
	defer func() { _ = resp.Body.Close() }() //nolint:errcheck // Preserve transport or provider outcome.
	if resp.StatusCode != 200 {
		return nil, errors.New("resource unavailable")
	}
	data, e := readMedia(resp.Body, mediaLimit+32)
	if e != nil {
		return nil, e
	}
	if b.Provider == "wecom" {
		return decryptWecomFile(data, l.AESKey)
	}
	if len(data) > mediaLimit {
		return nil, errors.New("attachment too large")
	}
	return data, nil
}

type boundedLarkClient struct{}

func (boundedLarkClient) Do(req *http.Request) (*http.Response, error) {
	c := http.Client{Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r, e := c.Do(req)
	if e != nil {
		return nil, e
	}
	if r.ContentLength > mediaLimit {
		_ = r.Body.Close() //nolint:errcheck // Reject the oversized response.
		return nil, errors.New("attachment too large")
	}
	r.Body = struct {
		io.Reader
		io.Closer
	}{io.LimitReader(r.Body, mediaLimit+1), r.Body}
	return r, nil
}
func decryptWecomFile(data []byte, key string) ([]byte, error) {
	k, e := base64.RawStdEncoding.DecodeString(strings.TrimRight(key, "="))
	if e != nil || len(k) != 32 || len(data) == 0 || len(data)%aes.BlockSize != 0 {
		return nil, errors.New("invalid encrypted media")
	}
	block, e := aes.NewCipher(k)
	if e != nil {
		return nil, e
	}
	cipher.NewCBCDecrypter(block, k[:16]).CryptBlocks(data, data)
	pad := int(data[len(data)-1])
	if pad < 1 || pad > 32 || pad > len(data) || !bytes.Equal(data[len(data)-pad:], bytes.Repeat([]byte{byte(pad)}, pad)) || len(data)-pad > mediaLimit {
		return nil, errors.New("invalid media padding")
	}
	return data[:len(data)-pad], nil
}
func (b *Backend) processMedia(ctx context.Context, lookup func(string) (Binding, bool)) error {
	var v struct {
		Job *MediaJob `json:"job"`
	}
	if e := b.request(ctx, "POST", "/api/internal/v1/channel-media/claim", nil, &v); e != nil || v.Job == nil {
		return e
	}
	j := *v.Job
	binding, ok := lookup(j.BindingID)
	if !ok || !binding.Enabled || binding.Generation != j.Generation || binding.Provider != j.Provider {
		return nil
	}
	a, e := mediaCipher(b.Token)
	if e != nil {
		return e
	}
	encrypted, e := base64.RawStdEncoding.DecodeString(j.Locator)
	var data []byte
	if e == nil {
		var plain []byte
		plain, e = a.Open(nil, nil, encrypted, mediaAAD(j.BindingID, j.Generation, j.MessageID, j.Ordinal))
		if e == nil {
			var l mediaLocator
			e = json.Unmarshal(plain, &l)
			if e == nil {
				data, e = fetchMedia(ctx, binding, j, l)
			}
		}
	}
	return b.completeMedia(ctx, j, data, e)
}

func (b *Backend) completeMedia(ctx context.Context, j MediaJob, data []byte, downloadErr error) error {
	endpoint := fmt.Sprintf("%s/api/internal/v1/channel-media/%s/%d", b.URL, j.InboxID, j.Ordinal)
	var storeErr error
	if downloadErr == nil {
		storeErr = b.writeMediaAttempt(ctx, j.Lease, http.MethodPut, endpoint, data)
		if storeErr == nil {
			return nil
		}
	}
	// Storage and download consume the same claim. The Manager reconciles its
	// durable upload receipt before settling a failed PUT (which may have
	// succeeded even though we didn't receive the response).
	return errors.Join(storeErr, b.writeMediaAttempt(ctx, j.Lease, http.MethodPost, endpoint+"/failed", nil))
}

func (b *Backend) writeMediaAttempt(ctx context.Context, lease, method, endpoint string, data []byte) error {
	req, e := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(data))
	if e != nil {
		return e
	}
	req.Header.Set("Authorization", "Bearer "+b.Token)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Kiko-Media-Lease", lease)
	resp, e := b.HTTP.Do(req)
	if e != nil {
		return e
	}
	defer func() { _ = resp.Body.Close() }() //nolint:errcheck // Preserve transport or provider outcome.
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("channel media %s: HTTP %d", method, resp.StatusCode)
	}
	return nil
}

func jsonString(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return ""
	}
	return text
}
