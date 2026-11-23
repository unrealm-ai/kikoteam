package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

// dingHTTP has no configurable production origin, redirects or automatic retries.
// In particular an ambiguous non-idempotent send is never dispatched a second time.
type dingHTTP struct {
	client *http.Client
	origin string
}

func newDingHTTP() dingHTTP {
	return dingHTTP{client: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, origin: "https://api.dingtalk.com"}
}
func (h dingHTTP) do(ctx context.Context, method, path, token string, body any) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, errors.New("invalid dingtalk request")
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, h.origin+path, reader)
	if err != nil {
		return nil, errors.New("invalid dingtalk request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("x-acs-dingtalk-access-token", token)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, errors.New("dingtalk provider unavailable")
	}
	return resp, nil
}

func (h dingHTTP) request(ctx context.Context, method, path, token string, body, out any) error {
	resp, err := h.do(ctx, method, path, token, body)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }() //nolint:errcheck // Read-only response cleanup.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.New("dingtalk provider rejected")
	}
	return decodeDingResponse(resp.Body, out)
}

func decodeDingResponse(body io.Reader, out any) error {
	data, err := io.ReadAll(io.LimitReader(body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 || json.Unmarshal(data, out) != nil {
		return errors.New("invalid dingtalk provider response")
	}
	return nil
}
func (h dingHTTP) token(ctx context.Context, b Binding) (string, error) {
	var v struct {
		Token  string          `json:"accessToken"`
		Expire int64           `json:"expireIn"`
		Code   json.RawMessage `json:"code"`
	}
	if err := h.request(ctx, "POST", "/v1.0/oauth2/accessToken", "", map[string]string{"appKey": b.Credentials.AppID, "appSecret": b.Credentials.AppSecret}, &v); err != nil {
		return "", err
	}
	if len(v.Code) != 0 || v.Token == "" || len(v.Token) > 8192 || strings.ContainsAny(v.Token, "\r\n\x00") || v.Expire <= 0 {
		return "", errors.New("invalid dingtalk credentials")
	}
	return v.Token, nil
}
func sendDingTalkDelivery(ctx context.Context, b Binding, d Delivery) deliveryReceipt {
	return sendDingTalkWithHTTP(ctx, b, d, newDingHTTP())
}
func dingTarget(b Binding, d Delivery) bool {
	return b.Provider == "dingtalk" && dingID(b.BotOpenID) && dingID(b.TenantID) && d.ThreadID == "" && ((d.ChatType == "direct" && dingID(d.RecipientID)) || (d.ChatType == "group" && dingID(d.ChatID) && d.RecipientID == ""))
}
func dingRequestKey(v string) bool {
	return v != "" && len(v) <= 2048 && !strings.ContainsAny(v, "\r\n\t \x00")
}
func sendDingTalkWithHTTP(ctx context.Context, b Binding, d Delivery, h dingHTTP) deliveryReceipt {
	if !dingTarget(b, d) || len(d.Text) > 65536 || strings.TrimSpace(d.Text) == "" && d.ArtifactID == "" || strings.ContainsRune(d.Text, 0) {
		return deliveryReceipt{Status: "failed", EvidenceType: "not_dispatched", ErrorCode: "invalid_dingtalk_target"}
	}
	var msgKey string
	var fields map[string]string
	switch d.contentFormat() {
	case "image", "file":
		if d.Media == nil {
			return mediaUploadFailure()
		}
	case "text":
		msgKey = "sampleText"
		fields = map[string]string{"content": d.Text}
	case "markdown":
		msgKey = "sampleMarkdown"
		fields = map[string]string{"title": "AI 员工回复", "text": d.Text}
	default:
		return deliveryReceipt{Status: "failed", EvidenceType: "not_dispatched", ErrorCode: "unsupported_delivery_format"}
	}
	token, err := h.token(ctx, b)
	if err != nil {
		return deliveryReceipt{Status: "failed", EvidenceType: "not_dispatched", ErrorCode: "dingtalk_authentication_failed"}
	}
	if d.Media != nil {
		mediaID, e := dingUpload(ctx, h, d, token)
		if e != nil {
			return mediaUploadFailure()
		}
		if d.Format == "image" {
			msgKey = "sampleImageMsg"
			fields = map[string]string{"photoURL": mediaID}
		} else {
			msgKey = "sampleFile"
			fileType := strings.TrimPrefix(strings.ToLower(filepath.Ext(d.Media.Name)), ".")
			if fileType == "" {
				fileType = "bin"
			}
			fields = map[string]string{"mediaId": mediaID, "fileName": d.Media.Name, "fileType": fileType}
		}
	}
	if d.BeforeDispatch != nil {
		if err := d.BeforeDispatch(); err != nil {
			return deliveryReceipt{Status: "failed", EvidenceType: "not_dispatched", ErrorCode: "binding_changed_before_send"}
		}
	}
	param, err := json.Marshal(fields)
	if err != nil {
		return deliveryReceipt{Status: "failed", EvidenceType: "not_dispatched", ErrorCode: "invalid_text"}
	}
	body := map[string]any{"robotCode": b.BotOpenID, "msgKey": msgKey, "msgParam": string(param)}
	path := "/v1.0/robot/groupMessages/send"
	if d.ChatType == "direct" {
		path = "/v1.0/robot/oToMessages/batchSend"
		body["userIds"] = []string{d.RecipientID}
	} else {
		body["openConversationId"] = d.ChatID
	}
	result, rejection := h.proactiveSend(ctx, path, token, body)
	if rejection != "" {
		// Diagnostic refinement only. Keep the conservative delivery state,
		// with no new confirmation evidence or permission to retry.
		receipt := deliveryReceipt{Status: "unknown", ErrorCode: rejection}
		if rejection == dingProactivePermissionDenied {
			receipt.EvidenceType = "send_response"
		}
		return receipt
	}
	if d.ChatType == "direct" {
		for _, list := range [][]string{result.Invalid, result.Limited} {
			for _, id := range list {
				if id != d.RecipientID {
					return deliveryReceipt{Status: "unknown", ErrorCode: "dingtalk_receipt_invalid"}
				}
			}
		}
		if len(result.Invalid) > 0 || len(result.Limited) > 0 {
			return deliveryReceipt{Status: "failed", EvidenceType: "send_response", ErrorCode: "dingtalk_recipient_rejected"}
		}
	}
	if !dingRequestKey(result.Key) {
		return deliveryReceipt{Status: "unknown", ErrorCode: "dingtalk_request_id_missing"}
	}
	return deliveryReceipt{Status: "accepted", ProviderRequestID: result.Key, EvidenceType: "send_response", EvidenceID: result.Key}
}

const (
	dingProactivePermissionDenied = "dingtalk_proactive_permission_denied"
	dingSendUnknown               = "dingtalk_send_unknown"
	// Listed in the DingTalk OpenAPI error catalog as missing interface permission.
	dingPermissionDeniedCode = "Forbidden.AccessDenied.AccessTokenPermissionDenied"
)

type dingActiveSendResponse struct {
	Key     string          `json:"processQueryKey"`
	Invalid []string        `json:"invalidStaffIdList"`
	Limited []string        `json:"flowControlledStaffIdList"`
	Code    json.RawMessage `json:"code"`
	ErrCode json.RawMessage `json:"errcode"`
}

func (h dingHTTP) proactiveSend(ctx context.Context, path, token string, body any) (dingActiveSendResponse, string) {
	var result dingActiveSendResponse
	resp, err := h.do(ctx, http.MethodPost, path, token, body)
	if err != nil {
		return result, dingSendUnknown
	}
	defer func() { _ = resp.Body.Close() }() //nolint:errcheck // Read-only response cleanup.
	status := resp.StatusCode
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		if status == http.StatusForbidden && decodeDingResponse(resp.Body, &result) == nil && result.Key == "" && result.permissionDenied() {
			return result, dingProactivePermissionDenied
		}
		return result, dingSendUnknown
	}
	if decodeDingResponse(resp.Body, &result) != nil {
		return result, dingSendUnknown
	}
	if result.permissionDenied() {
		if result.Key != "" {
			return result, dingSendUnknown // Contradictory provider evidence; do not confirm this send.
		}
		return result, dingProactivePermissionDenied
	}
	if len(result.ErrCode) != 0 && string(result.ErrCode) != "0" {
		return result, "dingtalk_send_rejected"
	}
	return result, ""
}

func (v dingActiveSendResponse) permissionDenied() bool {
	for _, raw := range []json.RawMessage{v.Code, v.ErrCode} {
		var code string
		if json.Unmarshal(raw, &code) == nil && code == dingPermissionDeniedCode {
			return true
		}
	}
	return false
}

func queryDingTalkDelivery(ctx context.Context, b Binding, d Delivery) deliveryReceipt {
	return queryDingTalkWithHTTP(ctx, b, d, newDingHTTP())
}
func queryDingTalkWithHTTP(ctx context.Context, b Binding, d Delivery, h dingHTTP) deliveryReceipt {
	unknown := deliveryReceipt{Status: "unknown", ProviderRequestID: d.ProviderRequestID, ErrorCode: "dingtalk_status_unknown"}
	if !dingTarget(b, d) || !dingRequestKey(d.ProviderRequestID) {
		return unknown
	}
	token, err := h.token(ctx, b)
	if err != nil {
		return unknown
	}
	if d.BeforeDispatch != nil {
		if err := d.BeforeDispatch(); err != nil {
			return unknown
		}
	}
	body := map[string]any{"robotCode": b.BotOpenID, "processQueryKey": d.ProviderRequestID, "openConversationId": d.ChatID, "maxResults": 1}
	path, method := "/v1.0/robot/groupMessages/query", "POST"
	var payload any = body
	if d.ChatType == "direct" {
		method = "GET"
		path = "/v1.0/robot/oToMessages/readStatus?" + url.Values{"robotCode": []string{b.BotOpenID}, "processQueryKey": []string{d.ProviderRequestID}}.Encode()
		payload = nil
	}
	var v struct {
		Status    string          `json:"sendStatus"`
		Code      json.RawMessage `json:"code"`
		ErrorCode json.RawMessage `json:"errcode"`
	}
	if err = h.request(ctx, method, path, token, payload, &v); err != nil {
		return unknown
	}
	for _, code := range []json.RawMessage{v.Code, v.ErrorCode} {
		if len(code) > 0 && string(code) != "0" {
			return unknown
		}
	}
	receipt := deliveryReceipt{ProviderRequestID: d.ProviderRequestID, EvidenceType: "status_query", EvidenceID: d.ProviderRequestID}
	switch v.Status {
	case "SUCCESS":
		receipt.Status = "sent"
	case "PROCESSING":
		receipt.Status = "accepted"
	case "RECALLED":
		receipt.Status = "failed"
		receipt.ErrorCode = "dingtalk_message_recalled"
	default:
		return unknown
	}
	return receipt
}
