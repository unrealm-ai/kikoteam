package channel

import (
	"bytes"
	"context"
	"crypto/md5" // WeCom's chunk upload protocol requires this checksum.
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	im "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type deliveryMedia struct {
	ID        string `json:"artifact_id"`
	Name      string `json:"name"`
	Version   string `json:"version"`
	Size      int64  `json:"size_bytes"`
	MediaType string `json:"media_type"`
	Data      []byte `json:"-"`
}

func (b *Backend) deliveryMedia(ctx context.Context, path string, d Delivery) (*deliveryMedia, error) {
	var result struct {
		Artifact deliveryMedia `json:"artifact"`
		Data     []byte        `json:"bytes"`
	}
	if err := b.request(ctx, "POST", path+"/content", map[string]string{"lease_token": d.LeaseToken}, &result); err != nil {
		return nil, err
	}
	a := &result.Artifact
	digest := sha256.Sum256(result.Data)
	if a.ID != d.ArtifactID || a.Name == "" || int64(len(result.Data)) != a.Size || a.Size > mediaLimit || a.Version != hex.EncodeToString(digest[:]) {
		return nil, errors.New("invalid artifact version")
	}
	a.Data = result.Data
	return a, nil
}
func mediaUploadFailure() deliveryReceipt {
	return deliveryReceipt{Status: "failed", EvidenceType: "not_dispatched", ErrorCode: "media_upload_failed"}
}
func checkMediaDispatch(d Delivery) error {
	if d.Media == nil || d.Format != "image" && d.Format != "file" {
		return errors.New("invalid media")
	}
	if d.BeforeDispatch != nil {
		return d.BeforeDispatch()
	}
	return nil
}
func feishuUpload(ctx context.Context, api *lark.Client, d Delivery) (map[string]string, error) {
	if err := checkMediaDispatch(d); err != nil {
		return nil, err
	}
	reader := bytes.NewReader(d.Media.Data)
	if d.Format == "image" {
		r, e := api.Im.Image.Create(ctx, im.NewCreateImageReqBuilder().Body(im.NewCreateImageReqBodyBuilder().ImageType("message").Image(reader).Build()).Build())
		if e != nil || r == nil || !r.Success() || r.Data == nil || value(r.Data.ImageKey) == "" {
			return nil, errors.New("image upload failed")
		}
		return map[string]string{"image_key": *r.Data.ImageKey}, nil
	}
	r, e := api.Im.File.Create(ctx, im.NewCreateFileReqBuilder().Body(im.NewCreateFileReqBodyBuilder().FileType("stream").FileName(d.Media.Name).File(reader).Build()).Build())
	if e != nil || r == nil || !r.Success() || r.Data == nil || value(r.Data.FileKey) == "" {
		return nil, errors.New("file upload failed")
	}
	return map[string]string{"file_key": *r.Data.FileKey}, nil
}
func dingUpload(ctx context.Context, h dingHTTP, d Delivery, token string) (string, error) {
	if err := checkMediaDispatch(d); err != nil {
		return "", err
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := w.WriteField("type", d.Format); err != nil {
		return "", err
	}
	part, e := w.CreateFormFile("media", d.Media.Name)
	if e != nil {
		return "", e
	}
	if _, e = part.Write(d.Media.Data); e != nil {
		return "", e
	}
	if e = w.Close(); e != nil {
		return "", e
	}
	req, e := http.NewRequestWithContext(ctx, "POST", "https://oapi.dingtalk.com/media/upload?"+url.Values{"access_token": []string{token}}.Encode(), &buf)
	if e != nil {
		return "", e
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, e := h.client.Do(req)
	if e != nil {
		return "", errors.New("media upload unavailable")
	}
	defer func() { _ = resp.Body.Close() }() //nolint:errcheck // Preserve transport or provider outcome.
	var v struct {
		Code *int   `json:"errcode"`
		Type string `json:"type"`
		ID   string `json:"media_id"`
	}
	data, e := readMedia(resp.Body, 1<<20)
	if e != nil || resp.StatusCode != 200 || json.Unmarshal(data, &v) != nil || v.Code == nil || *v.Code != 0 || v.Type != d.Format || !dingRequestKey(v.ID) {
		return "", errors.New("media upload rejected")
	}
	return v.ID, nil
}
func (s *wecomSession) mediaCommand(ctx context.Context, cmd string, body any, guard func() error) (json.RawMessage, error) {
	id := wireID("upload_")
	result := make(chan wecomFrame, 1)
	s.mu.Lock()
	if s.closed || len(s.pending) >= 128 {
		s.mu.Unlock()
		return nil, errors.New("socket unavailable")
	}
	s.pending[id] = result
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.pending, id); s.mu.Unlock() }()
	if e := s.writeGuard(ctx, cmd, id, body, guard); e != nil {
		return nil, e
	}
	timer := time.NewTimer(s.timeout)
	defer timer.Stop()
	select {
	case ack := <-result:
		if ack.ErrorCode != nil && *ack.ErrorCode == 0 && ack.Headers.ReqID == id {
			return ack.Body, nil
		}
	case <-s.done:
	case <-ctx.Done():
	case <-timer.C:
	}
	return nil, errors.New("missing upload acknowledgement")
}
func (s *wecomSession) uploadMedia(ctx context.Context, d Delivery) (string, error) {
	if e := checkMediaDispatch(d); e != nil {
		return "", e
	}
	const chunk = 512 * 1024
	data := d.Media.Data
	count := (len(data) + chunk - 1) / chunk
	if count == 0 || count > 100 {
		return "", errors.New("invalid media size")
	}
	raw, e := s.mediaCommand(ctx, "aibot_upload_media_init", map[string]any{"type": d.Format, "filename": d.Media.Name, "total_size": len(data), "total_chunks": count, "md5": fmt.Sprintf("%x", md5.Sum(data))}, d.BeforeDispatch)
	var upload struct {
		ID string `json:"upload_id"`
	}
	if e != nil || json.Unmarshal(raw, &upload) != nil || !validWecomID(upload.ID) {
		return "", errors.New("invalid media upload handle")
	}
	for i := 0; i < count; i++ {
		_, e = s.mediaCommand(ctx, "aibot_upload_media_chunk", map[string]any{"upload_id": upload.ID, "chunk_index": i, "base64_data": base64.StdEncoding.EncodeToString(data[i*chunk : min((i+1)*chunk, len(data))])}, nil)
		if e != nil {
			return "", e
		}
	}
	raw, e = s.mediaCommand(ctx, "aibot_upload_media_finish", map[string]string{"upload_id": upload.ID}, d.BeforeDispatch)
	var out struct {
		ID   string `json:"media_id"`
		Type string `json:"type"`
	}
	if e != nil || json.Unmarshal(raw, &out) != nil || !dingRequestKey(out.ID) || out.Type != d.Format {
		return "", errors.New("invalid media upload receipt")
	}
	return out.ID, nil
}
