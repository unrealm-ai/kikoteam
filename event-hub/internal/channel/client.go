package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type BackendError struct{ Status int }

func (e *BackendError) Error() string { return fmt.Sprintf("channel backend status %d", e.Status) }

type Backend struct {
	Logger               *slog.Logger
	notificationDelivery bool
	humanNoticeDelivery  bool
	OnObserved           func()
	URL, Token           string
	HTTP                 *http.Client
}

func NewBackend(base, token string) (*Backend, error) {
	u, e := url.Parse(base)
	if e != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || token == "" {
		return nil, errors.New("invalid channel backend configuration")
	}
	return &Backend{URL: strings.TrimRight(base, "/"), Token: token, HTTP: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// ReconcileTaskReports carries no task content; Manager owns the transaction.
func (b *Backend) ReconcileTaskReports(ctx context.Context) error {
	return b.request(ctx, "POST", "/api/internal/v1/task-results/reconcile", map[string]int{"limit": 100}, nil)
}
func (b *Backend) ReconcileModelUsage(ctx context.Context) error {
	return b.request(ctx, "POST", "/api/internal/v1/model-proxy/reconcile", nil, nil)
}
func (b *Backend) request(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		data, e := json.Marshal(in)
		if e != nil {
			return e
		}
		body = bytes.NewReader(data)
	}
	req, e := http.NewRequestWithContext(ctx, method, b.URL+path, body)
	if e != nil {
		return e
	}
	req.Header.Set("Authorization", "Bearer "+b.Token)
	if method == http.MethodPost && path == "/api/internal/v1/channel-deliveries/claim" {
		req.Header.Set("X-Kiko-Delivery-Markdown", "1")
		req.Header.Set("X-Kiko-Delivery-Media", "1")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, e := b.HTTP.Do(req)
	if e != nil {
		return errors.New("channel backend unavailable")
	}
	defer resp.Body.Close() //nolint:errcheck // Read-only HTTP body cleanup.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &BackendError{Status: resp.StatusCode}
	}
	if out != nil {
		if e = json.NewDecoder(io.LimitReader(resp.Body, responseLimit(path))).Decode(out); e != nil {
			return errors.New("invalid channel backend response")
		}
	}
	return nil
}
func responseLimit(path string) int64 {
	if strings.HasPrefix(path, "/api/internal/v1/channel-deliveries/") && strings.HasSuffix(path, "/content") {
		return (mediaLimit * 4 / 3) + 65536
	}
	return 8 << 20
}
func (b *Backend) Snapshot(ctx context.Context) (Snapshot, error) {
	var s Snapshot
	e := b.request(ctx, "GET", "/api/internal/v1/channel-bindings", nil, &s)
	return s, e
}
func (b *Backend) Applied(ctx context.Context, id string, v Applied) error {
	err := b.request(ctx, "POST", "/api/internal/v1/channel-bindings/"+url.PathEscape(id)+"/applied", v, nil)
	if err == nil && b.OnObserved != nil && (v.BotOpenID != "" || v.TenantID != "") {
		b.OnObserved()
	}
	return err
}

var inboxIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type inboundReceipt struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	ControlReply string `json:"control_reply,omitempty"`
}

func (b *Backend) Inbound(ctx context.Context, v any) error {
	_, err := b.receiveInbound(ctx, v)
	return err
}

func (b *Backend) receiveInbound(ctx context.Context, v any) (inboundReceipt, error) {
	inbound, _ := v.(Inbound)
	if len(inbound.Media) > 0 {
		sealed, e := sealMedia(inbound, b.Token)
		if e != nil {
			return inboundReceipt{}, e
		}
		v = sealed
	}
	b.traceInbound(ctx, "channel.inbound.forward", "", inbound, "started")
	outcome := "failed"
	defer func() { b.traceInbound(ctx, "channel.inbound.forward_result", "", inbound, outcome) }()

	var receipt inboundReceipt
	if err := b.request(ctx, "POST", "/api/internal/v1/channel-inbox", v, &receipt); err != nil {
		return inboundReceipt{}, err
	}
	if !inboxIDPattern.MatchString(receipt.ID) || (receipt.Status != "accepted" && receipt.Status != "duplicate") {
		return inboundReceipt{}, errors.New("invalid durable inbox receipt")
	}
	outcome = receipt.Status
	b.trace(ctx, "channel.inbound.durable_receipt", "provider", inbound.LogProvider, "binding_id", traceID(inbound.BindingID), "provider_event_id", traceID(inbound.EventID), "provider_message_id", traceID(inbound.MessageID), "inbox_id", receipt.ID, "outcome", receipt.Status)
	return receipt, nil
}
