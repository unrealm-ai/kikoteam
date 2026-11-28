package controller

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	proxy "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy"
	upstream "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy/integration"
	repo "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy/repository"
	mp "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy/service"
	platform "github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type Calls interface {
	Admit(context.Context, string, string, string, time.Time) (proxy.Call, error)
	Record(context.Context, string, string, string, string, *proxy.Usage) error
	Settle(context.Context, string) error
	Reconcile(context.Context) error
}

type Proxy struct {
	ReconcileImages func(context.Context) error
	Calls           Calls
	Secrets         mp.Secrets
	Upstream        upstream.Upstream
	Log             *slog.Logger
}

func (p Proxy) Routes(mux *http.ServeMux, hubToken string) {
	for path, protocol := range map[string]string{
		"chat/completions": "chat_completions", "responses": "openai_responses", "messages": "anthropic_messages",
	} {
		mux.HandleFunc("POST /model-proxy/v1/"+path, func(w http.ResponseWriter, r *http.Request) { p.serve(w, r, protocol) })
	}
	mux.HandleFunc("/model-proxy/", func(w http.ResponseWriter, _ *http.Request) { writeError(w, "", 404, "not_found", "接口不存在") })
	mux.HandleFunc("POST /api/internal/v1/model-proxy/reconcile", func(w http.ResponseWriter, r *http.Request) {
		if hubToken == "" || r.Header.Get("Origin") != "" || r.Header.Get("Cookie") != "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+hubToken)) != 1 {
			writeError(w, "", 401, "unauthorized", "服务身份无效")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 9*time.Second)
		defer cancel()
		err := p.Calls.Reconcile(ctx)
		if p.ReconcileImages != nil {
			err = errors.Join(err, p.ReconcileImages(ctx))
		}
		if err != nil {
			writeError(w, "", 503, "reconcile_pending", "仍有待结算记录")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func writeError(w http.ResponseWriter, protocol string, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	body := map[string]any{"error": map[string]string{"code": code, "type": code, "message": message}}
	if protocol == "anthropic_messages" {
		body["type"] = "error"
	}
	_ = json.NewEncoder(w).Encode(body) //nolint:errcheck // Cannot send another response after a write failure.
}

func requestKey(r *http.Request, protocol string) string {
	if r.Header.Get("Cookie") != "" || r.Header.Get("Origin") != "" || len(r.Header.Values("Authorization")) > 1 || len(r.Header.Values("X-Api-Key")) > 1 {
		return ""
	}
	auth, api := r.Header.Get("Authorization"), r.Header.Get("X-Api-Key")
	if protocol == "anthropic_messages" {
		if auth != "" {
			return ""
		}
		return api
	}
	if api != "" {
		return ""
	}
	key, ok := strings.CutPrefix(auth, "Bearer ")
	if !ok {
		return ""
	}
	return key
}

func readBody(w http.ResponseWriter, r *http.Request) (map[string]json.RawMessage, string, error) {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, upstream.MaxBody))
	var body map[string]json.RawMessage
	if e := d.Decode(&body); e != nil {
		return nil, "", e
	}
	var extra json.RawMessage
	if d.Decode(&extra) != io.EOF {
		return nil, "", errors.New("invalid model request body")
	}
	var id string
	if json.Unmarshal(body["model"], &id) != nil || !repo.UUID(id) {
		return nil, "", errors.New("invalid model id")
	}
	if raw := body["stream"]; len(raw) > 0 && string(raw) != "true" && string(raw) != "false" {
		return nil, "", errors.New("invalid stream option")
	}
	if raw := body["stream_options"]; len(raw) > 0 {
		var opts map[string]json.RawMessage
		if json.Unmarshal(raw, &opts) != nil {
			return nil, "", errors.New("invalid stream options")
		}
	}
	return body, strings.ToLower(id), nil
}

func (p Proxy) serve(w http.ResponseWriter, r *http.Request, protocol string) {
	key := requestKey(r, protocol)
	if len(key) != 54 || !strings.HasPrefix(key, "kiko_agent_") {
		writeError(w, protocol, 401, "invalid_api_key", "代理凭据无效")
		return
	}
	body, id, e := readBody(w, r)
	if e != nil {
		writeError(w, protocol, 400, "invalid_request", "模型请求无效")
		return
	}
	// Bound each call, including a brief opportunity to meter after disconnect.
	deadline := time.Now().Add(3 * time.Minute)
	call, e := p.Calls.Admit(r.Context(), key, id, protocol, deadline)
	if e != nil {
		var failure *platform.Error
		if errors.As(e, &failure) {
			writeError(w, protocol, failure.Status, failure.Code, failure.Message)
		} else {
			writeError(w, protocol, 503, "model_proxy_unavailable", "模型代理暂不可用")
		}
		return
	}
	w.Header().Set("X-Model-Call-ID", call.ID)
	e = http.NewResponseController(w).SetWriteDeadline(deadline.Add(12 * time.Second))
	if e != nil && !errors.Is(e, http.ErrNotSupported) {
		p.finish(r.Context(), call.ID, "no_charge", "downstream_error", "", nil)
		return
	}
	p.forward(w, r, call, body, deadline)
}

func (p Proxy) forward(w http.ResponseWriter, r *http.Request, call proxy.Call, body map[string]json.RawMessage, deadline time.Time) {
	m := call.Model
	key, e := p.Secrets.Open("provider", m.ID, m.Credential)
	if e != nil {
		p.finish(r.Context(), call.ID, "no_charge", "credential_unavailable", "", nil)
		writeError(w, m.Protocol, 503, "credential_unavailable", "供应商配置暂不可用")
		return
	}
	ctx, cancel := context.WithDeadline(context.WithoutCancel(r.Context()), deadline)
	defer cancel()
	resp, e := p.Upstream.Request(ctx, m, key, body, r.Header)
	if e != nil {
		p.finish(ctx, call.ID, "pending_review", "upstream_unavailable", "", nil)
		writeError(w, m.Protocol, 502, "upstream_unavailable", "模型服务未完成响应")
		return
	}
	ok := resp.StatusCode >= 200 && resp.StatusCode < 300
	contentType := resp.Header.Get("Content-Type")
	stream := strings.HasPrefix(strings.ToLower(contentType), "text/event-stream")
	requestID := resp.Header.Get("request-id")
	if requestID == "" {
		requestID = resp.Header.Get("x-request-id")
	}
	if len(requestID) > 256 || key != "" && strings.Contains(requestID, key) {
		requestID = ""
	}
	var sent, disconnected bool
	usage, readErr := upstream.Read(resp, m.Protocol, func(raw []byte) {
		if !ok || disconnected || r.Context().Err() != nil {
			return
		}
		// Never relay a supplier credential if an upstream includes it in its response.
		if key != "" && strings.Contains(string(raw), key) {
			disconnected = true
			return
		}
		if !sent {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "application/json")
			if stream {
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("X-Accel-Buffering", "no")
			}
			w.WriteHeader(resp.StatusCode)
			sent = true
		}
		_, e := w.Write(raw)
		if e == nil && stream {
			e = http.NewResponseController(w).Flush()
		}
		disconnected = e != nil
	})
	status, code := "pending_review", "usage_unavailable"
	if !ok || readErr != nil {
		code = "upstream_error"
	}
	if usage != nil {
		status = "usage_recorded"
		if ok && readErr == nil {
			code = ""
		}
	}
	p.finish(ctx, call.ID, status, code, requestID, usage)
	if !sent && r.Context().Err() == nil {
		writeError(w, m.Protocol, 502, "upstream_error", "模型服务未完成响应")
	}
}

func (p Proxy) finish(ctx context.Context, id, status, code, upstreamID string, usage *proxy.Usage) {
	done, cancel := context.WithTimeout(context.WithoutCancel(ctx), 8*time.Second)
	defer cancel()
	e := p.Calls.Record(done, id, status, code, upstreamID, usage)
	if e == nil && usage != nil {
		e = p.Calls.Settle(done, id)
	}
	if e != nil && p.Log != nil {
		p.Log.Warn("Model call needs reconciliation", "event", "model_proxy.settlement_pending", "call_id", id)
	}
}
