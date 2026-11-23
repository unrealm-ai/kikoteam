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
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/unrealm-ai/kikoteam/event-hub/internal/feishucontent"
)

const (
	inboundMediaLimit        = 100 << 20
	inboundMediaMaxRedirects = 5
)

type mediaLocator struct {
	Key    string `json:"key"`
	AESKey string `json:"aes_key,omitempty"`
}
type mediaNode struct {
	Type                string `json:"type"`
	DownloadCode        string `json:"downloadCode"`
	PictureDownloadCode string `json:"pictureDownloadCode"`
	URL                 string `json:"url"`
	AESKey              string `json:"aeskey"`
	FileName            string `json:"fileName"`
	Name                string `json:"name"`
}

func mediaRef(kind, name, key, aesKey string) []MediaReference {
	if key == "" || len(key) > 8192 || len(aesKey) > 128 {
		return nil
	}
	name = cleanMediaName(name)
	raw, e := json.Marshal(mediaLocator{key, aesKey})
	if e != nil {
		return nil
	}
	return []MediaReference{{Kind: kind, Name: name, Locator: string(raw)}}
}

func cleanMediaName(name string) string {
	if !utf8.ValidString(name) {
		return ""
	}
	name = path.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	if name == "." || name == ".." || name == "/" || strings.TrimSpace(name) == "" {
		return ""
	}
	for len(name) > 220 {
		_, size := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-size]
	}
	return name
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
				out = append(out, mediaRef("image", item.FileName, item.dingPictureKey(), "")...)
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
		return mediaRef("image", n.FileName, n.dingPictureKey(), "")
	case "file":
		return mediaRef("file", n.FileName, n.DownloadCode, "")
	}
	return nil
}

func (n mediaNode) dingPictureKey() string {
	if n.DownloadCode != "" {
		return n.DownloadCode
	}
	return n.PictureDownloadCode
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

// The structured rejection is deliberately separate from net/url errors, which
// may contain the full signed URL. Its wire classification remains unchanged.
type mediaURLRejection struct {
	stage, reason, hostname string
	redirectHop             int
}

func (*mediaURLRejection) Error() string { return string(mediaDownloadURLInvalid) }
func (*mediaURLRejection) Unwrap() error { return mediaDownloadURLInvalid }

func mediaURLRejectionError(provider, stage, reason, hostname string, hop int) error {
	if provider != "dingtalk" {
		return mediaDownloadURLInvalid
	}
	return &mediaURLRejection{stage: stage, reason: reason, hostname: safeMediaHostname(hostname), redirectHop: hop}
}

// Only DNS hostnames are useful diagnostics for this provider. Exclude IPs and
// malformed or unbounded host values; never take a hostname from a parse error.
func safeMediaHostname(host string) string {
	if len(host) == 0 || len(host) > 253 || net.ParseIP(strings.TrimSuffix(host, ".")) != nil {
		return ""
	}
	for label := range strings.SplitSeq(strings.TrimSuffix(host, "."), ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return ""
		}
		for _, c := range label {
			if !asciiMediaHostChar(c) {
				return ""
			}
		}
	}
	return strings.ToLower(host)
}

func asciiMediaHostChar(c rune) bool {
	return c == '-' || c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z'
}

type mediaURLPolicy string

const resolvedDingMediaPolicy mediaURLPolicy = "dingtalk"

func (p mediaURLPolicy) rejectReason(raw string) (reason, hostname string) {
	u, e := url.Parse(raw)
	if e != nil {
		return "malformed_url", ""
	}
	host := u.Hostname()
	switch {
	case u.Scheme != "https" && (p != resolvedDingMediaPolicy || u.Scheme != "http"):
		return "unexpected_scheme", host
	case u.User != nil:
		return "userinfo_present", host
	case host == "":
		return "missing_hostname", ""
	}
	if p == resolvedDingMediaPolicy {
		return resolvedDingMediaRejectReason(u, host)
	}
	if u.Port() != "" && u.Port() != "443" {
		return "unexpected_port", host
	}
	if p != "wecom" || safeMediaHostname(host) == "" {
		return "hostname_not_allowed", host
	}
	return "", host
}

// Only syntactically valid DNS names reach the guarded public-IP transport.
func resolvedDingMediaRejectReason(u *url.URL, host string) (reason, hostname string) {
	port := "443"
	if u.Scheme == "http" {
		port = "80"
	}
	if safeMediaHostname(host) == "" || !strings.Contains(strings.TrimSuffix(host, "."), ".") {
		return "invalid_dns_hostname", host
	}
	if u.Host != host && u.Host != host+":"+port {
		return "unexpected_port", host
	}
	_, queryErr := url.QueryUnescape(u.RawQuery)
	if queryErr != nil || strings.Contains(u.RawQuery, " ") || (u.RawPath != "" && u.RawPath != u.EscapedPath()) {
		return "malformed_url", host
	}
	return "", host
}

type inboundMediaFailure string

func (f inboundMediaFailure) Error() string { return string(f) }

const (
	mediaBindingUnavailable inboundMediaFailure = "binding_unavailable"
	mediaLocatorInvalid     inboundMediaFailure = "locator_invalid"
	mediaProviderAuth       inboundMediaFailure = "provider_auth"
	mediaProviderResolve    inboundMediaFailure = "provider_resolve"
	mediaDownloadURLInvalid inboundMediaFailure = "download_url_invalid"
	mediaDownloadTimeout    inboundMediaFailure = "download_timeout"
	mediaDownloadHTTP       inboundMediaFailure = "download_http"
	mediaDownloadFailed     inboundMediaFailure = "download_failed"
	mediaTooLarge           inboundMediaFailure = "file_too_large"
	mediaDecryptFailed      inboundMediaFailure = "decrypt_failed"
	mediaStorageFailed      inboundMediaFailure = "storage_failed"
)

func mediaFailureCode(e error, fallback inboundMediaFailure) inboundMediaFailure {
	var code inboundMediaFailure
	if errors.As(e, &code) {
		return code
	}
	var transport net.Error
	if errors.Is(e, context.DeadlineExceeded) || errors.As(e, &transport) && transport.Timeout() {
		return mediaDownloadTimeout
	}
	return fallback
}

type fetchedMedia struct {
	data []byte
	name string // Optional provider response basename; never used as a trusted path.
}

func readMedia(r io.Reader, limit int64) ([]byte, error) {
	b, e := io.ReadAll(io.LimitReader(r, limit+1))
	if int64(len(b)) > limit {
		return nil, mediaTooLarge
	}
	return b, e
}

func fetchMedia(ctx context.Context, b Binding, j MediaJob, l mediaLocator) (fetchedMedia, error) {
	if b.Provider == "feishu" {
		return fetchFeishuMedia(ctx, b, j, l)
	}
	if b.Provider == "dingtalk" {
		address, e := resolveDingMedia(ctx, b, l)
		if e != nil {
			return fetchedMedia{}, e
		}
		// Only the official authenticated resolver supplies this URL. Never
		// treat the callback's downloadCode itself as a download address.
		return fetchMediaURL(ctx, b.Provider, address, l.AESKey, j.AllowNonPublicIP)
	}
	return fetchMediaURL(ctx, b.Provider, l.Key, l.AESKey, j.AllowNonPublicIP)
}

func fetchFeishuMedia(ctx context.Context, b Binding, j MediaJob, l mediaLocator) (fetchedMedia, error) {
	api := lark.NewClient(b.Credentials.AppID, b.Credentials.AppSecret, lark.WithLogger(quietLogger{}), lark.WithReqTimeout(45*time.Second), lark.WithHttpClient(boundedLarkClient{}))
	r, e := api.Im.MessageResource.Get(ctx, larkim.NewGetMessageResourceReqBuilder().MessageId(j.MessageID).FileKey(l.Key).Type(j.Kind).Build())
	if e != nil {
		return fetchedMedia{}, mediaFailureCode(e, mediaDownloadFailed)
	}
	if r == nil || r.ApiResp == nil {
		return fetchedMedia{}, mediaDownloadFailed
	}
	switch {
	case r.StatusCode == http.StatusUnauthorized || r.StatusCode == http.StatusForbidden || r.Code == 234002 || r.Code == 234009:
		return fetchedMedia{}, mediaProviderAuth
	case len(r.RawBody) > inboundMediaLimit || r.Code == 234037:
		return fetchedMedia{}, mediaTooLarge
	case r.StatusCode != http.StatusOK || !r.Success() || r.File == nil:
		return fetchedMedia{}, mediaDownloadHTTP
	}
	// The pinned SDK already buffers the stream. Reuse the bounded bytes
	// rather than making a second full copy by reading its bytes.Buffer.
	return fetchedMedia{data: r.RawBody}, nil
}

func resolveDingMedia(ctx context.Context, b Binding, l mediaLocator) (string, error) {
	h := newDingHTTP()
	token, e := h.token(ctx, b)
	if e != nil {
		return "", mediaProviderAuth
	}
	var result struct {
		URL string `json:"downloadUrl"`
	}
	e = h.request(ctx, "POST", "/v1.0/robot/messageFiles/download", token, map[string]string{"robotCode": b.BotOpenID, "downloadCode": l.Key}, &result)
	if e != nil || result.URL == "" {
		return "", mediaProviderResolve
	}
	return result.URL, nil
}

func fetchMediaURL(ctx context.Context, provider, address, aesKey string, allowNonPublicIP bool) (fetchedMedia, error) {
	transport := mediaDownloadTransport
	if allowNonPublicIP && (provider == "dingtalk" || provider == "wecom") {
		transport = mediaPrivateDownloadTransport
	}
	return fetchMediaURLWithTransport(ctx, mediaURLPolicy(provider), address, aesKey, transport)
}

func fetchMediaURLWithTransport(ctx context.Context, policy mediaURLPolicy, address, aesKey string, transport http.RoundTripper) (fetchedMedia, error) {
	provider := string(policy)
	if reason, host := policy.rejectReason(address); reason != "" {
		return fetchedMedia{}, mediaURLRejectionError(provider, "initial", reason, host, 0)
	}
	client := http.Client{Timeout: 45 * time.Second, CheckRedirect: policy.checkRedirect, Transport: transport}
	req, e := http.NewRequestWithContext(ctx, "GET", address, nil)
	if e != nil {
		return fetchedMedia{}, mediaURLRejectionError(provider, "initial", "invalid_request", "", 0)
	}
	resp, e := client.Do(req)
	if e != nil {
		var rejected *mediaURLRejection
		if errors.As(e, &rejected) {
			return fetchedMedia{}, rejected // Drop the outer URL-bearing transport error.
		}
		return fetchedMedia{}, mediaFailureCode(e, mediaDownloadFailed)
	}
	defer func() { _ = resp.Body.Close() }() //nolint:errcheck // Preserve transport or provider outcome.
	return decodeMediaDownload(resp, provider, aesKey)
}

func (p mediaURLPolicy) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) > inboundMediaMaxRedirects {
		return mediaURLRejectionError(string(p), "redirect", "redirect_limit", req.URL.Hostname(), len(via))
	}
	if reason, host := p.rejectReason(req.URL.String()); reason != "" {
		return mediaURLRejectionError(string(p), "redirect", reason, host, len(via))
	}
	// net/http builds Referer from the previous URL, including its query.
	req.Header.Del("Referer")
	return nil
}

func decodeMediaDownload(resp *http.Response, provider, aesKey string) (fetchedMedia, error) {
	if resp.StatusCode != http.StatusOK {
		return fetchedMedia{}, mediaDownloadHTTP
	}
	limit := int64(inboundMediaLimit)
	if provider == "wecom" {
		limit += 32 // Bound encrypted bytes too, allowing the documented CBC padding.
	}
	if resp.ContentLength > limit {
		return fetchedMedia{}, mediaTooLarge
	}
	data, e := readMedia(resp.Body, limit)
	if e != nil {
		return fetchedMedia{}, mediaFailureCode(e, mediaDownloadFailed)
	}
	if provider != "wecom" {
		return fetchedMedia{data: data}, nil
	}
	data, e = decryptWecomFile(data, aesKey)
	if e != nil {
		return fetchedMedia{}, mediaFailureCode(e, mediaDecryptFailed)
	}
	return fetchedMedia{data: data, name: mediaDispositionName(resp.Header.Get("Content-Disposition"))}, nil
}

func mediaDispositionName(raw string) string {
	if len(raw) == 0 || len(raw) > 4096 {
		return ""
	}
	kind, params, e := mime.ParseMediaType(raw)
	if e != nil || kind != "attachment" && kind != "inline" {
		return ""
	}
	return cleanMediaName(params["filename"])
}

type boundedLarkClient struct{}

func (boundedLarkClient) Do(req *http.Request) (*http.Response, error) {
	c := http.Client{Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r, e := c.Do(req)
	if e != nil {
		return nil, e
	}
	if r.ContentLength > inboundMediaLimit {
		_ = r.Body.Close() //nolint:errcheck // Reject the oversized response.
		return nil, mediaTooLarge
	}
	r.Body = struct {
		io.Reader
		io.Closer
	}{io.LimitReader(r.Body, inboundMediaLimit+1), r.Body}
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
	if pad < 1 || pad > 32 || pad > len(data) || !bytes.Equal(data[len(data)-pad:], bytes.Repeat([]byte{byte(pad)}, pad)) {
		return nil, errors.New("invalid media padding")
	}
	if len(data)-pad > inboundMediaLimit {
		return nil, mediaTooLarge
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
	b.traceMedia(ctx, j, "claimed", "")
	binding, ok := lookup(j.BindingID)
	if !ok || !binding.Enabled || binding.Generation != j.Generation || binding.Provider != j.Provider {
		return b.completeMedia(ctx, j, fetchedMedia{}, mediaBindingUnavailable)
	}
	l, e := b.openMediaLocator(j)
	if e != nil {
		return b.completeMedia(ctx, j, fetchedMedia{}, e)
	}
	download, cancel := context.WithTimeout(ctx, 70*time.Second)
	data, e := fetchMedia(download, binding, j, l)
	cancel()
	return b.completeMedia(ctx, j, data, e)
}

func (b *Backend) openMediaLocator(j MediaJob) (mediaLocator, error) {
	var l mediaLocator
	a, e := mediaCipher(b.Token)
	if e != nil {
		return l, mediaLocatorInvalid
	}
	encrypted, e := base64.RawStdEncoding.DecodeString(j.Locator)
	if e != nil {
		return l, mediaLocatorInvalid
	}
	plain, e := a.Open(nil, nil, encrypted, mediaAAD(j.BindingID, j.Generation, j.MessageID, j.Ordinal))
	if e != nil || json.Unmarshal(plain, &l) != nil || l.Key == "" {
		return l, mediaLocatorInvalid
	}
	return l, nil
}

func (b *Backend) traceMedia(ctx context.Context, j MediaJob, outcome string, code inboundMediaFailure) {
	b.trace(ctx, "channel.media", "provider", traceID(j.Provider), "binding_id", traceID(j.BindingID), "inbox_id", traceID(j.InboxID),
		"ordinal", j.Ordinal, "outcome", outcome, "error_code", string(code))
}

func (b *Backend) completeMedia(ctx context.Context, j MediaJob, item fetchedMedia, downloadErr error) error {
	if ctx.Err() != nil {
		return ctx.Err() // On term exit do not dispatch further settlements; the lease expires.
	}
	endpoint := fmt.Sprintf("%s/api/internal/v1/channel-media/%s/%d", b.URL, j.InboxID, j.Ordinal)
	var storeErr error
	code := mediaFailureCode(downloadErr, mediaDownloadFailed)
	if downloadErr == nil {
		storeErr = b.writeMediaAttempt(ctx, j.Lease, http.MethodPut, endpoint, item, "")
		if storeErr == nil {
			b.traceMedia(ctx, j, "stored", "")
			return nil
		}
		code = mediaStorageFailed
	}
	b.traceMedia(ctx, j, "attempt_failed", code)
	var rejected *mediaURLRejection
	if j.Provider == "dingtalk" && errors.As(downloadErr, &rejected) {
		b.trace(ctx, "channel.media_download_url_rejected", "provider", j.Provider, "binding_id", traceID(j.BindingID),
			"inbox_id", traceID(j.InboxID), "ordinal", j.Ordinal, "error_code", string(code),
			"stage", rejected.stage, "reason", rejected.reason, "hostname", rejected.hostname, "redirect_hop", rejected.redirectHop)
	}
	// The Manager reconciles any durable upload before settling the same
	// claim as failed. An ambiguous PUT must not trigger a second upload here.
	e := b.writeMediaAttempt(ctx, j.Lease, http.MethodPost, endpoint+"/failed", fetchedMedia{}, code)
	if e != nil {
		b.traceMedia(ctx, j, "failure_settlement_unavailable", code)
	}
	return errors.Join(storeErr, e)
}

func (b *Backend) writeMediaAttempt(ctx context.Context, lease, method, endpoint string, item fetchedMedia, code inboundMediaFailure) error {
	req, e := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(item.data))
	if e != nil {
		return mediaStorageFailed
	}
	req.Header.Set("Authorization", "Bearer "+b.Token)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Kiko-Media-Lease", lease)
	if method == http.MethodPut && item.name != "" {
		req.Header.Set("X-Kiko-Media-Name", url.PathEscape(item.name))
	}
	if method == http.MethodPost && code != "" {
		req.Header.Set("X-Kiko-Media-Failure", string(code))
	}
	resp, e := b.HTTP.Do(req)
	if e != nil {
		return mediaStorageFailed
	}
	defer func() { _ = resp.Body.Close() }() //nolint:errcheck // Preserve transport or provider outcome.
	if resp.StatusCode != http.StatusOK {
		return mediaStorageFailed
	}
	return nil
}

const inboundMediaWorkers = 2

// Keep full validated payloads per operation so a truncated download never
// becomes a project file. This deliberately bounds concurrent buffered media.
func (s *Supervisor) runInboundMedia(ctx context.Context) {
	var workers sync.WaitGroup
	for range inboundMediaWorkers {
		workers.Go(func() {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if e := s.Backend.processMedia(ctx, s.Current); e != nil && ctx.Err() == nil {
						s.warn("channel.media_retry")
					}
				}
			}
		})
	}
	workers.Wait()
}

func jsonString(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return ""
	}
	return text
}
