package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ChannelNotifier persists the desired revision, not transient HTTP delivery.
// A 202 only acknowledges a refresh hint. Applied status is reported separately.
type ChannelNotifier struct {
	Store  ChannelStore
	URL    string
	Token  string
	Client *http.Client
}

func (n *ChannelNotifier) Notify(ctx context.Context) error {
	revision, e := n.Store.PendingNotification(ctx)
	if e != nil || revision == 0 {
		return e
	}
	u, e := url.Parse(n.URL)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") || len(n.Token) < 32 {
		return errors.New("invalid Hub notification configuration")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/internal/v1/channel-bindings/refresh"
	body, e := json.Marshal(map[string]int64{"revision": revision})
	if e != nil {
		return e
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if e != nil {
		return e
	}
	req.Header.Set("Authorization", "Bearer "+n.Token)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 5 * time.Second}
	if n.Client != nil {
		*client = *n.Client
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, e := client.Do(req)
	if e != nil {
		return errors.New("hub notification unavailable")
	}
	defer resp.Body.Close() //nolint:errcheck // No response data consumed.
	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusNoContent {
		return errors.New("hub notification rejected")
	}
	return n.Store.MarkNotified(ctx, revision)
}
func (n *ChannelNotifier) Run(ctx context.Context, onError func()) {
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	for {
		if e := n.Notify(ctx); e != nil && ctx.Err() == nil && onError != nil {
			onError()
		}
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}
